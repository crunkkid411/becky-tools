// kanban.go - the pure, testable core of becky-kanban: the board-edit tool that
// adds / moves / notes / lists cards in MissionControl's data\kanban.json. main.go
// is only flag parsing + wiring, so everything with a decision in it lives here
// and can be unit-tested with no real args.
//
// WHY THIS TOOL EXISTS (WHORETANA/docs/DEBRIEF-MODE.md, phase 1): debrief mode
// lets Jordan talk to Whoretana ABOUT his Show Me dashboards and edit his task
// board by voice ("add a task to fix the orb throttling", "move that to done").
// Editing his OWN board is an internal workspace action (AUTOPILOT Law 19 SAFE -
// it is NOT posting/sending as him externally). This is the clean board API that
// debrief calls; it is also independently useful - agents get a real board API
// instead of hand-editing JSON.
//
// Safety posture (AUTOPILOT.md Law 8b - "DELETE NOTHING OF JORDAN'S. EVER."):
// the board is ADDITIVE. There is NO delete action; the `delete` verb is an
// explicit typed refusal and a unit test asserts no code path removes a card.
// add appends, move only changes a card's col, note only appends to a card's
// text. A store file that is present but unparseable is REFUSED (typed error,
// exit 1) and left byte-for-byte untouched - never overwritten, because a
// corrupt-looking file may still be recoverable and blowing it away is exactly
// the data loss Law 8b forbids. Writes are atomic (temp file + rename on the
// same volume) so MissionControl - which reads and hot-reloads this file live -
// never sees a half-written board. Unknown card fields are preserved verbatim
// (see Card), so a field the GUI adds tomorrow is never silently dropped.
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

// Card is one board card. It is stored as a raw key->value map, NOT a fixed
// struct, so ANY field MissionControl or a future GUI adds (an id, an order, a
// color) survives a round-trip untouched - dropping a field Jordan's board
// carries would be exactly the data loss AUTOPILOT Law 8b forbids. The known
// fields - agent, col, text, title, details, rev, id - are read/written through
// typed accessors; everything else is carried verbatim. encoding/json emits map
// keys in sorted order, which for {agent,col,text} is the exact order the
// existing kanban.json already uses.
type Card map[string]json.RawMessage

// Board is the whole store: a bare JSON array of cards, the shape MissionControl
// already reads.
type Board []Card

// MissionControl's kanban schema spans two generations. Legacy (BUILD_2 and
// earlier) cards carry a single `text` field. The rebuilt v2 Board (BUILD_3,
// 2026-07-16) uses structured fields: `title` + `details`, a UUID `id`, and a
// `rev` that every writer bumps on the card it changes (the multi-writer
// contract in docs/AUTOPILOT.md). This tool is v2-aware: it reads whatever text
// is available (text -> title -> first line of details) so `list` is never
// blank for a v2 card, and `move` bumps rev + updated so the GUI and other
// writers see the change immediately.
const (
	keyAgent   = "agent"
	keyCol     = "col"
	keyText    = "text"
	keyTitle   = "title"
	keyDetails = "details"
	keyRev     = "rev"
	keyID      = "id"
)

// defaultStore is MissionControl's live Board file. Hardcoding the machine path
// is consistent with the rest of becky-tools (see becky-goal's defaultStore);
// Whoretana calls `becky-kanban add "..."` with no path, which is the whole
// point of "one dumb call".
const defaultStore = `X:\AI-2\hj-mission-control\data\kanban.json`

// defaultAgent is the owner stamped on a new card when --agent is not given -
// every existing card on the Board is "claude".
const defaultAgent = "claude"

