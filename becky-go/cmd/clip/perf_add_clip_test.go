package main

import (
	"testing"
	"time"
)

// TestAddClipLatencyStaysFlat is the I-2 regression guard (BUILD_1.md contract
// item I-2: "Adding a search hit to the timeline is interactive in <200ms —
// proxy building never gates the add"). The 2026-07-23 cycle-25 review flagged
// real, measured growth from ~16ms to 130-172ms per add_clip call once a reel
// passes ~5,000 clips - an O(n)-per-add cost hiding inside every single edit
// (pushUndoLocked deep-copied the WHOLE clip list on every mutation, and three
// clip mutators - SetTrim/Split/SetLabel - wrote into the live slice in place,
// which is what blocked switching the undo snapshot to an O(1) header copy:
// an in-place write into a shared backing array would have silently corrupted
// any earlier snapshot's view of that same index).
//
// This test adds 6,000 clips (Jordan's real reels run past 5,000) and asserts
// the LAST 100 calls' mean latency stays well under the contract's 200ms bar,
// so a regression back to the old O(n)-per-add behavior fails a build instead
// of shipping silently again.
func TestAddClipLatencyStaysFlat(t *testing.T) {
	app, _ := openFixture(t)
	const total = 6000
	var lastBatch []time.Duration
	for i := 0; i < total; i++ {
		start := time.Now()
		if _, err := app.AddClipAt("ring.mp4", float64(i), float64(i)+1, "", -1); err != nil {
			t.Fatalf("AddClipAt #%d: %v", i, err)
		}
		el := time.Since(start)
		if i >= total-100 {
			lastBatch = append(lastBatch, el)
		}
	}
	var sum, max time.Duration
	for _, d := range lastBatch {
		sum += d
		if d > max {
			max = d
		}
	}
	mean := sum / time.Duration(len(lastBatch))
	t.Logf("after %d clips: mean=%v max=%v (last 100 add_clip calls)", total, mean, max)
	if mean > 40*time.Millisecond {
		t.Fatalf("add_clip mean latency at %d clips = %v (max %v), want < 40ms - "+
			"this is the I-2 regression bar (contract is <200ms; 40ms leaves headroom "+
			"for the JSON round trip to the C++ side which this test does not cross)",
			total, mean, max)
	}
}

// TestClipMutatorsNeverAliasUndoSnapshot proves the copy-on-write fix in
// SetTrim/Split/SetLabel: take a snapshot (add one clip, which pushes the
// pre-edit state to the undo stack), mutate the clip via each of the three
// in-place-look ing setters, then Undo and confirm the restored clip's fields
// are the ORIGINAL values, not whatever the mutator wrote. Before the fix,
// these setters wrote a.reel.Clips[i].Field directly into the slice backing
// array; once the undo snapshot stopped deep-copying (this session's I-2 fix),
// that in-place write would have silently corrupted the snapshot too, so
// Undo would restore the ALREADY-MUTATED value instead of the original -
// a silent undo-history corruption that would only show up as "undo did
// nothing" in Jordan's hands, never in a build log.
func TestClipMutatorsNeverAliasUndoSnapshot(t *testing.T) {
	app, _ := openFixture(t)
	tl, err := app.AddClipAt("ring.mp4", 1, 5, "original label", -1)
	if err != nil {
		t.Fatalf("AddClipAt: %v", err)
	}
	if len(tl.Clips) != 1 {
		t.Fatalf("want 1 clip, got %d", len(tl.Clips))
	}
	id := tl.Clips[0].ID

	// SetLabel: pushes a NEW undo snapshot of the pre-label state, then mutates.
	if _, err := app.SetLabel(id, "changed label"); err != nil {
		t.Fatalf("SetLabel: %v", err)
	}
	tl, changed := app.Undo()
	if !changed {
		t.Fatalf("Undo() after SetLabel reported no change")
	}
	if tl.Clips[0].Label != "original label" {
		t.Fatalf("undo restored label %q, want %q (undo snapshot was aliased/corrupted)", tl.Clips[0].Label, "original label")
	}

	// SetTrim: same check on In/Out.
	if _, err := app.SetTrim(id, 2, 8); err != nil {
		t.Fatalf("SetTrim: %v", err)
	}
	tl, changed = app.Undo()
	if !changed {
		t.Fatalf("Undo() after SetTrim reported no change")
	}
	if tl.Clips[0].In != 1 || tl.Clips[0].Out != 5 {
		t.Fatalf("undo restored In/Out %v/%v, want 1/5 (undo snapshot was aliased/corrupted)", tl.Clips[0].In, tl.Clips[0].Out)
	}

	// Split: the LEFT half's Out is written where the old in-place code mutated
	// a.reel.Clips[idx] directly. Confirm undo restores the single pre-split clip.
	if _, _, err := app.Split(id, 3); err != nil {
		t.Fatalf("Split: %v", err)
	}
	tl, changed = app.Undo()
	if !changed {
		t.Fatalf("Undo() after Split reported no change")
	}
	if len(tl.Clips) != 1 {
		t.Fatalf("undo after split left %d clips, want 1 (undo snapshot was aliased/corrupted)", len(tl.Clips))
	}
	if tl.Clips[0].In != 1 || tl.Clips[0].Out != 5 {
		t.Fatalf("undo after split restored In/Out %v/%v, want 1/5", tl.Clips[0].In, tl.Clips[0].Out)
	}
}
