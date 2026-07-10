// goal.go - the pure, testable core of becky-goal: the durable goal-object
// store and the four actions on it (add / list / update-status / note).
// main.go is only flag parsing + wiring, so everything with a decision in it
// lives here and can be unit-tested with no real args.
//
// WHY THIS TOOL EXISTS (docs/research/manus-gap-analysis.md, MANUS-GAP FIX #3):
// goal MEMORY that outlives any single Claude Code session - a plain-words
// outcome ("get the childcare email back", "ship the weekly video") recorded to
// a JSON file that survives the tick that created it, so the next tick, or the
// GUI, or Whoretana, can see what Jordan is still waiting on. This is the thing
// Manus has that the stack didn't: durable intent.
//
// Safety posture (AUTOPILOT.md Law 8b - "DELETE NOTHING OF JORDAN'S. EVER."):
// the store is ADDITIVE. There is NO delete action. update-status only changes
// a goal's status field; note only appends progress. A store file that is
// present but unparseable is REFUSED (typed error, exit 1) and left byte-for-
// byte untouched - never overwritten, because a corrupt-looking file may still
// be recoverable and blowing it away is exactly the data loss Law 8b forbids.
// Writes are atomic (temp file + rename on the same volume) so a crash mid-write
// can never truncate the store MissionControl reads.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"becky-go/internal/pathx"
)

// Goal is one durable outcome record. It is the on-disk shape and the CLI
// output shape - one struct, no mapping layer (KISS).
type Goal struct {
	ID       string `json:"id"`
	Outcome  string `json:"outcome"`
	Status   string `json:"status"`
	Due      string `json:"due,omitempty"`
	Notes    string `json:"notes,omitempty"`
	Progress []Note `json:"progress,omitempty"`
	Created  string `json:"created"`
	Updated  string `json:"updated"`
}

// Note is one timestamped progress entry appended by the `note` action.
type Note struct {
	Time string `json:"time"`
	Text string `json:"text"`
}

// The four canonical statuses. update-status accepts exactly these; anything
// else is a typed error (no silent coercion).
const (
	StatusTodo    = "todo"
	StatusActive  = "active"
	StatusBlocked = "blocked"
	StatusDone    = "done"
)

// defaultStore is where MissionControl will read the durable goal list from -
// alongside data\kanban.json, its existing Board file. Hardcoding the machine
// path is consistent with the rest of becky-tools (see knownPathBin =
// C:\Users\only1\bin in cmd/becky/list.go); Whoretana calls `becky-goal add
// "..."` with no path, which is the whole point of "one dumb call".
const defaultStore = `X:\AI-2\hj-mission-control\data\goals.json`

// nowFunc is the clock, indirected so tests are deterministic (becky invariant:
// same input -> same output). Production returns UTC RFC3339.
var nowFunc = func() string { return time.Now().UTC().Format(time.RFC3339) }

