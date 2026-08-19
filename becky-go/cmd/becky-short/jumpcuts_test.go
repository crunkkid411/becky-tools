package main

import (
	"strings"
	"testing"

	"becky-go/internal/config"
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

// boundaryTighten must use a REAL becky-cut REMOVE span found near the
// boundary, not the flag default, when one exists — this is what "becky-cut
// is used to tighten" means (research/jordan-edit-reverse-engineered.md,
// Finding 2), not always applying a flat number.
func TestBoundaryTighten_UsesRealDeadAirWhenFound(t *testing.T) {
	remove := []keepSpan{{In: 9.9, Out: 10.06}} // 0.16s of real dead air at the cut
	got := boundaryTighten(remove, 10.0, defaultTighten)
	if abs(got-0.16) > 1e-9 {
		t.Errorf("boundaryTighten = %.4f, want 0.16 (the real dead-air span)", got)
	}
}

// No remove span anywhere near the boundary must fall back to the flag
// default exactly — never zero, never something interpolated.
func TestBoundaryTighten_FallsBackToFlagDefault(t *testing.T) {
	remove := []keepSpan{{In: 100, Out: 101}} // nowhere near boundary=10
	got := boundaryTighten(remove, 10.0, defaultTighten)
	if got != defaultTighten {
		t.Errorf("boundaryTighten = %.4f, want the flag default %.4f", got, defaultTighten)
	}
}

// A long silence near a cut must not swallow the whole boundary — capped at
// 4x the flag default, so one real pause can't turn into an aggressive cut
// exactly where Jordan's own edit only trims 150ms.
func TestBoundaryTighten_CapsALongNearbySilence(t *testing.T) {
	remove := []keepSpan{{In: 9.0, Out: 11.0}} // 2s of "silence" spanning the boundary
	got := boundaryTighten(remove, 10.0, defaultTighten)
	want := defaultTighten * 4
	if abs(got-want) > 1e-9 {
		t.Errorf("boundaryTighten = %.4f, want the cap %.4f", got, want)
	}
}

// planShotSpans is the core of Part B: existing shot boundaries become span
// boundaries PRESERVED AS-IS, tightened by a small, explicit amount at each
// one — never re-cut with a silence threshold. Two interior cuts, no
// becky-cut remove spans (falls back to the flag default at both), asserted
// down to the exact span boundaries.
func TestPlanShotSpans_PreservesBoundariesAndTightensByTheFlagDefault(t *testing.T) {
	j := job{Src: "src.mp4", In: 0, Out: 10}
	cuts := []float64{4, 7} // two existing cuts inside the window
	plan := planShotSpans(cuts, nil, j, 0.2)

	want := []keepSpan{
		{In: 0, Out: 3.9},   // 4 - 0.2/2
		{In: 4.1, Out: 6.9}, // 4+0.1, 7-0.1
		{In: 7.1, Out: 10},  // 7+0.1
	}
	if len(plan.Spans) != len(want) {
		t.Fatalf("got %d spans, want %d: %+v", len(plan.Spans), len(want), plan.Spans)
	}
	for i, w := range want {
		if abs(plan.Spans[i].In-w.In) > 1e-9 || abs(plan.Spans[i].Out-w.Out) > 1e-9 {
			t.Errorf("span %d = %+v, want %+v", i, plan.Spans[i], w)
		}
	}
	if plan.ExistingCuts != 2 {
		t.Errorf("ExistingCuts = %d, want 2", plan.ExistingCuts)
	}
	if plan.PreservedCuts != 2 {
		t.Errorf("PreservedCuts = %d, want 2", plan.PreservedCuts)
	}
	// Each boundary tightened by 0.2s total (0.1 off each side) x2 boundaries.
	if abs(plan.RemovedSeconds-0.4) > 1e-9 {
		t.Errorf("RemovedSeconds = %.4f, want 0.4", plan.RemovedSeconds)
	}
}

// A cut too close to the window's own edge cannot become a usable span
// boundary (the resulting sliver would be dropped anyway) but must still
// count toward ExistingCuts — Jordan needs to see it was FOUND even if it
// wasn't used.
func TestPlanShotSpans_EdgeCutCountsAsExistingButNotPreserved(t *testing.T) {
	j := job{Src: "src.mp4", In: 0, Out: 10}
	cuts := []float64{0.05, 5} // 0.05 is inside jumpcutMinSpan of the window start
	plan := planShotSpans(cuts, nil, j, 0.1)

	if plan.ExistingCuts != 2 {
		t.Errorf("ExistingCuts = %d, want 2 (both found)", plan.ExistingCuts)
	}
	if plan.PreservedCuts != 1 {
		t.Errorf("PreservedCuts = %d, want 1 (only the real interior cut)", plan.PreservedCuts)
	}
}

// planPacing must degrade to the raw-footage path (planJumpcuts) when shot
// detection can't run at all (here: a source that doesn't exist), rather
// than failing the render — raw footage with no existing edit is the other
// real case and must still work.
func TestPlanPacing_DegradesToRawFootagePathWhenDetectionFails(t *testing.T) {
	cache := newCutCache()
	cache.spans["missing.mp4"] = []keepSpan{{In: 0, Out: 5}}
	j := job{Src: "missing.mp4", In: 0, Out: 5}

	plan, err := planPacing(config.Config{}, cache, j, defaultTighten)
	if err != nil {
		t.Fatalf("planPacing: %v", err)
	}
	if plan.ExistingCuts != 0 {
		t.Errorf("ExistingCuts = %d, want 0 (detection should have failed and degraded)", plan.ExistingCuts)
	}
	if len(plan.Spans) != 1 || plan.Spans[0] != (keepSpan{In: 0, Out: 5}) {
		t.Fatalf("got %+v, want the raw-footage plan from the cache", plan.Spans)
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

// Measured on the BLINDFOLD master (a three-person table scene): span 3 of 19
// found a subject in 46% of samples and the ENTIRE short was refused over it -
// 18 good spans thrown away for one. Jordan's own edit of that same footage
// holds 1.27s on a pointing finger with no face in frame, so "every span must
// hold a trackable subject" is not a rule he works to.
func TestTooManyDegraded_AMinorityFallsBackAMajorityRefuses(t *testing.T) {
	cases := []struct {
		degraded, total int
		want            bool
		why             string
	}{
		{1, 19, false, "the real BLINDFOLD case: one bad span must not kill eighteen good ones"},
		{6, 19, false, "six of nineteen degraded still rendered a usable short"},
		{9, 19, false, "just under half is still a short"},
		{10, 19, true, "past half, this window is not a talking-head short"},
		{19, 19, true, "nothing trackable anywhere"},
		{0, 0, false, "no spans at all is not a majority of anything"},
	}
	for _, c := range cases {
		if got := tooManyDegraded(c.degraded, c.total); got != c.want {
			t.Errorf("tooManyDegraded(%d, %d) = %v, want %v - %s", c.degraded, c.total, got, c.want, c.why)
		}
	}
}

// A degraded span was never tracked, so it must count against coverage. Leaving
// it out of BOTH the numerator and the denominator made the reported number
// describe only the spans that worked - becky-short claimed 0.952 on the
// BLINDFOLD render while an independent face pass over the rendered file
// measured 0.18. Honest accounting gives 0.579.
func TestUntrackedSamples_ADegradedSpanCountsAgainstCoverage(t *testing.T) {
	if got := untrackedSamples(3.79, 29.97); got != 113 {
		t.Errorf("untrackedSamples(3.79s @ 29.97fps) = %d, want 113 - the real span 3 of the "+
			"BLINDFOLD render, which used to contribute 0", got)
	}
	if got := untrackedSamples(0.01, 29.97); got != 1 {
		t.Errorf("untrackedSamples(0.01s) = %d, want 1: a span too short to round up is still "+
			"one unframed sample, never zero", got)
	}
	// The bug in one assertion: coverage over a 2-span render where one degraded.
	tracked, trackedFound := untrackedSamples(10, 30), 300
	degraded := untrackedSamples(10, 30)
	if cov := float64(trackedFound) / float64(tracked+degraded); cov != 0.5 {
		t.Errorf("coverage = %.3f, want 0.500 - half the output holds the subject; the old "+
			"accounting reported 1.000 for this exact case", cov)
	}
}

// Cuts are detected over the WHOLE source once and filtered to each window,
// never detected per window. The threshold is derived from the footage's own
// diff distribution, so a short mostly-static window lowers it and admits motion
// a whole-file scan rejects: measured on test-for-clips.mp4 the whole file
// reports ZERO cuts while the window [102.40,138.72] reported two, 133ms apart,
// on one continuous shot of a hand sweeping past the lens.
func TestCutsWithin_FiltersWholeFileCutsToTheWindow(t *testing.T) {
	all := []float64{5.0, 102.4, 110.0, 125.9, 138.72, 200.0}

	got := cutsWithin(all, 102.4, 138.72)
	want := []float64{110.0, 125.9}
	if len(got) != len(want) {
		t.Fatalf("cutsWithin = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cut %d = %v, want %v", i, got[i], want[i])
		}
	}
	// Strictly inside: a cut exactly ON a boundary is that boundary, not a cut
	// within the span, and keeping it would make a zero-length first span.
	if in := cutsWithin(all, 102.4, 138.72); containsF(in, 102.4) || containsF(in, 138.72) {
		t.Errorf("a cut on the window edge survived: %v", in)
	}
	if got := cutsWithin(all, 300, 400); len(got) != 0 {
		t.Errorf("cutsWithin outside any cut = %v, want empty", got)
	}
}

func containsF(xs []float64, v float64) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// Every word becky-cut DELIBERATELY REMOVED overlaps no kept span, and
// WordsPerSegment's rescue would retime all of it onto the spans that survived.
// Measured on a real render (26.16s window, 18.86s removed, 7.3s out): 16 cues
// crammed into the first 3.3 seconds, one with a ZERO duration, against audio
// running the full 7.2s. This is the same rescue bug already fixed once for the
// single-window path (110 captions -> 18), returning through the jumpcut path.
func TestWordsInSpans_DropsWhatTheJumpcutRemoved(t *testing.T) {
	words := []subs.Word{
		{Word: "kept-a", Start: 1.0, End: 1.4},
		{Word: "removed", Start: 5.0, End: 5.4}, // sits in the dead air that was cut
		{Word: "kept-b", Start: 9.0, End: 9.4},
		{Word: "far", Start: 40.0, End: 40.4},
	}
	spans := []keepSpan{{In: 0.5, Out: 2.0}, {In: 8.5, Out: 10.0}}

	got := wordsInSpans(words, spans)

	if len(got) != 2 {
		t.Fatalf("kept %d words, want 2 (only the ones inside a surviving span): %v", len(got), texts(got))
	}
	if got[0].Word != "kept-a" || got[1].Word != "kept-b" {
		t.Errorf("kept %v, want [kept-a kept-b]", texts(got))
	}
	// A word straddling a span edge survives - Parakeet's clock drifts against
	// the cut points by ~80ms and the cut is ground truth, not the transcript.
	edge := []subs.Word{{Word: "straddle", Start: 1.9, End: 2.1}}
	if len(wordsInSpans(edge, spans)) != 1 {
		t.Error("a word straddling the span edge was dropped; it is audible in the kept side")
	}
	if len(wordsInSpans(words, nil)) != 0 {
		t.Error("no spans should keep no words")
	}
}

func texts(ws []subs.Word) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.Word
	}
	return out
}
