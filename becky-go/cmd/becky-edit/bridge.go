package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"becky-go/internal/config"
	"becky-go/internal/ctlagent"
	"becky-go/internal/editmodel"
	"becky-go/internal/edittools"
	"becky-go/internal/edl"
	"becky-go/internal/footage"
	"becky-go/internal/seam"
)

// Bridge is the becky-edit engine the forked Shotcut dock talks to over NDJSON
// (internal/seam wire shape). It owns THE shared live state (editmodel.Project)
// and keeps it synced from BOTH directions through the SAME validated edittools:
//   - the model edits via "agent" (propose) -> "approve" (commit), and the dock's
//     buttons via "do";
//   - the human's own edits inside Shotcut are mirrored in via "event".
//
// This is Jordan's "share state regardless of who edits" requirement.
type Bridge struct {
	cfg       config.Config
	logf      func(string, ...any)
	state     *editmodel.Project
	index     *footage.FolderIndex
	model     ctlagent.Model // nil => agent degrades
	modelNote string         // why the agent is unavailable, if so
	pending   map[string]ctlagent.Result
	seq       int
}

// NewBridge builds a bridge with an empty project. model may be nil (no agent).
func NewBridge(cfg config.Config, model ctlagent.Model, modelNote string, logf func(string, ...any)) *Bridge {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Bridge{
		cfg:       cfg,
		logf:      logf,
		state:     editmodel.New("untitled", edl.DefaultFPS),
		model:     model,
		modelNote: modelNote,
		pending:   map[string]ctlagent.Result{},
	}
}

// Dispatch handles one request and returns the response (same id). It never
// panics: an unknown verb or a handler error becomes an ok=false response.
func (b *Bridge) Dispatch(ctx context.Context, req seam.CommandMsg) seam.ResponseMsg {
	data, err := b.handle(ctx, req.Name, req.Args)
	if err != nil {
		return seam.ResponseMsg{Type: seam.TypeResponse, ID: req.ID, OK: false, Error: err.Error()}
	}
	raw, mErr := json.Marshal(data)
	if mErr != nil {
		return seam.ResponseMsg{Type: seam.TypeResponse, ID: req.ID, OK: false, Error: mErr.Error()}
	}
	return seam.ResponseMsg{Type: seam.TypeResponse, ID: req.ID, OK: true, Data: raw}
}

func (b *Bridge) handle(ctx context.Context, name string, args json.RawMessage) (any, error) {
	switch name {
	case "ping":
		return map[string]any{"pong": true}, nil
	case "open_folder":
		return b.openFolder(args)
	case "state":
		return b.stateView(), nil
	case "do":
		return b.doVerb(args)
	case "event":
		return b.applyEvent(args)
	case "search":
		return b.search(args)
	case "agent":
		return b.runAgent(ctx, args)
	case "approve":
		return b.approve(args)
	case "reject":
		return b.reject(args)
	default:
		return nil, fmt.Errorf("unknown command %q", name)
	}
}

// stateView is the full project + its compact digest (what the model sees).
func (b *Bridge) stateView() map[string]any {
	return map[string]any{"project": b.state, "digest": b.state.Digest()}
}

func (b *Bridge) openFolder(args json.RawMessage) (any, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Path == "" {
		return nil, fmt.Errorf("open_folder needs a path")
	}
	idx, err := footage.Index(a.Path)
	if err != nil {
		return nil, fmt.Errorf("index folder: %w", err)
	}
	b.index = &idx
	b.state.Folder = idx.Root
	with := 0
	for _, v := range idx.Videos {
		if v.HasTranscript {
			with++
		}
	}
	b.logf("becky-edit: opened %s (%d videos, %d with transcripts)", idx.Root, len(idx.Videos), with)
	return map[string]any{
		"root": idx.Root, "videos": len(idx.Videos),
		"with_transcripts": with, "orphans": len(idx.Orphans),
	}, nil
}

