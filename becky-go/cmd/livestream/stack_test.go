package main

import "testing"

// System One settles a Qwen/Gemma split only when it is sure and sides with one
// of them; otherwise the line stays unsure and the note names all three calls.
func TestConcludeStackSettlesSplits(t *testing.T) {
	ds := []Decision{
		{ID: 0, Label: "narrative", Topic: 1, Keep: true, Confidence: 60}, // qwen keep, gemma cut, s1 sure cut
		{ID: 1, Label: "narrative", Topic: 1, Keep: true, Confidence: 60}, // qwen keep, gemma cut, s1 unsure
		{ID: 2, Label: "narrative", Topic: 1, Keep: true, Confidence: 95}, // all agree
	}
	reviews := map[int]Review{
		0: {Model: "gemma4", Label: "break", Keep: false, Confidence: 80},
		1: {Model: "gemma4", Label: "break", Keep: false, Confidence: 80},
	}
	s1 := []Decision{{Said: false, Confidence: 97}, {Said: false, Confidence: 55}, {Said: true, Confidence: 99}}
	out := concludeStack(ds, reviews, s1, "qwen3.5")
	if out[0].Keep || out[0].Unsure {
		t.Errorf("line 0: keep=%v unsure=%v, want cut and settled (%s)", out[0].Keep, out[0].Unsure, out[0].Note)
	}
	if !out[1].Unsure || !out[1].Keep {
		t.Errorf("line 1: keep=%v unsure=%v, want kept and unsure", out[1].Keep, out[1].Unsure)
	}
	if want := " - systemone said cut (55% sure)"; len(out[1].Note) < len(want) || out[1].Note[len(out[1].Note)-len(want):] != want {
		t.Errorf("line 1 note %q does not say what System One said", out[1].Note)
	}
	if !out[2].Keep || out[2].Unsure {
		t.Errorf("line 2: keep=%v unsure=%v, want a plain keep", out[2].Keep, out[2].Unsure)
	}
}
