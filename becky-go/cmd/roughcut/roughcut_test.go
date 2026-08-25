package main

import (
	"math"
	"strings"
	"testing"

	"becky-go/internal/quotes"
)

// The bad-take detector is asserted on the SHAPES Jordan described in
// WE_TRIED.md, not on invented prose: stop-pause-restart, restarts that begin
// several words in, re-wordings, and chains of chains.

func cue(start, end float64, text string) quotes.Cue {
	return quotes.Cue{Start: start, End: end, Text: text}
}

func TestDetectsSimpleRestartChain(t *testing.T) {
	cues := []quotes.Cue{
		cue(0, 3, "So the FBI opened a file on him last spring"),
		cue(5, 8, "So the FBI opened a file on him last spring and"),
		cue(10, 16, "So the FBI opened a file on him last spring and nobody told us"),
	}
	got := detectBadTakes(cues)
	if len(got) != 1 {
		t.Fatalf("got %d bad takes, want 1: %+v", len(got), got)
	}
	b := got[0]
	if !b.Confident {
		t.Errorf("exact 5x word-run restart must be confident, got %+v", b)
	}
	if math.Abs(b.Start-0) > 0.01 || math.Abs(b.End-(10-0.05)) > 0.01 {
		t.Errorf("chain span = [%v,%v], want [0,9.95]", b.Start, b.End)
	}
	if b.LastCue != 1 {
		t.Errorf("LastCue = %d, want 1 (both abandoned attempts cut)", b.LastCue)
	}
}

func TestRestartBeginningSeveralWordsIn(t *testing.T) {
	cues := []quotes.Cue{
		cue(0, 4, "and at one point she said the whole thing was a joke"),
		cue(6, 10, "she said the whole thing was a joke but then"),
	}
	got := detectBadTakes(cues)
	if len(got) != 1 {
		t.Fatalf("mid-sentence re-start must be flagged, got %+v", got)
	}
	if got[0].Confident {
		t.Errorf("two complete deliveries, no abandonment signal: human decides, got %+v", got[0])
	}
}

func TestRewordingGoesToMarkerNotCut(t *testing.T) {
	cues := []quotes.Cue{
		cue(0, 4, "the restraining order was filed in Elkhart"),
		cue(6, 10, "the restraining order got filed last month"),
	}
	got := detectBadTakes(cues)
	if len(got) != 1 {
		t.Fatalf("re-wording should still be flagged, got %+v", got)
	}
	if got[0].Confident {
		t.Errorf("3-word re-worded restart must NOT be auto-cut: %+v", got[0])
	}
	// Two clean complete alternates of the same statement: flagged, and the
	// human picks in the morning.
	cut := []quotes.Cue{
		cue(0, 4, "and for a little while she thought it was funny"),
		cue(6, 10, "and at one point she thought it was funny"),
	}
	got = detectBadTakes(cut)
	if len(got) != 1 || got[0].Confident {
		t.Fatalf("two clean alternates must be a marker, not a cut: %+v", got)
	}
}

func TestObviousTwoTakeRetakeIsCut(t *testing.T) {
	// Jordan 2026-08-24: "there are absolutely times when 2 takes exist because
	// one is an obvious re-take" - the first attempt dies mid-sentence, he
	// pauses, then delivers the fuller version. That one is NOT his homework.
	cues := []quotes.Cue{
		cue(0, 2, "so the restraining order"),
		cue(4, 9, "so the restraining order was filed in Elkhart county"),
	}
	got := detectBadTakes(cues)
	if len(got) != 1 {
		t.Fatalf("obvious retake must be detected, got %+v", got)
	}
	if !got[0].Confident {
		t.Errorf("abandoned first take + fuller re-delivery must CUT: %+v", got[0])
	}
	if got[0].LastCue != 0 {
		t.Errorf("only the abandoned attempt is cut, got %+v", got[0])
	}
}

func TestNoCutWithoutPauseOrWithoutMatch(t *testing.T) {
	// No pause: continuous speech that repeats words is emphasis, not a retake.
	noPause := []quotes.Cue{
		cue(0, 3, "that is the fact that is the fact"),
		cue(3.2, 6, "that is the fact that is the fact"),
	}
	if got := detectBadTakes(noPause); len(got) != 0 {
		t.Errorf("no-pause repetition must not cut: %+v", got)
	}
	// Pause but unrelated sentences: nothing to cut.
	unrelated := []quotes.Cue{
		cue(0, 3, "the camera battery died twice"),
		cue(5, 8, "so the FBI opened a file on him"),
	}
	if got := detectBadTakes(unrelated); len(got) != 0 {
		t.Errorf("unrelated cues must not match: %+v", got)
	}
}

