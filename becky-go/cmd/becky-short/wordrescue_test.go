package main

import (
	"testing"

	"becky-go/internal/subs"
)

func w(text string, start, end float64) subs.Word {
	return subs.Word{Word: text, Start: start, End: end}
}

// The measured failure: an absolute silence threshold on quiet footage cut
// 6.5 seconds of speech out of a 30-second window that had no silence in it.
// A span that carries a word is not silence, whatever the level says.
func TestRescueWordsPutsBackSpeechTheThresholdCut(t *testing.T) {
	// The threshold kept only two short stretches...
	spans := []keepSpan{{In: 2, Out: 3}, {In: 20, Out: 21}}
	// ...but these words were spoken across the window.
	words := []subs.Word{
		w("uh", 2.1, 2.4), // already kept
		w("dont", 8.0, 8.3),
		w("fall", 8.4, 8.9),
		w("okay", 15.0, 15.4),
		w("whatever", 20.2, 20.8), // already kept
	}
	got, rescued := rescueWords(spans, words, 0, 30, wordRescuePad)
	if rescued != 3 {
		t.Fatalf("rescued %d word(s), want 3 (dont, fall, okay)", rescued)
	}
	// Every spoken word must now be inside a kept span, with its ends intact.
	for _, x := range words {
		in := false
		for _, s := range got {
			if x.Start >= s.In && x.End <= s.Out {
				in = true
				break
			}
		}
		if !in {
			t.Errorf("word %q (%.1f-%.1f) is still cut: spans=%+v", x.Word, x.Start, x.End, got)
		}
	}
	// Adjacent words must merge, not stack up as slivers.
	if len(got) != 4 {
		t.Errorf("got %d spans %+v, want 4 (dont+fall merge into one)", len(got), got)
	}
	// And it must not simply keep everything — the real silence stays cut.
	if d := spansDuration(got); d > 6 {
		t.Errorf("kept %.1fs of 30s; rescuing words should not turn into keeping the whole window", d)
	}
}

func TestRescueWordsLeavesAnAgreeingPlanAlone(t *testing.T) {
	spans := []keepSpan{{In: 1, Out: 5}, {In: 10, Out: 14}}
	words := []subs.Word{w("a", 1.5, 1.8), w("b", 4.0, 4.5), w("c", 11.0, 11.4)}
	got, rescued := rescueWords(spans, words, 0, 20, wordRescuePad)
	if rescued != 0 {
		t.Errorf("rescued %d, want 0 — the plan already covers every word", rescued)
	}
	if len(got) != 2 || got[0] != spans[0] || got[1] != spans[1] {
		t.Errorf("spans changed when nothing needed rescuing: %+v", got)
	}
}

// A boundary through the MIDDLE of a word is the clipped-stub case: the word is
// half there, which sounds worse than either keeping or cutting it cleanly.
func TestRescueWordsFixesAWordClippedByASpanBoundary(t *testing.T) {
	spans := []keepSpan{{In: 0, Out: 5.2}}
	words := []subs.Word{w("everything", 5.0, 5.9)}
	got, rescued := rescueWords(spans, words, 0, 10, wordRescuePad)
	if rescued != 1 {
		t.Fatalf("rescued %d, want 1 — the word is cut in half at 5.2", rescued)
	}
	if len(got) != 1 || got[0].Out < 5.9 {
		t.Errorf("span %+v does not contain the whole word (ends at 5.9)", got)
	}
}

func TestRescueWordsIgnoresWordsOutsideTheWindow(t *testing.T) {
	spans := []keepSpan{{In: 10, Out: 12}}
	words := []subs.Word{w("before", 1, 2), w("after", 50, 51), w("inside", 15, 15.5)}
	got, rescued := rescueWords(spans, words, 10, 20, wordRescuePad)
	if rescued != 1 {
		t.Fatalf("rescued %d, want 1 (only the word inside the window)", rescued)
	}
	for _, s := range got {
		if s.In < 10 || s.Out > 20 {
			t.Errorf("span %+v escaped the window [10,20]", s)
		}
	}
}

func TestMergeSpansUnionsOverlapsAndTouching(t *testing.T) {
	got := mergeSpans([]keepSpan{{In: 5, Out: 7}, {In: 0, Out: 2}, {In: 1.5, Out: 3}, {In: 7, Out: 8}})
	want := []keepSpan{{In: 0, Out: 3}, {In: 5, Out: 8}}
	if len(got) != len(want) {
		t.Fatalf("mergeSpans = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("span %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if mergeSpans(nil) != nil {
		t.Error("mergeSpans(nil) should be nil")
	}
}
