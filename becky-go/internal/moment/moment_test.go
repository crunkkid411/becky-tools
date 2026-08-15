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
	// A transcript whose gaps are mostly 0.2s with a few 2.0s breaks. The p75 of
	// {0.2,0.2,0.2,0.2,2.0,2.0} is 2.0, which clamps to maxThoughtGap (2.5 -> 2.0
	// stays). The point of the assertion is that the value TRACKS the data.
	tight := []Segment{
		seg(0, 1, "a"), seg(1.2, 2, "b"), seg(2.2, 3, "c"),
		seg(3.2, 4, "d"), seg(4.2, 5, "e"), seg(5.2, 6, "f"),
	}
	got := AutoThoughtGap(tight)
	if got != minThoughtGap {
		t.Errorf("AutoThoughtGap(all-tight) = %v, want the %v floor", got, minThoughtGap)
	}

	loose := []Segment{
		seg(0, 1, "a"), seg(2, 3, "b"), seg(4, 5, "c"),
		seg(6, 7, "d"), seg(8, 9, "e"), seg(10, 11, "f"),
	}
	got = AutoThoughtGap(loose)
	if got != 1.0 {
		t.Errorf("AutoThoughtGap(all 1.0s gaps) = %v, want 1.0", got)
	}

	// Regression for the constant-does-not-transfer lesson (STATE-OF-MASTER
	// 2026-07-19): the two transcripts above must NOT produce the same threshold.
	if AutoThoughtGap(tight) == AutoThoughtGap(loose) {
		t.Error("AutoThoughtGap returned the same value for tight and loose transcripts — it is behaving like a constant")
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
	cands := []Candidate{{Start: 0, End: 20, Score: 0.85}}
	ranked := Rank(cands, []Judgement{{Index: 0, Score: 90, Complete: false, Reason: "ends mid-sentence"}})
	if ranked[0].Confidence != Disputed {
		t.Fatalf("confidence = %q, want %q for an incomplete arc", ranked[0].Confidence, Disputed)
	}
	// A 90/99 clip that does not complete must rank BELOW a modest complete one.
	complete := Rank([]Candidate{{Start: 0, End: 20, Score: 0.55}},
		[]Judgement{{Index: 0, Score: 55, Complete: true, Reason: "fine"}})
	if ranked[0].Final >= complete[0].Final {
		t.Errorf("incomplete clip (%.3f) must rank below a complete one (%.3f)", ranked[0].Final, complete[0].Final)
	}
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
