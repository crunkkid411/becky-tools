package main

// edit_batch_test.go covers apply_edit_batch (BUILD_1.md H-4): the whole point
// is that a batch of clip-mutating ops is ONE undo span, not one snapshot per
// op — these tests assert that VALUE (Undo() count), not just "it builds".

import (
	"path/filepath"
	"testing"

	"becky-go/internal/assistant"
)

func TestApplyEditBatchIsOneUndoSpan(t *testing.T) {
	app, dir := openFixture(t)
	ring := filepath.Join(dir, "ring.mp4")

	tl, results, err := app.ApplyEditBatch([]EditOp{
		{Verb: "add_clip", Args: map[string]any{"source": ring, "in": 1.0, "out": 3.0, "label": "a"}},
		{Verb: "add_clip", Args: map[string]any{"source": ring, "in": 4.0, "out": 6.0, "label": "b"}},
	})
	if err != nil {
		t.Fatalf("ApplyEditBatch: %v", err)
	}
	if len(tl.Clips) != 2 {
		t.Fatalf("want 2 clips after batch, got %d", len(tl.Clips))
	}
	for i, r := range results {
		if !r.OK {
			t.Errorf("op %d (%s) failed: %s", i, r.Verb, r.Error)
		}
	}

	// The whole 2-op batch must undo in ONE Ctrl+Z, not two.
	after, changed := app.Undo()
	if !changed {
		t.Fatal("expected one undo to be available")
	}
	if len(after.Clips) != 0 {
		t.Fatalf("one undo should revert the WHOLE batch (want 0 clips), got %d", len(after.Clips))
	}
	if _, changed := app.Undo(); changed {
		t.Fatal("a single 2-op batch must cost exactly ONE undo entry, but a second undo found more history")
	}
}

func TestApplyEditBatchEmptyIsRejectedNoUndoEntry(t *testing.T) {
	app, _ := openFixture(t)
	if _, _, err := app.ApplyEditBatch(nil); err == nil {
		t.Fatal("an empty batch should error, not silently no-op")
	}
	if _, changed := app.Undo(); changed {
		t.Fatal("a rejected empty batch must not push an undo entry")
	}
}

func TestApplyEditBatchBadOpIsSkippedNotFatal(t *testing.T) {
	app, dir := openFixture(t)
	ring := filepath.Join(dir, "ring.mp4")

	tl, results, err := app.ApplyEditBatch([]EditOp{
		{Verb: "remove_clip", Args: map[string]any{"id": "no-such-clip"}},               // bad: should be reported, not crash
		{Verb: "add_clip", Args: map[string]any{"source": ring, "in": 1.0, "out": 2.0}}, // good
	})
	if err != nil {
		t.Fatalf("a batch with one bad op should not error at the batch level: %v", err)
	}
	if len(tl.Clips) != 1 {
		t.Fatalf("the good op should still apply; want 1 clip, got %d", len(tl.Clips))
	}
	if results[0].OK {
		t.Error("the bad remove_clip op should be reported as failed")
	}
	if !results[1].OK {
		t.Errorf("the good add_clip op should be reported OK, got error: %s", results[1].Error)
	}
}

func TestApplyEditBatchViaBridgeVerb(t *testing.T) {
	app, dir := openFixture(t)
	ring := filepath.Join(dir, "ring.mp4")

	r := callEnv(t, app, "apply_edit_batch", `{"ops":[
		{"verb":"add_clip","args":{"source":`+jsonStr(ring)+`,"in":1,"out":3}},
		{"verb":"add_clip","args":{"source":`+jsonStr(ring)+`,"in":4,"out":6}}
	]}`)
	if !r.OK {
		t.Fatalf("apply_edit_batch bridge call failed: %s", r.Error)
	}
	var body struct {
		Timeline TimelineView   `json:"timeline"`
		Results  []EditOpResult `json:"results"`
	}
	remarshal(t, r.Data, &body)
	if len(body.Timeline.Clips) != 2 {
		t.Fatalf("want 2 clips via the bridge verb, got %d", len(body.Timeline.Clips))
	}
	if len(body.Results) != 2 || !body.Results[0].OK || !body.Results[1].OK {
		t.Errorf("expected both ops OK in results, got %+v", body.Results)
	}
}

// TestApplyActionsBatchesClipMutationsIntoOneUndo is the H-6 regression: an
// approved multi-action AI proposal used to cost one Ctrl+Z per mutating
// action (AddClip/RemoveClip/etc each pushed their own undo snapshot).
// applyActions now routes clip-mutating actions through ApplyEditBatch, so
// the WHOLE approved pass reverts in one press.
func TestApplyActionsBatchesClipMutationsIntoOneUndo(t *testing.T) {
	app, dir := openFixture(t)
	ring := filepath.Join(dir, "ring.mp4")

	app.applyActions([]assistant.Action{
		{Verb: assistant.VerbAddClip, Args: map[string]any{"source": ring, "in": "1", "out": "3"}},
		{Verb: assistant.VerbAddClip, Args: map[string]any{"source": ring, "in": "4", "out": "6"}},
		{Verb: assistant.VerbSetLabel, Args: map[string]any{"id": "c1", "text": "relabeled"}},
	})

	tl := app.Timeline()
	if len(tl.Clips) != 2 {
		t.Fatalf("want 2 clips after the 3-action pass, got %d", len(tl.Clips))
	}
	if tl.Clips[0].Label != "relabeled" {
		t.Fatalf("want clip c1 relabeled, got %q", tl.Clips[0].Label)
	}

	after, changed := app.Undo()
	if !changed {
		t.Fatal("expected one undo to be available")
	}
	if len(after.Clips) != 0 {
		t.Fatalf("one undo should revert the ENTIRE 3-action AI pass (want 0 clips), got %d", len(after.Clips))
	}
	if _, changed := app.Undo(); changed {
		t.Fatal("a 3-action approved proposal must cost exactly ONE undo entry, but a second undo found more history")
	}
}
