package main

import (
	"testing"

	"becky-go/internal/subs"
)

// THE REGRESSION. Jordan, 2026-08-20: "whatever you did to change the cut-times
// based on the audio energy absolutely did not work - it now cuts off words and
// makes the footage completely unusable."
//
// The exact shape that did it: a shot boundary at t=5.0 with 0.6s of real dead
// air sitting entirely BEFORE it (4.4->5.0). boundaryTighten returns 0.6, the
// caller halves it, and takes 0.3s off the START of the span after the
// boundary — which is speech, because the silence was all on the other side.
// The word "BURGER" spanning 5.0->5.4 loses its first 300ms.
//
// Asserted as a VALUE, not truthiness: the second span must start at or before
// the word's start, not merely "somewhere earlier".
func TestPlanShotSpans_NeverTrimsIntoAWord(t *testing.T) {
	j := job{Src: "src.mp4", In: 0, Out: 10}
	cuts := []float64{5}
	// All the dead air is BEFORE the boundary; speech resumes immediately after.
	remove := []keepSpan{{In: 4.4, Out: 5.0}}
	words := []subs.Word{
		{Word: "so", Start: 4.0, End: 4.3},
		{Word: "BURGER", Start: 5.0, End: 5.4},
		{Word: "sauce", Start: 5.5, End: 5.9},
	}

	unguarded := planShotSpans(cuts, remove, j, defaultTighten, nil)
	guarded := planShotSpans(cuts, remove, j, defaultTighten, words)

	if len(unguarded.Spans) != 2 || len(guarded.Spans) != 2 {
		t.Fatalf("want 2 spans each, got %d and %d", len(unguarded.Spans), len(guarded.Spans))
	}

	// Prove the bug is real: unguarded, span 2 starts INSIDE "BURGER".
	if got := unguarded.Spans[1].In; got <= 5.0 {
		t.Fatalf("test no longer reproduces the bug: unguarded span starts at %.3f, want > 5.0", got)
	}

	// The fix: guarded, span 2 may not start inside the word.
	if got := guarded.Spans[1].In; got > 5.0 {
		t.Errorf("guarded span starts at %.3f — INSIDE the word 'BURGER' (5.0-5.4); "+
			"a trim may eat silence, never speech", got)
	}
	if guarded.ProtectedEdges != 1 {
		t.Errorf("ProtectedEdges = %d, want 1 — Jordan must be able to see the guard fired",
			guarded.ProtectedEdges)
	}
	// And it must not have cut the tail off the earlier span's last word either.
	if got := guarded.Spans[0].Out; got < 4.3 {
		t.Errorf("span 1 ends at %.3f, cutting the tail off 'so' (ends 4.3)", got)
	}
}

// The guard must NOT make becky timid. A boundary sitting in genuine silence
// still tightens by the full amount — that tightening is Jordan's own measured
// 150ms/cut and is the entire difference between his pacing and the master's.
func TestPlanShotSpans_StillTightensFullyInRealSilence(t *testing.T) {
	j := job{Src: "src.mp4", In: 0, Out: 10}
	cuts := []float64{5}
	words := []subs.Word{
		{Word: "before", Start: 3.0, End: 3.5},
		{Word: "after", Start: 6.5, End: 7.0},
	}
	plan := planShotSpans(cuts, nil, j, 0.2, words)

	if len(plan.Spans) != 2 {
		t.Fatalf("want 2 spans, got %d", len(plan.Spans))
	}
	if abs(plan.Spans[0].Out-4.9) > 1e-9 {
		t.Errorf("span 1 ends at %.4f, want 4.9 (full 0.1s trim in silence)", plan.Spans[0].Out)
	}
	if abs(plan.Spans[1].In-5.1) > 1e-9 {
		t.Errorf("span 2 starts at %.4f, want 5.1 (full 0.1s trim in silence)", plan.Spans[1].In)
	}
	if abs(plan.RemovedSeconds-0.2) > 1e-9 {
		t.Errorf("RemovedSeconds = %.4f, want 0.2", plan.RemovedSeconds)
	}
	if plan.ProtectedEdges != 0 {
		t.Errorf("ProtectedEdges = %d, want 0 — nothing to protect here", plan.ProtectedEdges)
	}
}

// clampInToWords / clampOutToWords are the whole rule, unit-tested directly so
// the boundary arithmetic is pinned independent of planShotSpans.
func TestClampToWords(t *testing.T) {
	words := []subs.Word{{Word: "hi", Start: 2.0, End: 2.4}}

	// Trim wants 2.2 (mid-word); must be pulled back clear of 2.0.
	if got := clampInToWords(2.2, 1.0, words); got > 2.0-wordEdgePad+1e-9 {
		t.Errorf("clampInToWords(2.2) = %.4f, want <= %.4f", got, 2.0-wordEdgePad)
	}
	// Floor wins over the word: never move the edge earlier than allowed.
	if got := clampInToWords(2.2, 2.15, words); abs(got-2.15) > 1e-9 {
		t.Errorf("clampInToWords floor = %.4f, want 2.15", got)
	}
	// Trim in clear silence is untouched.
	if got := clampInToWords(1.5, 1.0, words); abs(got-1.5) > 1e-9 {
		t.Errorf("clampInToWords(1.5) = %.4f, want 1.5 unchanged", got)
	}
	// End side: wants 2.2 (mid-word); must be pushed past 2.4.
	if got := clampOutToWords(2.2, 5.0, words); got < 2.4+wordEdgePad-1e-9 {
		t.Errorf("clampOutToWords(2.2) = %.4f, want >= %.4f", got, 2.4+wordEdgePad)
	}
	// Ceiling wins.
	if got := clampOutToWords(2.2, 2.3, words); abs(got-2.3) > 1e-9 {
		t.Errorf("clampOutToWords ceil = %.4f, want 2.3", got)
	}
}