func (c Card) str(key string) string {
	raw, ok := c[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// Text returns the card's display text. v2-aware: legacy cards use `text`;
// v2 cards use `title` (with `details` holding the long body). Whatever is
// present is shown, so `list` is never blank for a v2 card. Falls back across
// text -> title -> first line of details.
func (c Card) Text() string {
	if t := c.str(keyText); t != "" {
		return t
	}
	if t := c.str(keyTitle); t != "" {
		return t
	}
	if d := c.str(keyDetails); d != "" {
		if nl := strings.IndexByte(d, '\n'); nl >= 0 {
			return d[:nl]
		}
		return d
	}
	return ""
}

// Title returns the v2 title field ("" if absent).
func (c Card) Title() string { return c.str(keyTitle) }

// ID returns the card's id field ("" if absent).
func (c Card) ID() string { return c.str(keyID) }

// Rev returns the card's rev (0 if absent or unreadable).
func (c Card) Rev() int {
	raw, ok := c[keyRev]
	if !ok {
		return 0
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0
	}
	return int(f)
}

// Agent returns the card's agent field ("" if absent).
func (c Card) Agent() string { return c.str(keyAgent) }

// Col returns the card's column as an int (0 if absent or unreadable). JSON
// numbers decode through float64 so a value written as 2 or 2.0 both read as 2.
func (c Card) Col() int {
	raw, ok := c[keyCol]
	if !ok {
		return 0
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0
	}
	return int(f)
}

func (c Card) setStr(key, val string) {
	// json.Marshal of a string never errors (invalid UTF-8 is sanitised, not
	// rejected), so the error is intentionally ignored.
	b, _ := json.Marshal(val)
	c[key] = b
}

func (c Card) setInt(key string, n int) {
	b, _ := json.Marshal(n)
	c[key] = b
}

// bumpRev increments the card's rev integer by 1 (starting at 0 if absent) and
// stamps `updated` with a fresh UTC timestamp. Every writer that mutates a card
// MUST call this so the GUI's merge-before-save protocol (docs/AUTOPILOT.md) and
// other writers see the change immediately instead of a 1-second stale-save
// window.
func (c Card) bumpRev() {
	c.setInt(keyRev, c.Rev()+1)
	c.setStr("updated", nowUTC())
}

// CardView is the DISPLAY shape for the JSON envelope + human output: the known
// fields plus the card's index in the array (the handle callers use for
// move/note) and its v2 id/rev when present. Extra on-disk fields are
// intentionally not shown here - the envelope is informational; the on-disk
// store is what preserves them.
type CardView struct {
	Index int    `json:"index"`
	Agent string `json:"agent"`
	Col   int    `json:"col"`
	Text  string `json:"text"`
	ID    string `json:"id,omitempty"`
	Rev   int    `json:"rev,omitempty"`
}

func view(i int, c Card) CardView {
	return CardView{Index: i, Agent: c.Agent(), Col: c.Col(), Text: c.Text(), ID: c.ID(), Rev: c.Rev()}
}

// nowUTC returns an RFC3339 UTC timestamp, matching the format MissionControl's
// GUI writes (NowIso8601Utc) so v2 cards stay consistent across writers.
func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

// Result is becky-kanban's stdout JSON envelope. Card is set for single-card
// actions (add/move/note); Cards+Count for list. OK is false with Error set on
// any failure.
type Result struct {
	OK      bool       `json:"ok"`
	Action  string     `json:"action,omitempty"`
	Store   string     `json:"store,omitempty"`
	Card    *CardView  `json:"card,omitempty"`
	Cards   []CardView `json:"cards,omitempty"`
	Count   int        `json:"count,omitempty"`
	Message string     `json:"message,omitempty"`
	Error   string     `json:"error,omitempty"`
}

// options carries the parsed flags into run().
type options struct {
	text  string
	col   string // raw flag value; "" means unset, so add can default it to 0
	agent string
	store string
}

// storePath resolves where the board lives: an explicit --store, else the
// BECKY_KANBAN_PATH env override (used by the selftest + tests against a temp
// dir), else the documented MissionControl location.
func storePath(override string) string {
	if s := strings.TrimSpace(override); s != "" {
		return s
	}
	if env := strings.TrimSpace(os.Getenv("BECKY_KANBAN_PATH")); env != "" {
		return env
	}
	return defaultStore
}

// load reads the board. A missing or empty file is a fresh (empty) board, not an
// error - degrade, never crash. An unparseable file is a HARD error and the file
// is left untouched (Law 8b).
func load(path string) (Board, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Board{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read board %s: %v", pathx.Base(path), err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return Board{}, nil
	}
	var board Board
	if err := json.Unmarshal(raw, &board); err != nil {
		return nil, fmt.Errorf("board %s is not valid JSON; refusing to touch it: %v", pathx.Base(path), err)
	}
	return board, nil
}

// save writes the board atomically: marshal, write a temp file in the SAME
// directory, then rename over the target. Same-dir temp guarantees the rename is
// a same-volume atomic replace, so a reader (MissionControl) never sees a
// half-written file. The parent dir is created if missing (additive).
func save(path string, board Board) error {
	if board == nil {
		board = Board{}
	}
	raw, err := json.MarshalIndent(board, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cannot create store directory: %v", err)
	}
	tmp, err := os.CreateTemp(dir, ".kanban-*.tmp")
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

// parseCol turns a raw column flag/arg into a validated non-negative int.
// ponytail: no upper bound - MissionControl currently renders cols 0,1,2, but a
// card at a higher col is PRESERVED (never deleted), just off-screen; capping it
// here would couple this tool to MC's column count. The voice layer (phase 2)
// maps words like "done" -> 2.
func parseCol(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("column must be a whole number, got %q", s)
	}
	if n < 0 {
		return 0, fmt.Errorf("column must be >= 0, got %d", n)
	}
	return n, nil
}

// resolveSelector turns an index-or-match token into a card index. A pure
// integer is a 0-based array index; anything else is a case-insensitive
// substring match against card text that MUST hit exactly one card - zero or
// many is a typed error, never a guess (safe: an ambiguous "move" could strand
// the wrong card in the wrong column).
func resolveSelector(board Board, sel string) (int, error) {
	sel = strings.TrimSpace(sel)
	if sel == "" {
		return -1, errors.New("no card given (pass an index or a text match)")
	}
	if len(board) == 0 {
		return -1, errors.New("the board is empty")
	}
	if n, err := strconv.Atoi(sel); err == nil {
		if n < 0 || n >= len(board) {
			return -1, fmt.Errorf("card index %d out of range (board has %d card(s), valid 0..%d)", n, len(board), len(board)-1)
		}
		return n, nil
	}
	needle := strings.ToLower(sel)
	var matches []int
	for i, c := range board {
		if strings.Contains(strings.ToLower(c.Text()), needle) {
			matches = append(matches, i)
		}
	}
	switch len(matches) {
	case 0:
		return -1, fmt.Errorf("no card matches %q", sel)
	case 1:
		return matches[0], nil
	default:
		return -1, fmt.Errorf("%d cards match %q; be more specific or use the index", len(matches), sel)
	}
}

func failResult(action string, err error) Result {
	return Result{OK: false, Action: action, Error: err.Error()}
}

// --- actions ---------------------------------------------------------------

func doAdd(path, text, colStr, agent string) Result {
	text = strings.TrimSpace(text)
	if text == "" {
		return failResult("add", errors.New("no card text given (what should the card say?)"))
	}
	col := 0
	if strings.TrimSpace(colStr) != "" {
		n, err := parseCol(colStr)
		if err != nil {
			return failResult("add", err)
		}
		col = n
	}
	agent = strings.TrimSpace(agent)
	if agent == "" {
		agent = defaultAgent
	}
	board, err := load(path)
	if err != nil {
		return failResult("add", err)
	}
	c := Card{}
	c.setStr(keyAgent, agent)
	c.setInt(keyCol, col)
	c.setStr(keyText, text)
	board = append(board, c)
	if err := save(path, board); err != nil {
		return failResult("add", err)
	}
	v := view(len(board)-1, c)
	return Result{OK: true, Action: "add", Store: path, Card: &v, Count: len(board),
		Message: fmt.Sprintf("added card %d in col %d: %s", v.Index, v.Col, v.Text)}
}

func doList(path, colStr string) Result {
	board, err := load(path)
	if err != nil {
		return failResult("list", err)
	}
	filter := -1
	if strings.TrimSpace(colStr) != "" {
		n, err := parseCol(colStr)
		if err != nil {
			return failResult("list", err)
		}
		filter = n
	}
	out := make([]CardView, 0, len(board))
	for i, c := range board {
		if filter < 0 || c.Col() == filter {
			out = append(out, view(i, c))
		}
	}
	msg := fmt.Sprintf("%d card(s)", len(out))
	if filter >= 0 {
		msg = fmt.Sprintf("%d card(s) in col %d", len(out), filter)
	}
	return Result{OK: true, Action: "list", Store: path, Cards: out, Count: len(out), Message: msg}
}

func doMove(path, sel, colStr string) Result {
	if strings.TrimSpace(colStr) == "" {
		return failResult("move", errors.New("no destination column given (move <index-or-match> <col>)"))
	}
	col, err := parseCol(colStr)
	if err != nil {
		return failResult("move", err)
	}
	board, err := load(path)
	if err != nil {
		return failResult("move", err)
	}
	idx, err := resolveSelector(board, sel)
	if err != nil {
		return failResult("move", err)
	}
	board[idx].setInt(keyCol, col)
	board[idx].bumpRev() // v2-aware: rev++ + updated so GUI/other writers see it now
	if err := save(path, board); err != nil {
		return failResult("move", err)
	}
	v := view(idx, board[idx])
	return Result{OK: true, Action: "move", Store: path, Card: &v, Count: len(board),
		Message: fmt.Sprintf("moved card %d to col %d", idx, col)}
}

func doNote(path, sel, text string) Result {
	text = strings.TrimSpace(text)
	if text == "" {
		return failResult("note", errors.New("no note text given (what should be appended to the card?)"))
	}
	board, err := load(path)
	if err != nil {
		return failResult("note", err)
	}
	idx, err := resolveSelector(board, sel)
	if err != nil {
		return failResult("note", err)
	}
	cur := board[idx].Text()
	if cur == "" {
		cur = text
	} else {
		cur = cur + " " + text
	}
	board[idx].setStr(keyText, cur)
	board[idx].bumpRev()
	if err := save(path, board); err != nil {
		return failResult("note", err)
	}
	v := view(idx, board[idx])
	return Result{OK: true, Action: "note", Store: path, Card: &v, Count: len(board),
		Message: fmt.Sprintf("appended note to card %d", idx)}
}

// run dispatches one action. rest holds the bare (non-flag) tokens after the
// action, so a caller can say either `move 3 2` / `note 3 "text"` or use flags.
// It is the single entry point main() calls and the one unit tests exercise
// directly.
func run(action string, rest []string, opt options) Result {
	path := storePath(opt.store)
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "add":
		text := opt.text
		if text == "" {
			text = strings.Join(rest, " ")
		}
		return doAdd(path, text, opt.col, opt.agent)
	case "list", "ls":
		return doList(path, opt.col)
	case "move", "mv":
		sel, col, r := "", opt.col, rest
		if len(r) > 0 {
			sel, r = r[0], r[1:]
		}
		if col == "" && len(r) > 0 {
			col = r[0]
		}
		return doMove(path, sel, col)
	case "note":
		sel, text, r := "", opt.text, rest
		if len(r) > 0 {
			sel, r = r[0], r[1:]
		}
		if text == "" {
			text = strings.Join(r, " ")
		}
		return doNote(path, sel, text)
	case "delete", "remove", "rm", "del":
		// AUTOPILOT Law 8b: DELETE NOTHING OF JORDAN'S. There is deliberately no
		// delete path anywhere in this tool; this verb is an explicit refusal and
		// TestNoDeleteEverRemovesACard asserts it stays unreachable.
		return Result{OK: false, Action: action,
			Error: "delete is not supported: this tool never removes a card (AUTOPILOT Law 8b). Move it to another column instead."}
	default:
		return Result{OK: false, Error: fmt.Sprintf("unknown action: %q (want add|list|move|note)", action)}
	}
}
