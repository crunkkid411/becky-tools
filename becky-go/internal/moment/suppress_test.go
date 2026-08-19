package moment

import "testing"

// The bug: on test-for-clips.mp4 the structural top ten was ten windows over
// ONE 68-second stretch of a five-minute video (130.4-198.2s). Every window was
// a well-formed thought, so no score could reject them - they were individually
// correct and collectively ten renders of the same story.
func TestDistinct_DropsRecutsOfTheSameMoment(t *testing.T) {
	// The real top-five windows, verbatim from the measurement.
	cands := []Candidate{
		{Source: "a.srt", Start: 146.2, End: 174.6, Score: 0.9815},
		{Source: "a.srt", Start: 217.7, End: 242.9, Score: 0.9493},
		{Source: "a.srt", Start: 130.4, End: 174.6, Score: 0.9377}, // contains #1
		{Source: "a.srt", Start: 146.2, End: 171.8, Score: 0.9300}, // ~identical to #1
		{Source: "a.srt", Start: 226.1, End: 242.9, Score: 0.9280}, // inside #2
	}

	got := Distinct(cands, DefaultMaxOverlap)

	if len(got) != 2 {
		t.Fatalf("kept %d windows, want 2 (the two genuinely distinct moments); got %v", len(got), spans(got))
	}
	// The BEST version of each moment survives, not merely the first seen.
	want := [][2]float64{{146.2, 174.6}, {217.7, 242.9}}
	for i, w := range want {
		if got[i].Start != w[0] || got[i].End != w[1] {
			t.Errorf("window %d = %.1f-%.1f, want %.1f-%.1f", i, got[i].Start, got[i].End, w[0], w[1])
		}
	}
}

// Two files that happen to share a timecode are not the same footage. Labelling
// moments with the wrong source video has already shipped once; suppression
// must not reintroduce it by comparing timestamps across files.
func TestDistinct_NeverSuppressesAcrossSources(t *testing.T) {
	cands := []Candidate{
		{Source: "a.srt", Start: 10, End: 40, Score: 0.9},
		{Source: "b.srt", Start: 10, End: 40, Score: 0.8},
	}
	got := Distinct(cands, DefaultMaxOverlap)
	if len(got) != 2 {
		t.Fatalf("kept %d, want both: identical timecodes in DIFFERENT files are different moments", len(got))
	}
}

// A window that merely touches another is not a repeat. 146.2-174.6 against
// 160.6-198.2 shares 14.0s of a 28.4s clip = 0.49, just under the threshold,
// and both survived in the real run - that is the intended behaviour, so pin it.
func TestDistinct_KeepsPartialOverlapBelowThreshold(t *testing.T) {
	cands := []Candidate{
		{Source: "a.srt", Start: 146.2, End: 174.6, Score: 0.98},
		{Source: "a.srt", Start: 160.6, End: 198.2, Score: 0.91},
	}
	if f := overlapFrac("a.srt", 146.2, 174.6, "a.srt", 160.6, 198.2); f <= 0.45 || f >= 0.5 {
		t.Fatalf("overlap fraction = %.3f, want just under the 0.5 threshold", f)
	}
	if got := Distinct(cands, DefaultMaxOverlap); len(got) != 2 {
		t.Fatalf("kept %d, want 2: a 49%% overlap is a different moment", len(got))
	}
}

// Overlap is measured against the SHORTER window, because IoU flatters a long
// window paired with a short one and let re-cuts through.
func TestOverlapFrac_MeasuresTheShorterWindow(t *testing.T) {
	// 16.8s wholly inside a 25.2s window: the short clip is 100% a repeat.
	if got := overlapFrac("a", 217.7, 242.9, "a", 226.1, 242.9); got != 1 {
		t.Errorf("contained window = %.3f, want 1.000 (all of it is a repeat)", got)
	}
	// IoU would have scored that same pair 16.8/25.2 = 0.667 - still above 0.5
	// here, but the gap widens as the outer window grows, which is how re-cuts
	// escaped. Pin the divergence so nobody "simplifies" this back to IoU.
	if got := overlapFrac("a", 100, 400, "a", 100, 130); got != 1 {
		t.Errorf("short clip inside a long one = %.3f, want 1.000; IoU would say 0.100", got)
	}
}

func TestDistinct_ZeroThresholdDisablesSuppression(t *testing.T) {
	cands := []Candidate{
		{Source: "a", Start: 10, End: 40, Score: 0.9},
		{Source: "a", Start: 10, End: 40, Score: 0.8},
	}
	if got := Distinct(cands, 0); len(got) != 2 {
		t.Fatalf("kept %d with --max-overlap 0, want all %d untouched", len(got), len(cands))
	}
}

// Rank has already applied the veto by the time DistinctRanked runs, so
// suppression must respect the order it was handed rather than re-sorting by
// score - otherwise a vetoed trailing-off clip could suppress the complete one.
func TestDistinctRanked_KeepsTheOrderItWasGiven(t *testing.T) {
	ranked := []Ranked{
		{Candidate: Candidate{Source: "a", Start: 10, End: 40, Score: 0.4}, Final: 0.4, Confidence: Conclusion},
		{Candidate: Candidate{Source: "a", Start: 12, End: 41, Score: 0.9}, Final: 0.9, Confidence: Vetoed},
	}
	got := DistinctRanked(ranked, DefaultMaxOverlap)
	if len(got) != 1 {
		t.Fatalf("kept %d, want 1", len(got))
	}
	if got[0].Confidence == Vetoed {
		t.Error("the VETOED re-cut survived and suppressed the complete moment")
	}
}

func spans(c []Candidate) [][2]float64 {
	out := make([][2]float64, len(c))
	for i, x := range c {
		out[i] = [2]float64{x.Start, x.End}
	}
	return out
}
