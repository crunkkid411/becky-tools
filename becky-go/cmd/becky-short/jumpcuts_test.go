package main

import (
	"strings"
	"testing"

	"becky-go/internal/subs"
)

// planJumpcuts must intersect becky-cut's WHOLE-FILE keep decisions with THIS
// short's window and report how much dead air came out — the pacing decision
// Jordan asked to see, not just a rendered file.
func TestPlanJumpcuts_IntersectsWindowAndReportsRemovedSeconds(t *testing.T) {
	cache := newCutCache()
	cache.spans["src.mp4"] = []keepSpan{
		{In: 0, Out: 5},
		{In: 8, Out: 20}, // a 3s cut precedes this
		{In: 25, Out: 30},
	}
	j := job{Src: "src.mp4", In: 4, Out: 27}

	plan, err := planJumpcuts(cache, j)
	if err != nil {
		t.Fatalf("planJumpcuts: %v", err)
	}

	want := []keepSpan{{In: 4, Out: 5}, {In: 8, Out: 20}, {In: 25, Out: 27}}
	if len(plan.Spans) != len(want) {
		t.Fatalf("got %d spans, want %d: %+v", len(plan.Spans), len(want), plan.Spans)
	}
	for i, w := range want {
		if abs(plan.Spans[i].In-w.In) > 1e-9 || abs(plan.Spans[i].Out-w.Out) > 1e-9 {
			t.Errorf("span %d = %+v, want %+v", i, plan.Spans[i], w)
		}
	}

	// window = 27-4 = 23s; kept = (5-4)+(20-8)+(27-25) = 1+12+2 = 15s; removed = 8s.
	const wantRemoved = 8.0
	if abs(plan.RemovedSeconds-wantRemoved) > 1e-9 {
		t.Errorf("removed = %.3fs, want %.3fs", plan.RemovedSeconds, wantRemoved)
	}
}

// A keep-span becky-cut reports OUTSIDE the requested window entirely must
// not appear, and a span clipped down to a sliver by the window boundary must
// not survive as a real cut.
func TestPlanJumpcuts_DropsOutOfWindowAndSliverSpans(t *testing.T) {
	cache := newCutCache()
	cache.spans["src.mp4"] = []keepSpan{
		{In: 0, Out: 2},     // entirely before the window
		{In: 9.98, Out: 10}, // clips to a 0.02s sliver at the window edge
		{In: 10, Out: 20},
		{In: 40, Out: 50}, // entirely after the window
	}
	j := job{Src: "src.mp4", In: 10, Out: 20}

	plan, err := planJumpcuts(cache, j)
	if err != nil {
		t.Fatalf("planJumpcuts: %v", err)
	}
	if len(plan.Spans) != 1 || plan.Spans[0] != (keepSpan{In: 10, Out: 20}) {
		t.Fatalf("got %+v, want exactly [{10 20}]", plan.Spans)
	}
	// The 0.02s sliver is real removed time even though it's not a rendered span.
	if abs(plan.RemovedSeconds-0.0) > 1e-9 {
		t.Errorf("removed = %.3fs, want 0 (the sliver contributes ~0, and only the window counts)",
			plan.RemovedSeconds)
	}
}

// A source becky-cut has no cached decisions for at all must return the
// cached error, not panic or silently report zero spans.
func TestCutCache_MemoizesAndReturnsCachedError(t *testing.T) {
	c := newCutCache()
	c.errs["missing.mp4"] = errNoBeckyCut

	_, err := c.wholeFileSpans("missing.mp4")
	if err != errNoBeckyCut {
		t.Fatalf("got %v, want the cached error", err)
	}
}

var errNoBeckyCut = errTestSentinel("becky-cut not found")

type errTestSentinel string

func (e errTestSentinel) Error() string { return string(e) }

// THE bug this test exists to catch: captions computed as if each span kept
// its WINDOW-relative offset (span 2 starting at 25-10=15s, its position in
// the original continuous [10,28] window) instead of being laid end to end
// onto the CONCATENATED output the ffmpeg graph actually renders (span 2
// starting at 5s: right after span 1's 5s duration, per subs.Build's
// "starts exactly at the cut's start" invariant). Two kept spans with 10s of
// dead air removed between them: cue 1 must open at output 0s (span 1's own
// start) and cue 2 at output ~5s (span 1's duration) — NOT ~15s, which is
// what leaving span 2's words on the uncut window's own timeline would give.
func TestCaptionCuesJumpcut_LandOnTheCutTimelineNotTheOriginalWindow(t *testing.T) {
	words := []subs.Word{
		{Word: "one", Start: 11.0, End: 11.4},
		{Word: "two", Start: 26.0, End: 26.4},
	}
	spans := []keepSpan{{In: 10, Out: 15}, {In: 25, Out: 28}}
	cues := captionCuesJumpcut(words, 10, 28, spans, 30)

	if len(cues) != 2 {
		t.Fatalf("want 2 cues, got %d: %+v", len(cues), cues)
	}
	if got := cues[0].Start; got > 0.5 {
		t.Errorf("cue 1 (%q) starts at %.2fs, want ~0s (span 1 opens the output timeline)",
			cues[0].Text, got)
	}
	if got := cues[1].Start; got < 4.5 || got > 5.5 {
		t.Errorf("cue 2 (%q) starts at %.2fs, want ~5.0s on the CUT timeline "+
			"(right after span 1's 5s duration); ~15.0s would mean span 2 kept its "+
			"position in the ORIGINAL continuous window instead of the cut output",
			cues[1].Text, got)
	}
}

// A word outside the short's own window entirely (elsewhere in a long source
// transcript) must never be "rescued" onto one of this short's spans just
// because it's the nearest span in an unfiltered word list — the same trap
// captionCues' capWordPad guards against for the single-window path.
func TestCaptionCuesJumpcut_DoesNotRescueWordsFromOutsideTheShort(t *testing.T) {
	words := []subs.Word{
		{Word: "faraway", Start: 500.0, End: 500.4}, // minutes away in the source
		{Word: "here", Start: 11.0, End: 11.4},
	}
	spans := []keepSpan{{In: 10, Out: 15}}
	cues := captionCuesJumpcut(words, 10, 15, spans, 30)

	for _, c := range cues {
		if strings.Contains(strings.ToLower(c.Text), "faraway") {
			t.Fatalf("cue carries a word from outside the short's window: %+v", cues)
		}
	}
}

// framesForSpan must quantise from the SPAN's own duration, not the original
// window — the whole point of copying internal/reel's frame-count math here
// is that a multi-clip concat's total length matches the sum of each span's
// quantised frames exactly, or it drifts like the reel did.
func TestFramesForSpan_QuantisesFromTheSpanNotTheWindow(t *testing.T) {
	sp := keepSpan{In: 10, Out: 13.1} // 3.1s at 30fps = 93 frames
	if got := framesForSpan(sp, 30); got != 93 {
		t.Errorf("got %d frames, want 93", got)
	}
	// A degenerate (near-zero) span must still floor to 1 frame, not 0 (0
	// frames would break trim=end_frame in the ffmpeg filter graph).
	if got := framesForSpan(keepSpan{In: 10, Out: 10.001}, 30); got != 1 {
		t.Errorf("got %d frames for a near-zero span, want floor of 1", got)
	}
}
