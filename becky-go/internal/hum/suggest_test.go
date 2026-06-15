package hum

import (
	"strings"
	"testing"
)

func TestSuggest_InKeyNoteKept(t *testing.T) {
	// C (60) in C major is in-key: no suggestion, no review.
	notes := []Note{{I: 0, Midi: 60, Confidence: 0.9}}
	corr := Suggest(notes, "C", tonicTriad("C"), DefaultSuggestOptions())
	if notes[0].Suggestion != nil || notes[0].NeedsReview || !notes[0].InKey {
		t.Errorf("in-key C should be kept: %+v", notes[0])
	}
	if len(corr) != 0 {
		t.Errorf("no correction expected for in-key note, got %d", len(corr))
	}
}

func TestSuggest_OutOfKeyGetsSuggestion(t *testing.T) {
	// C# (61) in C major is out-of-key. becky should suggest a real in-key target
	// (C=60 or D=62), flag it, and log a correction — never silently keep C#.
	notes := []Note{{I: 0, Midi: 61, Confidence: 0.6}}
	corr := Suggest(notes, "C", tonicTriad("C"), DefaultSuggestOptions())
	if notes[0].InKey {
		t.Fatal("C# in C major must not be in-key")
	}
	if notes[0].Suggestion == nil {
		t.Fatal("out-of-key note must carry a suggestion")
	}
	pc := ((notes[0].Suggestion.Midi % 12) + 12) % 12
	if pc != 0 && pc != 2 {
		t.Errorf("suggested pitch class %d not a C-major neighbor of C# (want C=0 or D=2)", pc)
	}
	if notes[0].Suggestion.Reason == "" {
		t.Error("suggestion must be explainable (non-empty reason)")
	}
	if len(corr) != 1 || corr[0].Field != "note.midi" || corr[0].Corrected != nil {
		t.Errorf("expected one corrections-log seed with nil Corrected, got %+v", corr)
	}
}

func TestSuggest_OffKeyNoteIsCorroboratedOrFlagged(t *testing.T) {
	// An off-key note must never be left silent: it is either corroborated
	// (>=2 signals agree -> "corroborated" reason) or flagged for review.
	notes := []Note{
		{I: 0, Midi: 59, Confidence: 0.9}, // B (leading tone, in C major)
		{I: 1, Midi: 61, Confidence: 0.6}, // C# off-key
	}
	Suggest(notes, "C", tonicTriad("C"), DefaultSuggestOptions())
	s := notes[1].Suggestion
	if s == nil {
		t.Fatal("expected a suggestion for the off-key note")
	}
	if !strings.Contains(s.Reason, "corroborated") && !notes[1].NeedsReview {
		t.Errorf("off-key note must be corroborated-or-flagged; reason=%q review=%v", s.Reason, notes[1].NeedsReview)
	}
}

func TestSuggest_AmbiguousRequestsReview(t *testing.T) {
	// With a wide ambiguous band, an off-tone note lands in the ambiguous zone and
	// must request review and report a non-zero distance to the scale.
	notes := []Note{{I: 0, Midi: 61, Confidence: 0.5}} // C# wrt C/D
	opt := SuggestOptions{OnToneCents: 25, AmbiguousCents: 150}
	Suggest(notes, "C", tonicTriad("C"), opt)
	if !notes[0].NeedsReview {
		t.Error("ambiguous note should request review")
	}
	if notes[0].DistanceCents == 0 {
		t.Error("ambiguous note should report a non-zero distance to scale")
	}
}

func TestSuggest_Deterministic(t *testing.T) {
	mk := func() []Note {
		return []Note{
			{I: 0, Midi: 60, Confidence: 0.9},
			{I: 1, Midi: 61, Confidence: 0.6},
			{I: 2, Midi: 66, Confidence: 0.5},
		}
	}
	a, b := mk(), mk()
	ca := Suggest(a, "C", tonicTriad("C"), DefaultSuggestOptions())
	cb := Suggest(b, "C", tonicTriad("C"), DefaultSuggestOptions())
	if len(ca) != len(cb) {
		t.Fatalf("non-deterministic correction count %d vs %d", len(ca), len(cb))
	}
	for i := range a {
		if a[i].Midi != b[i].Midi || a[i].NeedsReview != b[i].NeedsReview {
			t.Errorf("note %d non-deterministic", i)
		}
	}
}

func TestSuggest_NeverSuggestsOutOfKey(t *testing.T) {
	// Every suggestion becky makes must itself be in the detected key.
	scalePCs := ScaleTonesPC("F#m")
	inKey := map[int]bool{}
	for _, pc := range scalePCs {
		inKey[pc] = true
	}
	notes := []Note{{I: 0, Midi: 67, Confidence: 0.5}, {I: 1, Midi: 70, Confidence: 0.5}}
	Suggest(notes, "F#m", tonicTriad("F#m"), DefaultSuggestOptions())
	for _, n := range notes {
		if n.Suggestion != nil {
			pc := ((n.Suggestion.Midi % 12) + 12) % 12
			if !inKey[pc] {
				t.Errorf("suggested pitch class %d is not in F#m", pc)
			}
		}
	}
}