func TestNeverExtendsBackwardsAcrossCompletedSentence(t *testing.T) {
	cues := []quotes.Cue{
		cue(0, 3, "I want to be clear about one thing."),
		cue(4, 7, "the restraining order was filed in Elkhart"),
		cue(9, 13, "the restraining order was filed in Elkhart county"),
	}
	got := detectBadTakes(cues)
	if len(got) != 1 {
		t.Fatalf("want 1, got %+v", got)
	}
	if math.Abs(got[0].Start-4) > 0.01 {
		t.Errorf("cut must start at the abandoned attempt (4s), not bleed into the finished sentence: %v", got[0].Start)
	}
}

// --- zero-crossing snap -----------------------------------------------------

// silenceThenTone builds mono 16k audio: quietSec of near-silence then a sine
// burst. The keep boundary sits at the silence/tone edge.
func silenceThenTone(quietSec, toneSec float64, rate int, toneAmp float64) []float32 {
	n := int((quietSec + toneSec) * float64(rate))
	s := make([]float32, n)
	for i := 0; i < n; i++ {
		t := float64(i) / float64(rate)
		if t < quietSec {
			s[i] = 0.001 * float32(math.Sin(2*math.Pi*50*t)) // -60dBFS room tone
		} else {
			s[i] = float32(toneAmp * math.Sin(2*math.Pi*200*(t-quietSec)))
		}
	}
	return s
}

func TestSnapFindsQuietCrossingNearBoundary(t *testing.T) {
	rate := 16000
	s := silenceThenTone(1.0, 1.0, rate, 0.3)
	// ask 5ms INSIDE the tone: the snap must walk back to a crossing in the
	// quiet part, within the 20ms window.
	got, ok := snapBoundary(s, rate, 1.005, -40)
	if !ok {
		t.Fatal("expected a quiet crossing within the window")
	}
	if got > 1.001 || got < 1.005-0.021 {
		t.Errorf("snapped to %v, want within [0.984,1.001]", got)
	}
}

func TestSnapLeavesBoundaryWhenNoQuietNearby(t *testing.T) {
	rate := 16000
	n := rate * 2
	s := make([]float32, n)
	for i := range s { // continuous loud tone, no quiet anywhere
		s[i] = float32(0.5 * math.Sin(2*math.Pi*200*float64(i)/float64(rate)))
	}
	got, ok := snapBoundary(s, rate, 1.0, -40)
	if ok || got != 1.0 {
		t.Errorf("loud audio must leave the boundary untouched, got %v,%v", got, ok)
	}
}

// --- word-edge refinement ---------------------------------------------------

// Jordan, L2199 2026-08-24: "the timeline is littered with clips that are
// mostly room noise where I'm just adjusting myself preparing to deliver the
// line" - a multi-second lead-in must trim to the real first word, not just
// the old window's 0.8s.
func TestRefineWordEdgesTrimsLongRoomNoiseLeadIn(t *testing.T) {
	keeps := []span{{100.0, 105.0}}
	words := []span{{102.3, 102.6}, {102.7, 103.1}, {104.6, 104.8}}
	got := refineWordEdges(keeps, words)
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	if math.Abs(got[0].Start-102.24) > 0.01 {
		t.Errorf("Start = %v, want ~102.24 (2.3s room noise trimmed to the first word)", got[0].Start)
	}
	if math.Abs(got[0].End-104.98) > 0.01 {
		t.Errorf("End = %v, want ~104.98", got[0].End)
	}
}

// Regression for the 2026-08-24 live bug: a 0.79s keep whose first word ("You")
// starts just under the old window's 0.15s floor got skipped, so the window
// matched a LATER word instead and collapsed the keep to 0.06s. The fix must
// anchor to the first word overlapping the keep, not a floating window.
func TestRefineWordEdgesDoesNotDestroyShortKeep(t *testing.T) {
	keeps := []span{{443.62, 444.41}}
	words := []span{{443.68, 443.83}, {443.85, 443.95}, {443.97, 444.16}} // "You" "can" "see"
	got := refineWordEdges(keeps, words)
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	if got[0].End-got[0].Start < 0.6 {
		t.Fatalf("keep collapsed to %.2fs, want the full \"You can see\" preserved: %+v", got[0].End-got[0].Start, got[0])
	}
	if got[0].Start > 443.68 {
		t.Errorf("Start = %v trims into \"You\" (starts 443.68)", got[0].Start)
	}
}

