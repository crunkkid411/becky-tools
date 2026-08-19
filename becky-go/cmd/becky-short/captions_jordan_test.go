package main

import (
	"testing"

	"becky-go/internal/subs"
)

// The audio says WHERE the stress landed; it cannot say which word carries the
// meaning. English routinely puts a loudness peak on a function word next to
// the real one, and the first render on his footage coloured
// "ON THE ON >>THE<< BEAUTY MIRROR" - technically the nearest word to the spike,
// and it looks like a bug. Every coloured word in Jordan's own edit is a content
// word (GUYS, SORRY, REALLY, OKAY, NO, YES, BURGER DOWN, switch).
func TestNearestWord_SkipsWordsNoViewerReadsAsEmphasis(t *testing.T) {
	words := []subs.Word{
		{Word: "on", Start: 0.0, End: 0.2},
		{Word: "the", Start: 0.2, End: 0.4},
		{Word: "beauty", Start: 0.4, End: 0.9},
		{Word: "mirror", Start: 0.9, End: 1.3},
	}
	// The spike lands squarely on "the".
	if got := nearestWord(words, 0.3); words[got].Word != "beauty" {
		t.Errorf("emphasis = %q, want \"beauty\": the nearest word was a function word", words[got].Word)
	}
	// A spike on a content word still picks that word, not a neighbour.
	if got := nearestWord(words, 1.1); words[got].Word != "mirror" {
		t.Errorf("emphasis = %q, want \"mirror\"", words[got].Word)
	}
}

// Colouring something beats a block that silently loses its emphasis.
func TestNearestWord_FallsBackWhenEveryWordIsAFunctionWord(t *testing.T) {
	words := []subs.Word{
		{Word: "on", Start: 0.0, End: 0.2},
		{Word: "the", Start: 0.2, End: 0.4},
	}
	got := nearestWord(words, 0.3)
	if got < 0 || got >= len(words) {
		t.Fatalf("index %d out of range", got)
	}
	if words[got].Word != "the" {
		t.Errorf("fallback = %q, want the nearest word \"the\"", words[got].Word)
	}
}

// Pronouns are kept: he colours "thank YOU".
func TestStressable_KeepsPronounsAndStripsPunctuation(t *testing.T) {
	for _, w := range []string{"you", "YOU!", "no", "yes", "beauty"} {
		if !stressable(subs.Word{Word: w}) {
			t.Errorf("%q should be colourable", w)
		}
	}
	for _, w := range []string{"the", "THE,", "of", "just", "is", "  a  "} {
		if stressable(subs.Word{Word: w}) {
			t.Errorf("%q should not be colourable", w)
		}
	}
}
