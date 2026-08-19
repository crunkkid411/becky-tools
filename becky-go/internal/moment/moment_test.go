package moment

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

// seg is a terse cue constructor for the fixtures below.
func seg(start, end float64, text string) Segment {
	return Segment{Start: start, End: end, Text: text}
}

// story is a transcript with a clean hook, a build, and a landed payoff, spoken
// at a normal pace with a real pause before and after. It is the shape every
// assertion below is measured against.
func story() []Segment {
	return []Segment{
		seg(0.0, 3.0, "Some unrelated chatter before we begin."),
		// 2.0s pause -> a clear thought boundary.
		seg(5.0, 9.0, "Here is the single biggest mistake people make with their taxes."),
		seg(9.2, 14.0, "They assume the standard deduction is always the better deal."),
		seg(14.2, 19.0, "So they never actually run the numbers on itemising."),
		seg(19.2, 24.0, "I checked mine last year and found four thousand dollars."),
		seg(24.2, 28.0, "Run both every single time, it takes ten minutes."),
		// 2.0s pause after the payoff.
		seg(30.0, 34.0, "Anyway, that is all I wanted to cover today."),
	}
}

func TestAutoThoughtGap_DerivesFromTheTranscriptNotAConstant(t *testing.T) {
	// Measured on Jordan's own footage (E:\TakingBack2007, 177 real Parakeet
	// transcripts, 2026-08-18): the p75 inter-cue gap is 0.72-0.80s on every
	// full-length recording, i.e. the derivation is live and the floor is not
	// doing the work. Only very short or unusually dense transcripts clamp.
	// These fixtures cover both regimes and assert the value TRACKS the data.

	// Dense: every gap 0.2s. p75 = 0.2, below the floor, so it clamps. That is
	// deliberate — deriving 0.2 here would make a quarter of all cue breaks a
	// "new thought" and shatter the transcript into fragments.
	tight := []Segment{
		seg(0, 1, "a"), seg(1.2, 2, "b"), seg(2.2, 3, "c"),
		seg(3.2, 4, "d"), seg(4.2, 5, "e"), seg(5.2, 6, "f"),
	}
	if got := AutoThoughtGap(tight); got != minThoughtGap {
		t.Errorf("AutoThoughtGap(all 0.2s gaps) = %v, want the %v floor", got, minThoughtGap)
	}

	// Typical of his real recordings: p75 around 0.72s, comfortably derived.
	real := []Segment{
		seg(0, 1, "a"), seg(1.24, 2, "b"), seg(2.24, 3, "c"),
		seg(3.24, 4, "d"), seg(4.72, 5, "e"), seg(5.72, 6, "f"),
		seg(6.72, 7, "g"), seg(7.80, 8, "h"),
	}
	gotReal := AutoThoughtGap(real)
	if gotReal <= minThoughtGap || gotReal >= maxThoughtGap {
		t.Errorf("AutoThoughtGap(realistic parakeet) = %v, want a DERIVED value strictly inside (%v,%v)",
			gotReal, minThoughtGap, maxThoughtGap)
	}

	loose := []Segment{
		seg(0, 1, "a"), seg(2, 3, "b"), seg(4, 5, "c"),
		seg(6, 7, "d"), seg(8, 9, "e"), seg(10, 11, "f"),
	}
	if got := AutoThoughtGap(loose); got != 1.0 {
		t.Errorf("AutoThoughtGap(all 1.0s gaps) = %v, want 1.0", got)
	}

	// The constant-does-not-transfer lesson (STATE-OF-MASTER 2026-07-19): three
	// different transcripts must not collapse to one threshold. Asserting all
	// three are distinct is what makes this a test of derivation rather than a
	// test that a constant is still the constant.
	if gotReal == AutoThoughtGap(loose) || gotReal == AutoThoughtGap(tight) {
		t.Error("AutoThoughtGap returned the same value for different transcripts — it is behaving like a constant")
	}
}