// A word starting right at the keep edge (no real gap) must not trigger a
// no-op-sized "trim" that jitters the boundary.
func TestRefineWordEdgesNoOpWhenNoGap(t *testing.T) {
	keeps := []span{{10.0, 13.0}}
	words := []span{{10.02, 10.4}, {12.9, 12.98}}
	got := refineWordEdges(keeps, words)
	if got[0] != keeps[0] {
		t.Errorf("got %+v, want unchanged %+v (gaps under minGap)", got[0], keeps[0])
	}
}

// A gap between two words INSIDE a cue-merged keep (buttercut_proposal.md,
// measured: "a '13s cue' that contained a 6s silence") must split into two
// keeps, not survive as one span with a silent hole in the middle.
func TestSplitOnWordGapsSplitsInteriorSilence(t *testing.T) {
	keeps := []span{{0.0, 13.0}}
	words := []span{{0.1, 0.4}, {0.5, 0.9}, {7.0, 7.3}, {7.4, 7.8}, {7.9, 8.1}}
	got := splitOnWordGaps(keeps, words, 0.6)
	if len(got) != 2 {
		t.Fatalf("got %d spans, want 2 (the 0.9-7.0 gap is a 6.1s silence): %+v", len(got), got)
	}
	if got[0].End > 1.15 {
		t.Errorf("first span end = %v, should stop near word 2's end (0.9) + margin", got[0].End)
	}
	if got[1].Start < 6.8 {
		t.Errorf("second span start = %v, should resume near word 3's start (7.0) - margin", got[1].Start)
	}
}

// Gaps shorter than pause (conversational rhythm) must not fragment the keep.
func TestSplitOnWordGapsLeavesShortGapsAlone(t *testing.T) {
	keeps := []span{{0.0, 3.0}}
	words := []span{{0.1, 0.4}, {0.7, 1.0}, {1.3, 1.6}}
	got := splitOnWordGaps(keeps, words, 0.6)
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1 (all gaps under pause): %+v", len(got), got)
	}
}

// Regression for the 2026-08-24 live bug: real Parakeet word JSON contains
// genuine zero-duration entries ({"word":"And","start":132,"end":132}). A
// strict w.End>s overlap test excludes a word sitting exactly at the keep's
// start, so the search locks onto the NEXT word instead and clips the first
// one - here it dropped the QA gate's coverage for a real cue.
func TestRefineWordEdgesHandlesZeroDurationWords(t *testing.T) {
	keeps := []span{{131.4, 136.1}}
	words := []span{{132.0, 132.0}, {132.32, 132.32}, {132.56, 132.56}, {135.6, 135.6}}
	got := refineWordEdges(keeps, words)
	if got[0].Start > 132.0 {
		t.Errorf("Start = %v trims into the first word (132.0, zero-duration)", got[0].Start)
	}
}

// --- speaking corroboration --------------------------------------------------

// Jordan, 2026-08-24 (the "insufficient contextual data" feedback): LR-ASD
// speaking data must actually corroborate decisions, not just sit in a
// dossier file. A keep with real content but no visible speaker must be
// flagged for review - never silently cut (a detector is a signal, never a
// verdict).
func TestSpeakingCorroborationFlagsUnspokenKeep(t *testing.T) {
	keeps := []span{{10.0, 20.0}}
	speaking := []speakingWindow{{Start: 10.0, End: 20.0, Speakers: 0, BestFrac: 0.05}}
	got := speakingCorroboration("clip1", keeps, speaking)
	if len(got) != 1 {
		t.Fatalf("got %d markers, want 1 (no visible speaker despite kept audio): %+v", len(got), got)
	}
	if got[0].Kind != "review" {
		t.Errorf("kind = %q, want \"review\" - never a verdict", got[0].Kind)
	}
}

func TestSpeakingCorroborationSilentWhenSpeakerConfirmed(t *testing.T) {
	keeps := []span{{10.0, 20.0}}
	speaking := []speakingWindow{{Start: 10.0, End: 20.0, Speakers: 1, BestFrac: 0.95}}
	got := speakingCorroboration("clip1", keeps, speaking)
	if len(got) != 0 {
		t.Errorf("got %d markers, want 0 (visual confirms audio): %+v", len(got), got)
	}
}

