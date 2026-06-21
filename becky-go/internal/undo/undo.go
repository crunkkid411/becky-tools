// Package undo is a deterministic undo/redo history for an immutable
// dawmodel.Arrangement. Because every edit verb returns a NEW arrangement (the
// applyArr-is-the-only-commit-path rule), the history just holds the sequence of
// committed snapshots by pointer — no deep copy needed — with a cursor.
//
// This is core "basic functionality" (FEATURE-INVENTORY.md) and the dual human+agent
// operability rule (STANDARDS-CANVAS-UX.md §3): a ctledit batch is as undoable as a
// click because both commit through the same path that Pushes here.
package undo

import "becky-go/internal/dawmodel"

// DefaultDepth bounds how many states we keep (oldest dropped beyond this).
const DefaultDepth = 200

// History is an undo/redo stack over arrangement snapshots.
type History struct {
	stack []*dawmodel.Arrangement
	cur   int // index of the current state in stack (-1 when empty)
	max   int
}

// New returns an empty history bounded to max states (<=0 uses DefaultDepth).
func New(max int) *History {
	if max <= 0 {
		max = DefaultDepth
	}
	return &History{cur: -1, max: max}
}

// Push commits a new current state. Any redo tail (states after the cursor) is
// discarded — the standard editor behavior when you edit after undoing. A nil push
// is ignored. Pushing the exact same pointer twice is a no-op (so re-committing an
// unchanged arrangement doesn't bloat history).
func (h *History) Push(a *dawmodel.Arrangement) {
	if a == nil {
		return
	}
	if h.cur >= 0 && h.stack[h.cur] == a {
		return
	}
	h.stack = append(h.stack[:h.cur+1], a)
	h.cur = len(h.stack) - 1
	if len(h.stack) > h.max {
		drop := len(h.stack) - h.max
		h.stack = append([]*dawmodel.Arrangement(nil), h.stack[drop:]...)
		h.cur -= drop
	}
}

// Undo moves the cursor back one state and returns it. ok=false when there is
// nothing earlier to go to (the current state is unchanged).
func (h *History) Undo() (*dawmodel.Arrangement, bool) {
	if h.cur <= 0 {
		return h.Current(), false
	}
	h.cur--
	return h.stack[h.cur], true
}

// Redo moves the cursor forward one state and returns it. ok=false when there is
// nothing newer.
func (h *History) Redo() (*dawmodel.Arrangement, bool) {
	if h.cur < 0 || h.cur >= len(h.stack)-1 {
		return h.Current(), false
	}
	h.cur++
	return h.stack[h.cur], true
}

// Current returns the state at the cursor (nil when empty).
func (h *History) Current() *dawmodel.Arrangement {
	if h.cur < 0 || h.cur >= len(h.stack) {
		return nil
	}
	return h.stack[h.cur]
}

// CanUndo / CanRedo report whether the respective action is available (for graying
// out the buttons).
func (h *History) CanUndo() bool { return h.cur > 0 }
func (h *History) CanRedo() bool { return h.cur >= 0 && h.cur < len(h.stack)-1 }

// Len returns the number of states currently retained.
func (h *History) Len() int { return len(h.stack) }