func TestAutoThoughtGap_TooFewGapsFallsToFloor(t *testing.T) {
	if got := AutoThoughtGap([]Segment{seg(0, 1, "a")}); got != minThoughtGap {
		t.Errorf("AutoThoughtGap(single cue) = %v, want %v", got, minThoughtGap)
	}
}

func TestFind_WindowsStartAndEndOnThoughtBoundaries(t *testing.T) {
	cands := Find(story(), Options{MinDuration: 10, MaxDuration: 40})
	if len(cands) == 0 {
		t.Fatal("Find returned no candidates for a clean hook/build/payoff transcript")
	}

	// Every window must begin at a cue start and end at a cue end that exist in
	// the transcript — never at an interpolated time.
	starts := map[float64]bool{}
	ends := map[float64]bool{}
	for _, s := range story() {
		starts[s.Start] = true
		ends[s.End] = true
	}
	for _, c := range cands {
		if !starts[c.Start] {
			t.Errorf("candidate start %.2f is not a cue boundary", c.Start)
		}
		if !ends[c.End] {
			t.Errorf("candidate end %.2f is not a cue boundary", c.End)
		}
	}
}

func TestFind_PrefersTheSelfContainedStoryOverAMidSetupWindow(t *testing.T) {
	cands := Find(story(), Options{MinDuration: 10, MaxDuration: 40})
	var best Candidate
	for _, c := range cands {
		if c.Score > best.Score {
			best = c
		}
	}
	// The story proper starts at 5.0 ("Here is the single biggest mistake...").
	if best.Start != 5.0 {
		t.Errorf("best candidate starts at %.2f, want 5.00 (the hook cue); text=%q", best.Start, best.Text)
	}
	if best.Signals.SelfContained < 0.9 {
		t.Errorf("best candidate SelfContained = %.2f, want >= 0.90", best.Signals.SelfContained)
	}
}

func TestSelfContainedScore_PenalisesDanglingOpeners(t *testing.T) {
	cases := []struct {
		text string
		max  float64
	}{
		{"That's why I never trust the standard deduction.", 0.50},
		{"So he said the whole thing was a write-off.", 0.45},
		{"He told me it was fine.", 0.85},
		{"Anyway, back to the point.", 0.50},
	}
	for _, tc := range cases {
		if got := selfContainedScore(tc.text); got > tc.max {
			t.Errorf("selfContainedScore(%q) = %.2f, want <= %.2f", tc.text, got, tc.max)
		}
	}
	// The control: a clean declarative opener scores full marks.
	if got := selfContainedScore("Here is the single biggest mistake people make."); got != 1.0 {
		t.Errorf("selfContainedScore(clean opener) = %.2f, want 1.00", got)
	}
}