func TestSpeakingCorroborationSkipsWhenNoVisualCoverage(t *testing.T) {
	keeps := []span{{10.0, 20.0}}
	// speaking window barely overlaps the keep - nothing to corroborate with
	speaking := []speakingWindow{{Start: 19.8, End: 21.0, Speakers: 0, BestFrac: 0.0}}
	got := speakingCorroboration("clip1", keeps, speaking)
	if len(got) != 0 {
		t.Errorf("got %d markers, want 0 (insufficient overlap to judge): %+v", len(got), got)
	}
}

// Regression for the 2026-08-24 night bug: every retake and speaking-
// corroboration pendingMarker is built with source=c.Stem ("HJOC7106", no
// extension), but events carry the full c.Path ("...\HJOC7106.MP4"). Matching
// on the bare basename never matched, so these markers silently never landed
// on the timeline, however many were generated.
func TestMapToTimelineMatchesStemAgainstFullPath(t *testing.T) {
	events := []event{
		{Source: `X:\Videos\HJOC7106.MP4`, In: 10, Out: 20, TLStart: 100},
	}
	got, ok := mapToTimeline(events, "HJOC7106", 15)
	if !ok {
		t.Fatal("mapToTimeline returned ok=false for a stem source against a full-path event - the exact bug")
	}
	if got != 105 {
		t.Errorf("got %v, want 105 (100 + (15-10))", got)
	}
}

// --- watch pass ---------------------------------------------------------

func TestMergeWatchBlocksJoinsCloseEventsAcrossSources(t *testing.T) {
	events := []tlEvent{
		{Source: "a.mp4", In: 0, Out: 2},
		{Source: "a.mp4", In: 2.5, Out: 5}, // 0.5s gap - merges
		{Source: "a.mp4", In: 20, Out: 22}, // far gap - new block
		{Source: "b.mp4", In: 0, Out: 3},
	}
	got := mergeWatchBlocks(events)
	if len(got) != 3 {
		t.Fatalf("got %d blocks, want 3: %+v", len(got), got)
	}
	if got[0].Source != "a.mp4" || got[0].Start != 0 || got[0].End != 5 {
		t.Errorf("block 0 = %+v, want a.mp4 [0,5] (joined across the 0.5s gap)", got[0])
	}
	if got[1].Start != 20 || got[1].End != 22 {
		t.Errorf("block 1 = %+v, want [20,22] (separate - gap too large)", got[1])
	}
}

func TestMergeWatchBlocksDropsSubMinimumSpans(t *testing.T) {
	events := []tlEvent{{Source: "a.mp4", In: 0, Out: 0.3}}
	if got := mergeWatchBlocks(events); len(got) != 0 {
		t.Errorf("got %d blocks, want 0 (below watchMinBlockSec): %+v", len(got), got)
	}
}

func TestParseWatchVerdictPass(t *testing.T) {
	v, reason := parseWatchVerdict("VERDICT: PASS\nREASON:")
	if v != "pass" || reason != "" {
		t.Errorf("got (%q,%q), want (pass, \"\")", v, reason)
	}
}

func TestParseWatchVerdictFlagExtractsReason(t *testing.T) {
	v, reason := parseWatchVerdict("VERDICT: FLAG\nREASON: he is silently reading his phone, no speech at all")
	if v != "flag" {
		t.Errorf("verdict = %q, want flag", v)
	}
	if reason != "he is silently reading his phone, no speech at all" {
		t.Errorf("reason = %q", reason)
	}
}

// An unparseable/garbled reply must never be treated as a flag - this pass
// may only ADD a review marker, never invent a false alarm from noise.
func TestParseWatchVerdictUnparseableDefaultsToPass(t *testing.T) {
	v, _ := parseWatchVerdict("the model said something unexpected here")
	if v != "pass" {
		t.Errorf("verdict = %q, want pass (fail open, not a false flag)", v)
	}
}

// --- marker triage -------------------------------------------------------

func TestTriageWindowPadsBothSides(t *testing.T) {
	pm := pendingMarker{T: 100.0, TEnd: 104.0}
	start, end := triageWindow(pm)
	if start != 94.0 || end != 110.0 {
		t.Errorf("window = [%v,%v], want [94,110] (span + 6s pad each side)", start, end)
	}
}

func TestTriageWindowClampsAtZero(t *testing.T) {
	pm := pendingMarker{T: 2.0, TEnd: 3.0}
	start, _ := triageWindow(pm)
	if start != 0 {
		t.Errorf("start = %v, want 0 (never watch before the file starts)", start)
	}
}

