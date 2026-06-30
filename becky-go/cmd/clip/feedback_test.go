package main

// feedback_test.go covers the engine half of the Becky Review feedback round:
// timeline undo/redo (Ctrl+Z / Ctrl+Shift+Z) and render-the-selected-clips.
// Pure App methods + the bridge dispatch — no window, no ffmpeg.

import "testing"

// TestUndoRedoAddRemove: an add is undoable back to empty and redoable forward;
// undo on empty history is a quiet no-op (changed=false), not an error.
func TestUndoRedoAddRemove(t *testing.T) {
	app, _ := openFixture(t)

	// Nothing to undo yet.
	if _, changed := app.Undo(); changed {
		t.Fatal("Undo with empty history must report changed=false")
	}

	if _, err := app.AddClip("ring.mp4", 1, 3, "a"); err != nil {
		t.Fatalf("AddClip: %v", err)
	}
	if _, err := app.AddClip("ring.mp4", 4, 6, "b"); err != nil {
		t.Fatalf("AddClip: %v", err)
	}
	if got := len(app.Timeline().Clips); got != 2 {
		t.Fatalf("want 2 clips after two adds, got %d", got)
	}

	// Undo the second add -> 1 clip; undo the first -> 0 clips.
	if tl, changed := app.Undo(); !changed || len(tl.Clips) != 1 {
		t.Fatalf("first undo: changed=%v clips=%d (want true,1)", changed, len(tl.Clips))
	}
	if tl, changed := app.Undo(); !changed || len(tl.Clips) != 0 {
		t.Fatalf("second undo: changed=%v clips=%d (want true,0)", changed, len(tl.Clips))
	}

	// Redo both -> back to 2 clips, with the labels in order.
	if tl, changed := app.Redo(); !changed || len(tl.Clips) != 1 {
		t.Fatalf("first redo: changed=%v clips=%d (want true,1)", changed, len(tl.Clips))
	}
	tl, changed := app.Redo()
	if !changed || len(tl.Clips) != 2 {
		t.Fatalf("second redo: changed=%v clips=%d (want true,2)", changed, len(tl.Clips))
	}
	if tl.Clips[0].Label != "a" || tl.Clips[1].Label != "b" {
		t.Fatalf("redo restored wrong order/labels: %q,%q", tl.Clips[0].Label, tl.Clips[1].Label)
	}
}

// TestUndoTrimRestoresWindow: a trim is undoable to the original in/out — i.e. undo
// asserts VALUES, not just the clip count.
func TestUndoTrimRestoresWindow(t *testing.T) {
	app, _ := openFixture(t)
	if _, err := app.AddClip("ring.mp4", 1, 5, "x"); err != nil {
		t.Fatalf("AddClip: %v", err)
	}
	id := app.Timeline().Clips[0].ID
	if _, err := app.SetTrim(id, 2, 3); err != nil {
		t.Fatalf("SetTrim: %v", err)
	}
	if c := app.Timeline().Clips[0]; c.In != 2 || c.Out != 3 {
		t.Fatalf("after trim want in/out 2/3, got %v/%v", c.In, c.Out)
	}
	if _, changed := app.Undo(); !changed {
		t.Fatal("undo of a trim must report changed")
	}
	if c := app.Timeline().Clips[0]; c.In != 1 || c.Out != 5 {
		t.Fatalf("undo should restore in/out 1/5, got %v/%v", c.In, c.Out)
	}
}

// TestNewEditClearsRedo: a fresh edit after an undo forks history (the redo branch
// is dropped), so Redo then no-ops — the standard undo-stack contract.
func TestNewEditClearsRedo(t *testing.T) {
	app, _ := openFixture(t)
	if _, err := app.AddClip("ring.mp4", 1, 3, "a"); err != nil {
		t.Fatalf("AddClip: %v", err)
	}
	app.Undo() // back to empty; redo would re-add "a"
	if _, err := app.AddClip("ring.mp4", 4, 6, "b"); err != nil {
		t.Fatalf("AddClip: %v", err)
	}
	if _, changed := app.Redo(); changed {
		t.Fatal("a new edit after undo must clear the redo branch")
	}
	if c := app.Timeline().Clips; len(c) != 1 || c[0].Label != "b" {
		t.Fatalf("want only the new clip 'b', got %+v", c)
	}
}

// TestBridgeUndoRedoVerbs: the dispatch table wires undo/redo and returns the
// {timeline,changed} envelope.
func TestBridgeUndoRedoVerbs(t *testing.T) {
	app, _ := openFixture(t)
	if _, err := app.AddClip("ring.mp4", 1, 3, "a"); err != nil {
		t.Fatalf("AddClip: %v", err)
	}
	r := callEnv(t, app, "undo", `{}`)
	if !r.OK {
		t.Fatalf("undo verb errored: %s", r.Error)
	}
	var resp struct {
		Timeline TimelineView `json:"timeline"`
		Changed  bool         `json:"changed"`
	}
	remarshal(t, r.Data, &resp)
	if !resp.Changed || len(resp.Timeline.Clips) != 0 {
		t.Fatalf("undo verb: changed=%v clips=%d (want true,0)", resp.Changed, len(resp.Timeline.Clips))
	}
	r = callEnv(t, app, "redo", `{}`)
	remarshal(t, r.Data, &resp)
	if !resp.Changed || len(resp.Timeline.Clips) != 1 {
		t.Fatalf("redo verb: changed=%v clips=%d (want true,1)", resp.Changed, len(resp.Timeline.Clips))
	}
}

// TestExportSelectionEmptyErrors: rendering a selection that matches no clips is a
// clear error, never a silent empty render. (A real render needs ffmpeg, so the
// happy path is exercised manually; this guards the selection-filter contract.)
func TestExportSelectionEmptyErrors(t *testing.T) {
	app, _ := openFixture(t)
	if _, err := app.AddClip("ring.mp4", 1, 3, "a"); err != nil {
		t.Fatalf("AddClip: %v", err)
	}
	if _, err := app.ExportSelection(nil, ""); err == nil {
		t.Error("ExportSelection with no ids should error")
	}
	if _, err := app.ExportSelection([]string{"does-not-exist"}, ""); err == nil {
		t.Error("ExportSelection matching no clips should error")
	}
	// The bridge verb surfaces the same error (ok=false), not a panic.
	r := callEnv(t, app, "export_selection", `{"ids":["nope"]}`)
	if r.OK {
		t.Error("export_selection verb with unknown ids should fail with a message")
	}
}
