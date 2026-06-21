package undo

import (
	"testing"

	"becky-go/internal/dawmodel"
)

func arr(bpm int) *dawmodel.Arrangement {
	a := dawmodel.New()
	a.BPM = bpm
	return a
}

func TestPushUndoRedo(t *testing.T) {
	h := New(0)
	a1, a2, a3 := arr(100), arr(110), arr(120)
	h.Push(a1)
	h.Push(a2)
	h.Push(a3)
	if h.Current().BPM != 120 {
		t.Fatalf("current should be a3 (120), got %d", h.Current().BPM)
	}
	if got, ok := h.Undo(); !ok || got.BPM != 110 {
		t.Errorf("undo → a2 (110), got %d ok=%v", got.BPM, ok)
	}
	if got, ok := h.Undo(); !ok || got.BPM != 100 {
		t.Errorf("undo → a1 (100), got %d ok=%v", got.BPM, ok)
	}
	if _, ok := h.Undo(); ok {
		t.Error("undo past the start should fail")
	}
	if got, ok := h.Redo(); !ok || got.BPM != 110 {
		t.Errorf("redo → a2 (110), got %d ok=%v", got.BPM, ok)
	}
}

func TestEditAfterUndoDiscardsRedoTail(t *testing.T) {
	h := New(0)
	h.Push(arr(100))
	h.Push(arr(110))
	h.Push(arr(120))
	h.Undo() // now at 110
	h.Undo() // now at 100
	h.Push(arr(200))
	if h.CanRedo() {
		t.Error("editing after undo must discard the redo tail")
	}
	if h.Current().BPM != 200 {
		t.Errorf("current should be the new edit (200), got %d", h.Current().BPM)
	}
}

func TestCanUndoRedoFlags(t *testing.T) {
	h := New(0)
	if h.CanUndo() || h.CanRedo() {
		t.Error("empty history can neither undo nor redo")
	}
	h.Push(arr(100))
	if h.CanUndo() {
		t.Error("a single state cannot be undone")
	}
	h.Push(arr(110))
	if !h.CanUndo() || h.CanRedo() {
		t.Error("after two pushes: can undo, cannot redo")
	}
	h.Undo()
	if !h.CanRedo() {
		t.Error("after an undo, can redo")
	}
}

func TestBoundedDepth(t *testing.T) {
	h := New(3)
	for i := 0; i < 10; i++ {
		h.Push(arr(i))
	}
	if h.Len() != 3 {
		t.Fatalf("history should be bounded to 3, got %d", h.Len())
	}
	if h.Current().BPM != 9 {
		t.Errorf("current should be the last push (9), got %d", h.Current().BPM)
	}
	// Only the last 3 (7,8,9) survive: 9 → undo → 8 → undo → 7 → undo fails.
	h.Undo()
	h.Undo()
	if got := h.Current().BPM; got != 7 {
		t.Errorf("oldest retained should be 7, got %d", got)
	}
	if _, ok := h.Undo(); ok {
		t.Error("cannot undo past the bounded window")
	}
}

func TestPushIgnoresNilAndDuplicatePointer(t *testing.T) {
	h := New(0)
	a := arr(100)
	h.Push(a)
	h.Push(a) // same pointer → no-op
	h.Push(nil)
	if h.Len() != 1 {
		t.Errorf("nil and duplicate-pointer pushes should be ignored, len=%d", h.Len())
	}
}