func TestEndsSentence_TrailOffIsNotACompletion(t *testing.T) {
	cases := map[string]bool{
		"It takes ten minutes.": true,
		`he said "no."`:         true,
		"Is that even legal?":   true,
		"and then we":           false,
		"I was going to...":     false,
		"I was going to…":       false,
		"Stop right there!":     true,
		"":                      false,
	}
	for text, want := range cases {
		if got := endsSentence(text); got != want {
			t.Errorf("endsSentence(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestPayoffScore_DropsWhenTheNextCueContinuesTheClause(t *testing.T) {
	// Cue 0 ends without punctuation and cue 1 opens on "and" -> the thought did
	// not end at cue 0.
	segs := []Segment{
		seg(0, 4, "I ran the numbers twice"),
		seg(4.1, 8, "and it still came out the same"),
	}
	continued := payoffScore(segs, 0, 0.35)

	// Same timing, but the next cue starts a new thought.
	segs2 := []Segment{
		seg(0, 4, "I ran the numbers twice."),
		seg(4.1, 8, "Nobody believed me."),
	}
	completed := payoffScore(segs2, 0, 0.35)

	if !(completed > continued) {
		t.Errorf("payoff: completed=%.2f should exceed continued=%.2f", completed, continued)
	}
	if continued > 0.35 {
		t.Errorf("payoff for a clause continued by 'and' = %.2f, want <= 0.35", continued)
	}
}

func TestFind_RespectsDurationBounds(t *testing.T) {
	cands := Find(story(), Options{MinDuration: 10, MaxDuration: 20})
	for _, c := range cands {
		if c.Dur() < 10-1e-9 {
			t.Errorf("candidate %.2f-%.2f is %.2fs, below MinDuration 10", c.Start, c.End, c.Dur())
		}
		// Extended candidates may exceed MaxDuration by at most ExtendBudget.
		limit := 20.0 + DefaultOptions().ExtendBudget
		if c.Dur() > limit+1e-9 {
			t.Errorf("candidate %.2f-%.2f is %.2fs, beyond MaxDuration+budget %.2f", c.Start, c.End, c.Dur(), limit)
		}
		if !c.Extended && c.Dur() > 20+1e-9 {
			t.Errorf("non-extended candidate %.2fs exceeds MaxDuration 20", c.Dur())
		}
	}
}

func TestFind_ExtendsPastTheLimitToLandThePayoff(t *testing.T) {
	// A single unbroken thought that only completes at 26s. With MaxDuration 20
	// and a budget of 8, the ending-completion pass must reach the 26s cue rather
	// than emitting nothing or cutting mid-sentence.
	segs := []Segment{
		seg(0, 6, "The first thing you need to understand is the timing"),
		seg(6.1, 12, "because the window opens in April and closes in June"),
		seg(12.1, 19, "and most people only find this out in July"),
		seg(19.1, 26, "which is exactly why they miss it every single year."),
	}
	cands := Find(segs, Options{MinDuration: 10, MaxDuration: 20, ExtendBudget: 8})
	var found bool
	for _, c := range cands {
		if c.Extended && math.Abs(c.End-26.0) < 1e-9 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an extended candidate ending at 26.00s; got %d candidates: %+v", len(cands), cands)
	}
}

func TestFind_IsDeterministic(t *testing.T) {
	a := Find(story(), Options{MinDuration: 10, MaxDuration: 40})
	b := Find(story(), Options{MinDuration: 10, MaxDuration: 40})
	if len(a) != len(b) {
		t.Fatalf("Find is not deterministic: %d vs %d candidates", len(a), len(b))
	}
	for i := range a {
		if a[i].Start != b[i].Start || a[i].End != b[i].End || a[i].Score != b[i].Score {
			t.Fatalf("Find is not deterministic at %d: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestFind_HonoursMaxCandidates(t *testing.T) {
	var segs []Segment
	for i := 0; i < 200; i++ {
		t0 := float64(i) * 6
		segs = append(segs, seg(t0, t0+5, "This is a complete standalone sentence number."))
	}
	cands := Find(segs, Options{MinDuration: 10, MaxDuration: 40, MaxCandidates: 7})
	if len(cands) != 7 {
		t.Fatalf("len(candidates) = %d, want exactly 7", len(cands))
	}
	// And they must come back in transcript order despite being chosen by score.
	for i := 1; i < len(cands); i++ {
		if cands[i].Start < cands[i-1].Start {
			t.Errorf("candidates not in transcript order at %d: %.2f after %.2f", i, cands[i].Start, cands[i-1].Start)
		}
	}
}

func TestFind_EmptyAndDegenerateInput(t *testing.T) {
	if got := Find(nil, DefaultOptions()); got != nil {
		t.Errorf("Find(nil) = %v, want nil", got)
	}
	if got := Find([]Segment{seg(0, 0, "   ")}, DefaultOptions()); got != nil {
		t.Errorf("Find(blank cue) = %v, want nil", got)
	}
	// An end before its start must be repaired, not panic.
	Find([]Segment{seg(5, 1, "backwards"), seg(6, 20, "fine.")}, DefaultOptions())
}

// --- Rank / corroboration -------------------------------------------------

func TestRank_NoJudgeIsACandidateNotAConclusion(t *testing.T) {
	cands := Find(story(), Options{MinDuration: 10, MaxDuration: 40})
	ranked := Rank(cands, nil)
	if len(ranked) != len(cands) {
		t.Fatalf("Rank dropped candidates: %d in, %d out", len(cands), len(ranked))
	}
	for _, r := range ranked {
		if r.Confidence != CandidateOnly {
			t.Errorf("with no judge, confidence = %q, want %q", r.Confidence, CandidateOnly)
		}
		if r.Final != r.Score {
			t.Errorf("with no judge, Final = %.3f, want the structural prior %.3f", r.Final, r.Score)
		}
		if !strings.Contains(r.Basis, "second independent signal") {
			t.Errorf("basis must say a second signal is needed, got %q", r.Basis)
		}
	}
}

// The scenario HANDOFF-SHORTS-PIPELINE.md §7 names by name: the top-ranked
// moment was chosen from words alone and had him bent out of shot. Two
// candidates, IDENTICAL structural score, differing only in face coverage —
// the covered one must outrank the uncovered one, with real numbers, not
// merely "reordered somehow".
func TestRank_LowFaceCoverageSinksAHighStructuralScore(t *testing.T) {
	offScreen := Candidate{Start: 0, End: 20, Score: 0.95, Face: 0.0, FaceBasis: "face coverage 0.00 (0 sampled sighting(s))"}
	onScreen := Candidate{Start: 30, End: 50, Score: 0.95, Face: 1.0, FaceBasis: "face coverage 1.00 (10 sampled sighting(s))"}

	ranked := Rank([]Candidate{offScreen, onScreen}, nil)

	// Best-first: the on-screen moment must sort ahead of the identically-
	// scored off-screen one.
	if ranked[0].Start != 30 {
		t.Fatalf("top moment starts at %.0f, want the ON-SCREEN one (starts at 30) ranked first", ranked[0].Start)
	}
	var offFinal, onFinal float64
	for _, r := range ranked {
		if r.Start == 0 {
			offFinal = r.Final
		} else {
			onFinal = r.Final
		}
	}
	// Same structural score (0.95) for both. On-screen coverage clears
	// faceMinCoverage so its score is untouched: 0.95. Off-screen coverage is
	// 0, the floor multiplier, so its score is 0.95*0.35 = 0.3325.
	if want := 0.95; math.Abs(onFinal-want) > 1e-9 {
		t.Errorf("on-screen Final = %.4f, want %.4f (full coverage leaves the structural score untouched)", onFinal, want)
	}
	if want := 0.95 * 0.35; math.Abs(offFinal-want) > 1e-9 {
		t.Errorf("off-screen Final = %.4f, want %.4f (0.95 structure * 0.35 floor multiplier)", offFinal, want)
	}
	if !(onFinal > offFinal) {
		t.Fatalf("on-screen (%.4f) must outrank off-screen (%.4f) despite equal structure", onFinal, offFinal)
	}
}

// Being on screen does not make a moment GOOD — coverage at or above the
// threshold must leave the structural score exactly where it was, never
// boost it. This is what makes facePenalty asymmetric with the audio fold.
func TestFacePenalty_HighCoverageLeavesScoreUntouched(t *testing.T) {
	for _, cov := range []float64{0.5, 0.75, 1.0} {
		if got := facePenalty(cov); got != 1.0 {
			t.Errorf("facePenalty(%.2f) = %.4f, want 1.0 (coverage at/above the threshold must not change the score)", cov, got)
		}
	}
}

// Values, not truthiness: pin the exact multiplier at zero and at the
// midpoint of the ramp.
func TestFacePenalty_ValuesBelowThreshold(t *testing.T) {
	if got, want := facePenalty(0.0), 0.35; math.Abs(got-want) > 1e-9 {
		t.Errorf("facePenalty(0) = %.4f, want %.4f (the floor multiplier)", got, want)
	}
	// coverage 0.25 is halfway to faceMinCoverage (0.5): multiplier should sit
	// halfway between the floor (0.35) and 1.0, i.e. 0.675.
	if got, want := facePenalty(0.25), 0.675; math.Abs(got-want) > 1e-9 {
		t.Errorf("facePenalty(0.25) = %.4f, want %.4f (linear ramp, halfway to threshold)", got, want)
	}
}

// With no face signal computed at all (FaceBasis empty — e.g. no media file,
// or the detector failed), ranking must fall back to structure/audio alone.
// A candidate must never be silently penalised for a signal that never ran.
func TestRank_NoFaceSignalLeavesScoreUnchanged(t *testing.T) {
	cands := []Candidate{{Start: 0, End: 20, Score: 0.80}} // Face/FaceBasis left zero-value
	ranked := Rank(cands, nil)
	if ranked[0].Final != 0.80 {
		t.Errorf("Final = %.4f, want the untouched structural score 0.80 — no FaceBasis means no signal ran", ranked[0].Final)
	}
}

// The candidate-only basis line names the face signal when it ran, same as
// it already does for audio.
func TestRank_CandidateOnlyBasisNamesTheFaceSignal(t *testing.T) {
	cands := []Candidate{{Start: 0, End: 20, Score: 0.80, Face: 0.9, FaceBasis: "face coverage 0.90 (9 sampled sighting(s))"}}
	ranked := Rank(cands, nil)
	if !strings.Contains(ranked[0].Basis, "face: face coverage 0.90") {
		t.Errorf("basis = %q, want it to name the face evidence", ranked[0].Basis)
	}
}

func TestRank_AgreementConcludes(t *testing.T) {
	cands := []Candidate{{Start: 0, End: 20, Score: 0.80}}
	ranked := Rank(cands, []Judgement{{Index: 0, Score: 85, Complete: true, Reason: "strong hook"}})
	if ranked[0].Confidence != Conclusion {
		t.Fatalf("confidence = %q, want %q", ranked[0].Confidence, Conclusion)
	}
	want := 0.5*0.80 + 0.5*(85.0/99.0)
	if math.Abs(ranked[0].Final-want) > 1e-9 {
		t.Errorf("Final = %.6f, want %.6f", ranked[0].Final, want)
	}
}

func TestRank_DisagreementIsHeldAtTheLowerSignal(t *testing.T) {
	// Structure loves it (0.90), the model says it is worthless (5/99 = 0.05).
	cands := []Candidate{{Start: 0, End: 20, Score: 0.90}}
	ranked := Rank(cands, []Judgement{{Index: 0, Score: 5, Complete: true, Reason: "nothing happens"}})
	if ranked[0].Confidence != Disputed {
		t.Fatalf("confidence = %q, want %q", ranked[0].Confidence, Disputed)
	}
	if ranked[0].Final > 0.06 {
		t.Errorf("Final = %.3f, want the LOWER signal (~0.05), not an average", ranked[0].Final)
	}
	if !strings.Contains(ranked[0].Basis, "DISPUTED") {
		t.Errorf("basis must name the dispute, got %q", ranked[0].Basis)
	}
}

func TestRank_IncompleteArcIsVetoed(t *testing.T) {
	// The property, not one hand-picked pair: NO clip the content pass calls
	// incomplete may outrank ANY clip it calls complete — at any combination of
	// structural and content scores. The previous version of this test asserted
	// a single favourable pair and passed while the code merely applied a 0.6
	// discount, which let a high-scoring clip that trails off beat a modest one
	// that lands. That is the "short with no payoff" failure in §2.1.
	scores := []float64{0.05, 0.25, 0.50, 0.75, 0.95}
	content := []int{5, 25, 50, 75, 99}

	var worstComplete = 1.1
	var bestVetoed = -0.1
	for _, st := range scores {
		for _, ct := range content {
			done := Rank([]Candidate{{Start: 0, End: 20, Score: st}},
				[]Judgement{{Index: 0, Score: ct, Complete: true, Reason: "lands"}})[0]
			trails := Rank([]Candidate{{Start: 0, End: 20, Score: st}},
				[]Judgement{{Index: 0, Score: ct, Complete: false, Reason: "ends mid-sentence"}})[0]

			if trails.Confidence != Vetoed {
				t.Fatalf("structure %.2f content %d: confidence = %q, want %q",
					st, ct, trails.Confidence, Vetoed)
			}
			if done.Confidence == Vetoed {
				t.Fatalf("structure %.2f content %d: a completing arc must never be vetoed", st, ct)
			}
			worstComplete = minF(worstComplete, done.Final)
			bestVetoed = maxOf(bestVetoed, trails.Final)
		}
	}

	// The ordering guarantee is enforced by Rank's sort, so assert it where a
	// caller would actually see it: mix every case into one slice and require
	// every complete moment to come out ahead of every vetoed one.
	var cands []Candidate
	var verdicts []Judgement
	for i, st := range scores {
		cands = append(cands, Candidate{Start: float64(i * 100), End: float64(i*100 + 20), Score: st})
		verdicts = append(verdicts, Judgement{Index: len(verdicts), Score: 99, Complete: false, Reason: "trails off"})
		cands = append(cands, Candidate{Start: float64(i*100 + 30), End: float64(i*100 + 50), Score: st})
		verdicts = append(verdicts, Judgement{Index: len(verdicts), Score: 5, Complete: true, Reason: "lands"})
	}
	ranked := Rank(cands, verdicts)
	seenVetoed := false
	for _, r := range ranked {
		if r.Confidence == Vetoed {
			seenVetoed = true
			continue
		}
		if seenVetoed {
			t.Fatalf("a complete moment (final %.3f) ranked BELOW a vetoed one — the veto is only a discount", r.Final)
		}
	}
	if !seenVetoed {
		t.Fatal("fixture produced no vetoed moments; the test proves nothing")
	}
	t.Logf("worst complete final=%.3f, best vetoed final=%.3f (ordering is by veto first, not by score)",
		worstComplete, bestVetoed)
}

func maxOf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func TestRank_PartialVerdictsDegradePerCandidate(t *testing.T) {
	cands := []Candidate{
		{Start: 0, End: 20, Score: 0.70},
		{Start: 30, End: 50, Score: 0.60},
	}
	ranked := Rank(cands, []Judgement{{Index: 0, Score: 70, Complete: true, Reason: "ok"}})
	var judged, unjudged int
	for _, r := range ranked {
		switch r.Confidence {
		case Conclusion:
			judged++
		case CandidateOnly:
			unjudged++
		}
	}
	if judged != 1 || unjudged != 1 {
		t.Errorf("got %d concluded / %d candidate-only, want 1/1", judged, unjudged)
	}
}

func TestRank_IgnoresOutOfRangeIndices(t *testing.T) {
	cands := []Candidate{{Start: 0, End: 20, Score: 0.70}}
	ranked := Rank(cands, []Judgement{{Index: 99, Score: 90, Complete: true}})
	if ranked[0].Confidence != CandidateOnly {
		t.Errorf("a verdict for a nonexistent candidate must be ignored, got %q", ranked[0].Confidence)
	}
}

// --- Judge plumbing -------------------------------------------------------

func TestParseJudgements_ToleratesMessyModelOutput(t *testing.T) {
	raw := "```json\n" +
		`{"index":0,"score":72,"complete":true,"reason":"clear payoff"}` + "\n" +
		"not json at all\n" +
		`  {"index":1,"score":10,"complete":false,"reason":"trails off"},` + "\n" +
		`{"index":2,"score":500,"complete":true,"reason":"out of range"}` + "\n" +
		"```"
	got := ParseJudgements(raw)
	if len(got) != 2 {
		t.Fatalf("ParseJudgements returned %d verdicts, want 2 (the out-of-range one is rejected): %+v", len(got), got)
	}
	if got[0].Index != 0 || got[0].Score != 72 || !got[0].Complete {
		t.Errorf("first verdict = %+v", got[0])
	}
	if got[1].Index != 1 || got[1].Score != 10 || got[1].Complete {
		t.Errorf("second verdict = %+v", got[1])
	}
}

func TestPrompt_CarriesTheRubricAndEveryCandidate(t *testing.T) {
	cands := []Candidate{
		{Start: 0, End: 20, Text: "first candidate text"},
		{Start: 30, End: 50, Text: "second candidate text"},
	}
	p := Prompt(cands)
	for _, want := range []string{"Berger", "complete", "[0]", "[1]", "first candidate text", "second candidate text"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

// TestJudgeFunc_ErrorDegradesToStructureOnly proves the documented degrade path:
// a failing judge must not lose the candidates.
func TestJudgeFunc_ErrorDegradesToStructureOnly(t *testing.T) {
	var jf JudgeFunc = func(context.Context, []Candidate) ([]Judgement, error) {
		return nil, errors.New("no api key")
	}
	cands := Find(story(), Options{MinDuration: 10, MaxDuration: 40})
	verdicts, err := jf(context.Background(), cands)
	if err == nil {
		t.Fatal("fixture judge should have failed")
	}
	ranked := Rank(cands, verdicts)
	if len(ranked) != len(cands) {
		t.Fatalf("a failed judge lost candidates: %d -> %d", len(cands), len(ranked))
	}
	for _, r := range ranked {
		if r.Confidence != CandidateOnly {
			t.Errorf("after a judge error, confidence = %q, want %q", r.Confidence, CandidateOnly)
		}
	}
}

func TestRank_KeepsEachMomentsSourceThroughReorderingAndTruncation(t *testing.T) {
	// Two transcripts whose candidate windows are IDENTICAL — the ordinary case
	// when several clips are cut from the same stream, and the one that broke.
	// becky-moment used to drop the source and recover it afterwards by matching
	// Start/End floats, first match wins, so every moment here was labelled
	// alpha.srt and becky-hits cut the wrong footage. It rendered fine.
	cands := []Candidate{
		{Source: "alpha.srt", Start: 0, End: 20, Score: 0.40, Text: "alpha one"},
		{Source: "zulu.srt", Start: 0, End: 20, Score: 0.90, Text: "zulu one"},
		{Source: "alpha.srt", Start: 30, End: 50, Score: 0.20, Text: "alpha two"},
		{Source: "zulu.srt", Start: 30, End: 50, Score: 0.70, Text: "zulu two"},
	}
	ranked := Rank(cands, nil)
	if len(ranked) != len(cands) {
		t.Fatalf("Rank returned %d moments, want %d", len(ranked), len(cands))
	}
	// Assert values: the text and the source must still belong to each other.
	for _, r := range ranked {
		wantSrc := "alpha.srt"
		if strings.HasPrefix(r.Text, "zulu") {
			wantSrc = "zulu.srt"
		}
		if r.Source != wantSrc {
			t.Errorf("moment %q came back sourced to %q, want %q — a clip cut from this points at the wrong video",
				r.Text, r.Source, wantSrc)
		}
	}
	// And the highest-ranked moment really is zulu's, so truncation to --top N
	// cannot hand back alpha's window under zulu's score.
	if ranked[0].Source != "zulu.srt" || ranked[0].Text != "zulu one" {
		t.Errorf("top moment = %q from %q, want \"zulu one\" from zulu.srt", ranked[0].Text, ranked[0].Source)
	}
}
