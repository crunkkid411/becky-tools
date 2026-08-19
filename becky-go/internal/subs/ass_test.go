package subs

import (
	"strings"
	"testing"
)

func TestBuildWithWordsMatchesBuild(t *testing.T) {
	// BuildWithWords must produce the exact same cue Start/End/Text as Build —
	// they now share ONE implementation (buildFromChunks); this catches any
	// future drift between the two return shapes.
	words := []Word{w("hello", 0.0, 0.4), w("world", 0.5, 0.9), w("again", 1.6, 2.0)}
	opt := DefaultOptions()
	opt.GapSeconds = 0.5

	plain := Build([]Segment{{Source: "clip", Start: 0, End: 3, Words: words}}, opt)
	withWords := BuildWithWords([]Segment{{Source: "clip", Start: 0, End: 3, Words: words}}, opt)

	if len(plain) != len(withWords) {
		t.Fatalf("Build gave %d cues, BuildWithWords gave %d", len(plain), len(withWords))
	}
	for i := range plain {
		if plain[i] != withWords[i].Cue {
			t.Errorf("cue %d: Build=%+v BuildWithWords=%+v", i, plain[i], withWords[i].Cue)
		}
	}
	if len(withWords[0].Words) != 2 || withWords[0].Words[0].Word != "hello" {
		t.Errorf("cue 0 words = %+v, want [hello world]", withWords[0].Words)
	}
	if len(withWords[1].Words) != 1 || withWords[1].Words[0].Word != "again" {
		t.Errorf("cue 1 words = %+v, want [again]", withWords[1].Words)
	}
}

func TestJordanLinesStacksAndColoursOneWord(t *testing.T) {
	words := []Word{w("oh", 0, 0.1), w("you", 0.1, 0.3), w("guys", 0.3, 0.6), w("really", 0.7, 1.0)}
	got := jordanLines(words, 2, "&HFFFF00&") // colour "guys" (index 2)

	want := `OH YOU {\c&HFFFF00&}GUYS{\c&HFFFFFF&}\NREALLY`
	if got != want {
		t.Errorf("jordanLines =\n%q\nwant\n%q", got, want)
	}
}

func TestJordanLinesNoEmphasisStaysPlain(t *testing.T) {
	words := []Word{w("no", 0, 0.2), w("event", 0.2, 0.5), w("here", 0.5, 0.8)}
	got := jordanLines(words, -1, "&HFFFF00&")
	if got != "NO EVENT HERE" {
		t.Errorf("jordanLines with no emphasis = %q, want plain text with no colour codes", got)
	}
}

func TestPlainStackedTextStripsPunctuationKeepsExclaim(t *testing.T) {
	words := []Word{w("put,", 0, 0.2), w("the", 0.2, 0.4), w("burger", 0.4, 0.6), w("down!", 0.6, 0.9)}
	got := PlainStackedText(words)
	if got != "PUT THE BURGER DOWN!" {
		t.Errorf("PlainStackedText = %q, want %q", got, "PUT THE BURGER DOWN!")
	}
}

func TestWriteASSHasStyleAndDialogue(t *testing.T) {
	cues := BuildWithWords([]Segment{{Source: "clip", Start: 0, End: 2,
		Words: []Word{w("hi", 0.0, 0.3), w("there", 0.4, 0.8)}}}, DefaultOptions())

	var buf strings.Builder
	if err := WriteASS(&buf, cues, []int{1}, DefaultJordanStyle(1920), 1080, 1920); err != nil {
		t.Fatalf("WriteASS error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "PlayResX: 1080") || !strings.Contains(out, "PlayResY: 1920") {
		t.Errorf("ASS output missing PlayRes matching the output frame:\n%s", out)
	}
	if !strings.Contains(out, "Style: Jordan,") {
		t.Errorf("ASS output missing the Jordan style line:\n%s", out)
	}
	if !strings.Contains(out, "Dialogue: 0,") {
		t.Errorf("ASS output missing a Dialogue line:\n%s", out)
	}
	if !strings.Contains(out, `{\c&HFFFF00&}THERE{\c&HFFFFFF&}`) {
		t.Errorf("ASS output missing the coloured emphasis word:\n%s", out)
	}
}
