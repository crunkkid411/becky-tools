package main

import "testing"

func TestSentencizeCutsAtSentenceEnds(t *testing.T) {
	ws := func(spk string, start float64, text ...string) []Word {
		var out []Word
		for i, x := range text {
			s := start + float64(i)*0.3
			out = append(out, Word{Word: x, Start: s, End: s + 0.25, Speaker: spk})
		}
		return out
	}
	words := append(ws("S0", 0, "Hi,", "I'm", "Hair", "Jordan.", "Mr.", "Smith", "said", "so"),
		ws("S1", 2.5, "Really?", "No")...)
	words = append(words, ws("S1", 6, "way")...)           // long pause: new sentence
	words = append(words, ws("S1", 9, "Thank", "you.")...) // dropped by VAD
	got := sentencize(words, []DroppedSegment{{Start: 8.9, End: 9.7}})
	want := []string{"Hi, I'm Hair Jordan.", "Mr. Smith said so", "Really?", "No", "way"}
	if len(got) != len(want) {
		t.Fatalf("got %d sentences %+v", len(got), got)
	}
	for i, w := range want {
		if got[i].Text != w {
			t.Errorf("sentence %d = %q, want %q", i, got[i].Text, w)
		}
	}
	if got[1].Speaker != "S0" || got[2].Speaker != "S1" {
		t.Errorf("speaker change must cut: %+v", got)
	}
	if got[0].Start != 0 || got[0].End != 1.15 {
		t.Errorf("times: %v-%v", got[0].Start, got[0].End)
	}
}

func TestEndsSentence(t *testing.T) {
	for w, want := range map[string]bool{"done.": true, "why?": true, `"stop!"`: true, "Dr.": false, "J.": false, "3.5": false, "so": false} {
		if endsSentence(w) != want {
			t.Errorf("endsSentence(%q) != %v", w, want)
		}
	}
}
