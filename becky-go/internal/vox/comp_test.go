package vox

import "testing"

// ph builds a phrase with given tightness/stability (start/end fixed for one phrase).
func ph(tight, stable float64) Phrase {
	return Phrase{StartMs: 0, EndMs: 1000, TimingTightness: tight, PitchStability: stable}
}

func TestComp_PicksBestTakePerPhrase(t *testing.T) {
	// Take 1 is tighter+more stable on the one phrase -> it wins; take 0 is runner-up.
	takes := []TakeAnalysis{
		{Name: "t0", Phrases: []Phrase{ph(0.5, 0.5)}},
		{Name: "t1", Phrases: []Phrase{ph(0.9, 0.9)}},
	}
	res := Comp(takes, "balanced")
	if len(res.Choices) != 1 {
		t.Fatalf("got %d choices, want 1", len(res.Choices))
	}
	if res.Choices[0].ChosenTake != 1 {
		t.Errorf("chosen take = %d, want 1 (the tighter/stabler)", res.Choices[0].ChosenTake)
	}
	if res.Choices[0].RunnerUp != 0 {
		t.Errorf("runner-up = %d, want 0", res.Choices[0].RunnerUp)
	}
}

func TestComp_MetricWeightingShiftsWinner(t *testing.T) {
	// t0 is tight but pitchy; t1 is loose but stable. "pitch" metric should pick t1,
	// "timing" metric should pick t0 — same takes, different declared metric.
	takes := []TakeAnalysis{
		{Name: "t0", Phrases: []Phrase{ph(0.9, 0.2)}},
		{Name: "t1", Phrases: []Phrase{ph(0.2, 0.9)}},
	}
	if Comp(takes, "pitch").Choices[0].ChosenTake != 1 {
		t.Error("pitch metric should pick the more stable take (t1)")
	}
	if Comp(takes, "timing").Choices[0].ChosenTake != 0 {
		t.Error("timing metric should pick the tighter take (t0)")
	}
}

func TestComp_TieBreaksToLowestIndex(t *testing.T) {
	takes := []TakeAnalysis{
		{Name: "t0", Phrases: []Phrase{ph(0.7, 0.7)}},
		{Name: "t1", Phrases: []Phrase{ph(0.7, 0.7)}},
	}
	if Comp(takes, "balanced").Choices[0].ChosenTake != 0 {
		t.Error("a tie should break to the lowest take index (0)")
	}
}

func TestComp_NoTakesDegrades(t *testing.T) {
	res := Comp(nil, "balanced")
	if !res.Degraded || res.Reason == "" {
		t.Errorf("no takes should degrade with a reason, got %+v", res)
	}
}

func TestComp_Deterministic(t *testing.T) {
	takes := []TakeAnalysis{
		{Name: "t0", Phrases: []Phrase{ph(0.5, 0.6), ph(0.8, 0.4)}},
		{Name: "t1", Phrases: []Phrase{ph(0.9, 0.5), ph(0.3, 0.9)}},
	}
	a := Comp(takes, "balanced")
	b := Comp(takes, "balanced")
	if len(a.Choices) != len(b.Choices) {
		t.Fatal("comp not deterministic in length")
	}
	for i := range a.Choices {
		if a.Choices[i] != b.Choices[i] {
			t.Errorf("phrase %d non-deterministic: %+v vs %+v", i, a.Choices[i], b.Choices[i])
		}
	}
}

func TestComp_RecordsRunnerUpScore(t *testing.T) {
	takes := []TakeAnalysis{
		{Name: "t0", Phrases: []Phrase{ph(0.4, 0.4)}},
		{Name: "t1", Phrases: []Phrase{ph(0.9, 0.9)}},
	}
	c := Comp(takes, "balanced").Choices[0]
	if c.Score <= c.RunnerScore {
		t.Errorf("winner score %.3f should exceed runner-up %.3f", c.Score, c.RunnerScore)
	}
}