// Result is becky-goal's stdout JSON envelope. Goal is set for single-goal
// actions (add/update-status/note); Goals+Count for list. OK is false with
// Error set on any failure.
type Result struct {
	OK      bool   `json:"ok"`
	Action  string `json:"action,omitempty"`
	Store   string `json:"store,omitempty"`
	Goal    *Goal  `json:"goal,omitempty"`
	Goals   []Goal `json:"goals,omitempty"`
	Count   int    `json:"count,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// options carries the parsed flags into run().
type options struct {
	outcome string
	due     string
	notes   string
	id      string
	status  string
	text    string
	store   string
}

// storePath resolves where the store lives: an explicit --store, else the
// BECKY_GOALS_PATH env override (used by the selftest against a temp dir), else
// the documented MissionControl location.
func storePath(override string) string {
	if s := strings.TrimSpace(override); s != "" {
		return s
	}
	if env := strings.TrimSpace(os.Getenv("BECKY_GOALS_PATH")); env != "" {
		return env
	}
	return defaultStore
}

// load reads the store. A missing or empty file is a fresh (empty) store, not
// an error - degrade, never crash. An unparseable file is a HARD error and the
// file is left untouched (Law 8b).
func load(path string) ([]Goal, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []Goal{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read goals store %s: %v", pathx.Base(path), err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return []Goal{}, nil
	}
	var goals []Goal
	if err := json.Unmarshal(raw, &goals); err != nil {
		return nil, fmt.Errorf("goals store %s is not valid JSON; refusing to touch it: %v", pathx.Base(path), err)
	}
	return goals, nil
}

// save writes the store atomically: marshal, write a temp file in the SAME
// directory, then rename over the target. Same-dir temp guarantees the rename
// is a same-volume atomic replace, so a reader (MissionControl) never sees a
// half-written file. The parent dir is created if missing (additive).
func save(path string, goals []Goal) error {
	if goals == nil {
		goals = []Goal{}
	}
	raw, err := json.MarshalIndent(goals, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cannot create store directory: %v", err)
	}
	tmp, err := os.CreateTemp(dir, ".goals-*.tmp")
	if err != nil {
		return fmt.Errorf("cannot create temp store file: %v", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("cannot write temp store file: %v", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("cannot finalize temp store file: %v", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("cannot replace store file: %v", err)
	}
	return nil
}

// nextID returns the next "gN" id: max existing numeric suffix + 1. Deterministic
// given the store, collision-free even though goals are never deleted or
// reordered.
func nextID(goals []Goal) string {
	max := 0
	for _, g := range goals {
		if strings.HasPrefix(g.ID, "g") {
			if n, err := strconv.Atoi(g.ID[1:]); err == nil && n > max {
				max = n
			}
		}
	}
	return fmt.Sprintf("g%d", max+1)
}

func validStatus(s string) bool {
	switch s {
	case StatusTodo, StatusActive, StatusBlocked, StatusDone:
		return true
	}
	return false
}

func indexOf(goals []Goal, id string) int {
	for i := range goals {
		if goals[i].ID == id {
			return i
		}
	}
	return -1
}

func failResult(action string, err error) Result {
	return Result{OK: false, Action: action, Error: err.Error()}
}

// --- actions ---------------------------------------------------------------

func doAdd(path, outcome, due, notes string) Result {
	outcome = strings.TrimSpace(outcome)
	if outcome == "" {
		return failResult("add", fmt.Errorf("no outcome text given (what do you want to happen?)"))
	}
	goals, err := load(path)
	if err != nil {
		return failResult("add", err)
	}
	now := nowFunc()
	g := Goal{
		ID:      nextID(goals),
		Outcome: outcome,
		Status:  StatusTodo,
		Due:     strings.TrimSpace(due),
		Notes:   strings.TrimSpace(notes),
		Created: now,
		Updated: now,
	}
	goals = append(goals, g)
	if err := save(path, goals); err != nil {
		return failResult("add", err)
	}
	return Result{OK: true, Action: "add", Store: path, Goal: &g, Count: len(goals),
		Message: fmt.Sprintf("added goal %s: %s", g.ID, g.Outcome)}
}

func doList(path, statusFilter string) Result {
	goals, err := load(path)
	if err != nil {
		return failResult("list", err)
	}
	statusFilter = strings.ToLower(strings.TrimSpace(statusFilter))
	if statusFilter != "" && !validStatus(statusFilter) {
		return failResult("list", fmt.Errorf("unknown status filter %q (want todo|active|blocked|done)", statusFilter))
	}
	out := make([]Goal, 0, len(goals))
	for _, g := range goals {
		if statusFilter == "" || g.Status == statusFilter {
			out = append(out, g)
		}
	}
	msg := fmt.Sprintf("%d goal(s)", len(out))
	if statusFilter != "" {
		msg = fmt.Sprintf("%d goal(s) with status %s", len(out), statusFilter)
	}
	return Result{OK: true, Action: "list", Store: path, Goals: out, Count: len(out), Message: msg}
}

func doUpdateStatus(path, id, status string) Result {
	id = strings.TrimSpace(id)
	status = strings.ToLower(strings.TrimSpace(status))
	if id == "" {
		return failResult("update-status", fmt.Errorf("no goal id given (pass --id or the id as the first argument)"))
	}
	if !validStatus(status) {
		return failResult("update-status", fmt.Errorf("invalid status %q (want todo|active|blocked|done)", status))
	}
	goals, err := load(path)
	if err != nil {
		return failResult("update-status", err)
	}
	idx := indexOf(goals, id)
	if idx < 0 {
		return failResult("update-status", fmt.Errorf("no goal with id %q", id))
	}
	goals[idx].Status = status
	goals[idx].Updated = nowFunc()
	if err := save(path, goals); err != nil {
		return failResult("update-status", err)
	}
	g := goals[idx]
	return Result{OK: true, Action: "update-status", Store: path, Goal: &g, Count: len(goals),
		Message: fmt.Sprintf("goal %s -> %s", g.ID, g.Status)}
}

func doNote(path, id, text string) Result {
	id = strings.TrimSpace(id)
	text = strings.TrimSpace(text)
	if id == "" {
		return failResult("note", fmt.Errorf("no goal id given (pass --id or the id as the first argument)"))
	}
	if text == "" {
		return failResult("note", fmt.Errorf("no note text given (what progress do you want to record?)"))
	}
	goals, err := load(path)
	if err != nil {
		return failResult("note", err)
	}
	idx := indexOf(goals, id)
	if idx < 0 {
		return failResult("note", fmt.Errorf("no goal with id %q", id))
	}
	now := nowFunc()
	goals[idx].Progress = append(goals[idx].Progress, Note{Time: now, Text: text})
	goals[idx].Updated = now
	if err := save(path, goals); err != nil {
		return failResult("note", err)
	}
	g := goals[idx]
	return Result{OK: true, Action: "note", Store: path, Goal: &g, Count: len(goals),
		Message: fmt.Sprintf("noted progress on goal %s", g.ID)}
}

// run dispatches one action. rest holds the bare (non-flag) tokens after the
// action, so a caller can say either `note g1 "text"` or `note --id g1 --text
// "text"` - flags win, positional fills the gaps. It is the single entry point
// main() calls and the one unit tests exercise directly.
func run(action string, rest []string, opt options) Result {
	path := storePath(opt.store)
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "add":
		outcome := opt.outcome
		if outcome == "" {
			outcome = strings.Join(rest, " ")
		}
		return doAdd(path, outcome, opt.due, opt.notes)
	case "list", "ls":
		filter := opt.status
		if filter == "" && len(rest) > 0 {
			filter = rest[0]
		}
		return doList(path, filter)
	case "update-status", "set-status", "status":
		id, status, r := opt.id, opt.status, rest
		if id == "" && len(r) > 0 {
			id, r = r[0], r[1:]
		}
		if status == "" && len(r) > 0 {
			status = r[0]
		}
		return doUpdateStatus(path, id, status)
	case "note":
		id, text, r := opt.id, opt.text, rest
		if text == "" {
			text = opt.notes
		}
		if id == "" && len(r) > 0 {
			id, r = r[0], r[1:]
		}
		if text == "" {
			text = strings.Join(r, " ")
		}
		return doNote(path, id, text)
	default:
		return Result{OK: false, Error: fmt.Sprintf("unknown action: %q (want add|list|update-status|note)", action)}
	}
}