func TestTriageWindowCapsTotalLength(t *testing.T) {
	pm := pendingMarker{T: 100.0, TEnd: 140.0} // 40s span alone exceeds the cap
	start, end := triageWindow(pm)
	if got := end - start; got > triageWindowCap+0.01 {
		t.Errorf("window length = %v, want <= %v", got, triageWindowCap)
	}
}

func TestTriageWindowHandlesInstantMarker(t *testing.T) {
	pm := pendingMarker{T: 50.0} // TEnd unset (0), older-artifact shape
	start, end := triageWindow(pm)
	if start != 44.0 || end != 56.0 {
		t.Errorf("window = [%v,%v], want [44,56] (treated as a single instant at T)", start, end)
	}
}

func TestParseTriageVerdictResolved(t *testing.T) {
	v, reason := parseTriageVerdict("VERDICT: RESOLVED\nREASON: clearly him talking, real content")
	if v != "resolved" {
		t.Errorf("verdict = %q, want resolved", v)
	}
	if reason != "clearly him talking, real content" {
		t.Errorf("reason = %q", reason)
	}
}

func TestParseTriageVerdictNeedsReview(t *testing.T) {
	v, _ := parseTriageVerdict("VERDICT: NEEDS_REVIEW\nREASON: hard to tell if he's speaking off-camera")
	if v != "needs_review" {
		t.Errorf("verdict = %q, want needs_review", v)
	}
}

// Fails CLOSED: an unparseable reply must never resolve (silently drop) a
// marker that already exists - the opposite failure direction from
// parseWatchVerdict, which fails OPEN because it only ever adds a flag.
func TestParseTriageVerdictUnparseableDefaultsToNeedsReview(t *testing.T) {
	v, _ := parseTriageVerdict("the model said something unexpected here")
	if v != "needs_review" {
		t.Errorf("verdict = %q, want needs_review (fail closed, never silently resolve)", v)
	}
}

func TestApplyTriageVerdictsDropsOnlyResolvedMarker(t *testing.T) {
	existing := []markerOut{
		{T: 10, Title: "CHECK: audio kept here but LR-ASD saw no one visibly speaking (33%) - IQQP9972"},
		{T: 20, Title: "CHECK: audio kept here but LR-ASD saw no one visibly speaking (33%) - IQQP9972"}, // same title, different T - real observed shape
	}
	verdicts := []triageVerdict{
		{Marker: pendingMarker{TL: 10, Title: existing[0].Title}, Verdict: "resolved", Reason: "clearly speaking"},
		{Marker: pendingMarker{TL: 20, Title: existing[1].Title}, Verdict: "needs_review", Reason: "hard to tell"},
	}
	kept, resolved := applyTriageVerdicts(existing, verdicts)
	if resolved != 1 {
		t.Fatalf("resolved = %d, want 1", resolved)
	}
	if len(kept) != 1 || kept[0].T != 20 {
		t.Fatalf("kept = %+v, want only the T=20 marker (identical title must not cause a mismatch)", kept)
	}
	if !strings.Contains(kept[0].Title, "gemma4: hard to tell") {
		t.Errorf("kept title = %q, want it annotated with the model's reason", kept[0].Title)
	}
}

func TestApplyTriageVerdictsLeavesCallerSuppliedMarkersUntouched(t *testing.T) {
	existing := []markerOut{{T: 5, Title: "quote: he said X"}} // never in pendingMarkers/verdicts at all
	kept, resolved := applyTriageVerdicts(existing, nil)
	if resolved != 0 || len(kept) != 1 || kept[0] != existing[0] {
		t.Errorf("got kept=%+v resolved=%d, want the marker passed through unchanged", kept, resolved)
	}
}

func TestSnapExtendsToQuietPocket(t *testing.T) {
	rate := 16000
	// tone everywhere except a quiet pocket 0.1s after the boundary.
	n := rate * 2
	s := make([]float32, n)
	for i := range s {
		t := float64(i) / float64(rate)
		if t > 1.1 && t < 1.3 {
			s[i] = 0.0005
		} else {
			s[i] = float32(0.4 * math.Sin(2*math.Pi*200*t))
		}
	}
	got, ok := snapBoundary(s, rate, 1.0, -40)
	if !ok {
		t.Fatal("expected extension to the quiet pocket")
	}
	if got < 1.09 || got > 1.31 {
		t.Errorf("extended to %v, want inside the quiet pocket [1.09,1.31]", got)
	}
}
