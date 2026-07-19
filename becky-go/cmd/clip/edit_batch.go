// edit_batch.go — the AI/dual-operability seam's core (BUILD_1.md H-4/H-6):
// apply_edit_batch applies a LIST of existing clip-mutating verbs as ONE
// atomic undo span, so Ctrl+Z reverses an entire AI editing pass (or a
// scripted multi-op agent call) in a single press, instead of walking
// backwards through one undo step per op. Each op is best-effort: a
// malformed op is skipped and reported, never a crash (H-2's per-verb rule
// applied to batch members) — but the batch as a whole is still ONE undo
// checkpoint, taken before the first op runs.
package main

import (
	"fmt"
	"strings"

	"becky-go/internal/edl"
)

// EditOp is one instruction inside an apply_edit_batch call — same verb name
// and args shape as the matching single-clip bridge verb (add_clip,
// remove_clip, reorder, set_trim, split, set_label).
type EditOp struct {
	Verb string
	Args map[string]any
}

// EditOpResult reports what happened to one op in a batch.
type EditOpResult struct {
	Verb  string `json:"verb"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	NewID string `json:"new_id,omitempty"` // set by add_clip / split
}

// ApplyEditBatch applies ops in order as ONE undoable edit. An empty batch is
// an error (nothing to make undoable) and never touches the undo stack. Each
// op's source (for add_clip) is resolved BEFORE the lock is taken, since
// resolveSource locks internally. Returns the resulting timeline and a
// per-op result list (so a caller can tell which ops actually landed).
func (a *App) ApplyEditBatch(ops []EditOp) (TimelineView, []EditOpResult, error) {
	if len(ops) == 0 {
		return a.Timeline(), nil, fmt.Errorf("empty edit batch")
	}

	// Pre-resolve every add_clip source outside the lock (resolveSource takes
	// a.mu itself; holding it here would deadlock).
	resolved := make(map[string]resolvedSource, len(ops))
	for _, op := range ops {
		if op.Verb != "add_clip" {
			continue
		}
		src := argString(op.Args, "source")
		if src == "" {
			continue
		}
		if v, ok := a.resolveSource(src); ok {
			resolved[src] = resolvedSource{Path: v.Path, Meta: edl.ClipMeta{
				Date:      v.Meta.Date,
				Link:      v.Meta.Link,
				Person:    v.Meta.Person,
				Location:  v.Meta.Location,
				SourceFPS: v.Meta.SourceFPS,
			}}
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.pushUndoLocked() // ONE snapshot covers every op below.

	results := make([]EditOpResult, 0, len(ops))
	for _, op := range ops {
		results = append(results, a.applyOneOpLocked(op, resolved))
	}
	return a.timelineLocked(), results, nil
}

// resolvedSource is one add_clip source's pre-locked lookup result (path +
// forensic meta pulled from the sidecar), computed before a.mu is taken.
type resolvedSource struct {
	Path string
	Meta edl.ClipMeta
}

// applyOneOpLocked mutates a.reel.Clips for one op. Caller holds a.mu and has
// already called pushUndoLocked() once for the whole batch — this must NOT
// push its own undo entry (that would defeat the "one atomic span" point).
func (a *App) applyOneOpLocked(op EditOp, resolved map[string]resolvedSource) EditOpResult {
	switch op.Verb {
	case "add_clip":
		source := argString(op.Args, "source")
		in := argFloat(op.Args, "in")
		out := argFloat(op.Args, "out")
		if out < in {
			in, out = out, in
		}
		label := argString(op.Args, "label")
		at := argIntDefault(op.Args, "at", -1)
		v, ok := resolved[source]
		if !ok {
			return EditOpResult{Verb: op.Verb, Error: "clip source is not in the open folder: " + source}
		}
		a.nextID++
		clip := edl.Clip{
			ID:     fmt.Sprintf("c%d", a.nextID),
			Source: v.Path,
			In:     clampNonNeg(in),
			Out:    clampNonNeg(out),
			Label:  strings.TrimSpace(label),
			Meta:   v.Meta,
		}
		if at < 0 || at >= len(a.reel.Clips) {
			a.reel.Clips = append(a.reel.Clips, clip)
		} else {
			next := make([]edl.Clip, 0, len(a.reel.Clips)+1)
			next = append(next, a.reel.Clips[:at]...)
			next = append(next, clip)
			next = append(next, a.reel.Clips[at:]...)
			a.reel.Clips = next
		}
		return EditOpResult{Verb: op.Verb, OK: true, NewID: clip.ID}

	case "remove_clip":
		id := argString(op.Args, "id")
		idx := clipIndexLocked(a.reel.Clips, id)
		if idx < 0 {
			return EditOpResult{Verb: op.Verb, Error: "no clip " + id}
		}
		a.reel.Clips = append(a.reel.Clips[:idx], a.reel.Clips[idx+1:]...)
		return EditOpResult{Verb: op.Verb, OK: true}

	case "reorder":
		id := argString(op.Args, "id")
		to := argInt(op.Args, "to")
		from := clipIndexLocked(a.reel.Clips, id)
		if from < 0 {
			return EditOpResult{Verb: op.Verb, Error: "no clip " + id}
		}
		if to < 0 {
			to = 0
		}
		if to >= len(a.reel.Clips) {
			to = len(a.reel.Clips) - 1
		}
		if to != from {
			moved := a.reel.Clips[from]
			rest := make([]edl.Clip, 0, len(a.reel.Clips)-1)
			rest = append(rest, a.reel.Clips[:from]...)
			rest = append(rest, a.reel.Clips[from+1:]...)
			out := make([]edl.Clip, 0, len(a.reel.Clips))
			out = append(out, rest[:to]...)
			out = append(out, moved)
			out = append(out, rest[to:]...)
			a.reel.Clips = out
		}
		return EditOpResult{Verb: op.Verb, OK: true}

	case "set_trim":
		id := argString(op.Args, "id")
		in := argFloat(op.Args, "in")
		out := argFloat(op.Args, "out")
		if out < in {
			in, out = out, in
		}
		idx := clipIndexLocked(a.reel.Clips, id)
		if idx < 0 {
			return EditOpResult{Verb: op.Verb, Error: "no clip " + id}
		}
		a.reel.Clips[idx].In = clampNonNeg(in)
		a.reel.Clips[idx].Out = clampNonNeg(out)
		return EditOpResult{Verb: op.Verb, OK: true}

	case "split":
		id := argString(op.Args, "id")
		atSource := argFloat(op.Args, "at")
		idx := clipIndexLocked(a.reel.Clips, id)
		if idx < 0 {
			return EditOpResult{Verb: op.Verb, Error: "no clip " + id}
		}
		c := a.reel.Clips[idx]
		const splitEdgeMargin = 0.1
		if atSource <= c.In+splitEdgeMargin || atSource >= c.Out-splitEdgeMargin {
			return EditOpResult{Verb: op.Verb, Error: "split point too close to a clip edge"}
		}
		a.nextID++
		right := edl.Clip{
			ID:     fmt.Sprintf("c%d", a.nextID),
			Source: c.Source,
			In:     atSource,
			Out:    c.Out,
			Label:  c.Label,
			Meta:   c.Meta,
		}
		a.reel.Clips[idx].Out = atSource
		out := make([]edl.Clip, 0, len(a.reel.Clips)+1)
		out = append(out, a.reel.Clips[:idx+1]...)
		out = append(out, right)
		out = append(out, a.reel.Clips[idx+1:]...)
		a.reel.Clips = out
		return EditOpResult{Verb: op.Verb, OK: true, NewID: right.ID}

	case "set_label":
		id := argString(op.Args, "id")
		idx := clipIndexLocked(a.reel.Clips, id)
		if idx < 0 {
			return EditOpResult{Verb: op.Verb, Error: "no clip " + id}
		}
		a.reel.Clips[idx].Label = strings.TrimSpace(argString(op.Args, "text"))
		return EditOpResult{Verb: op.Verb, OK: true}

	default:
		return EditOpResult{Verb: op.Verb, Error: "unknown batch op verb"}
	}
}

// clipIndexLocked finds a clip's index by id, or -1. Caller holds a.mu.
func clipIndexLocked(clips []edl.Clip, id string) int {
	if id == "" {
		return -1
	}
	for i, c := range clips {
		if c.ID == id {
			return i
		}
	}
	return -1
}