// doVerb applies one tool directly (a dock button or a relayed action) and COMMITS
// it. The returned host commands are what the dock executes against Shotcut.
func (b *Bridge) doVerb(args json.RawMessage) (any, error) {
	var a struct {
		Verb string         `json:"verb"`
		Args map[string]any `json:"args"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Verb == "" {
		return nil, fmt.Errorf("do needs a verb")
	}
	newState, host, res := edittools.Apply(b.state, edittools.ToolCall{Verb: edittools.Verb(a.Verb), Args: a.Args})
	if res.OK {
		b.state = newState
	}
	return map[string]any{"result": res, "host": host, "rev": b.state.Rev, "digest": b.state.Digest()}, nil
}

// applyEvent mirrors a HUMAN edit performed inside Shotcut into the shared state.
// It routes through the SAME edittools as the model, but DISCARDS the host
// commands (the host already did the edit — we must not echo it back). This is
// what keeps the model's view correct when Jordan edits by hand.
func (b *Bridge) applyEvent(args json.RawMessage) (any, error) {
	var a struct {
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Name == "" {
		return nil, fmt.Errorf("event needs a name")
	}
	verb, vargs, ok := translateEvent(a.Name, a.Args)
	if !ok {
		return nil, fmt.Errorf("unknown event %q", a.Name)
	}
	newState, _, res := edittools.Apply(b.state, edittools.ToolCall{Verb: verb, Args: vargs})
	if !res.OK {
		return nil, fmt.Errorf("event %s rejected: %s", a.Name, res.Message)
	}
	b.state = newState
	return map[string]any{"rev": b.state.Rev, "digest": b.state.Digest()}, nil
}

// search runs the deterministic transcript grep over the open folder.
func (b *Bridge) search(args json.RawMessage) (any, error) {
	var a struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Query == "" {
		return nil, fmt.Errorf("search needs a query")
	}
	if b.index == nil {
		return nil, fmt.Errorf("no folder is open")
	}
	hits := footage.GrepTranscripts(*b.index, strings.Fields(a.Query))
	return map[string]any{"hits": hits, "count": len(hits)}, nil
}

// runAgent runs the multi-step editing loop with the warm Gemma model and stores
// the proposal under an id. It does NOT commit — the dock previews it, then calls
// "approve" or "reject" (propose-preview-apply; the forensic gate).
func (b *Bridge) runAgent(ctx context.Context, args json.RawMessage) (any, error) {
	var a struct {
		Goal string `json:"goal"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Goal == "" {
		return nil, fmt.Errorf("agent needs a goal")
	}
	if b.model == nil {
		return map[string]any{"done": false, "message": b.modelNote, "applied": []any{}}, nil
	}
	run, err := ctlagent.Run(ctx, b.model, b.state, a.Goal, ctlagent.Options{Enrich: b.enrich, Log: b.logf})
	if err != nil {
		return nil, fmt.Errorf("agent loop: %w", err)
	}
	id := b.newPendingID()
	b.pending[id] = run
	return map[string]any{
		"proposal_id": id,
		"steps":       run.Steps,
		"applied":     run.Applied,
		"host":        run.Host,
		"message":     run.Message,
		"done":        run.Done,
		"aborted":     run.Aborted,
		"digest":      run.Final.Digest(),
	}, nil
}

// approve commits a pending agent proposal: the proposed final state becomes the
// live state and the accumulated host commands are returned for the dock to run.
func (b *Bridge) approve(args json.RawMessage) (any, error) {
	id, err := pendingID(args)
	if err != nil {
		return nil, err
	}
	run, ok := b.pending[id]
	if !ok {
		return nil, fmt.Errorf("no pending proposal %q", id)
	}
	b.state = run.Final
	delete(b.pending, id)
	return map[string]any{"host": run.Host, "rev": b.state.Rev, "digest": b.state.Digest()}, nil
}

// reject discards a pending proposal (nothing is committed).
func (b *Bridge) reject(args json.RawMessage) (any, error) {
	id, err := pendingID(args)
	if err != nil {
		return nil, err
	}
	delete(b.pending, id)
	return map[string]any{"ok": true}, nil
}

func (b *Bridge) newPendingID() string {
	b.seq++
	return fmt.Sprintf("prop%d", b.seq)
}

func pendingID(args json.RawMessage) (string, error) {
	var a struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.ID == "" {
		return "", fmt.Errorf("need a proposal id")
	}
	return a.ID, nil
}

// translateEvent maps a host-side human edit into a (verb, args) pair the shared
// edittools understands. Returns ok=false for an unknown event name.
func translateEvent(name string, in map[string]any) (edittools.Verb, map[string]any, bool) {
	switch name {
	case "playhead":
		at := in["seconds"]
		if at == nil {
			at = in["at"]
		}
		return "set_playhead", map[string]any{"at": at}, true
	case "select":
		if ids, ok := in["ids"].([]any); ok && len(ids) > 0 {
			return "select_clip", map[string]any{"id": ids[0]}, true
		}
		return "select_clip", map[string]any{"clear": true}, true
	case "clip_added":
		return "add_clip", in, true
	case "clip_moved":
		return "move_clip", in, true
	case "clip_trimmed":
		return "trim_clip", in, true
	case "clip_removed":
		return "remove_clip", in, true
	default:
		return "", nil, false
	}
}

// --- shared small helpers (package-wide) ------------------------------------

func baseName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "..."
}
