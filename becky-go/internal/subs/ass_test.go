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

func TestJordanLinesColoursTheCurrentWordAndBreaksAtMostOnce(t *testing.T) {
	words := []Word{w("oh", 0, 0.1), w("you", 0.1, 0.3), w("guys", 0.3, 0.6), w("really", 0.7, 1.0)}
	got := jordanLines(words, 2, "&HFFFF00&") // light "guys" (index 2)

	// 18 chars is over the measured 15-char column, so it breaks ONCE. Of the
	// two evenest splits (6/11 and 11/6) the EARLIER wins, which puts the wider
	// line at the BOTTOM - his own blocks break exactly that way: AREN'T /
	// SUPPOSED, WELL LET / ME FINISH THAT.
	want := `OH YOU\N{\c&HFFFF00&}GUYS{\c&HFFFFFF&} REALLY`
	if got != want {
		t.Errorf("jordanLines =\n%q\nwant\n%q", got, want)
	}
	if n := strings.Count(got, `\N`); n > 1 {
		t.Errorf("jordanLines produced %d breaks = %d lines; Jordan never uses three: %q", n, n+1, got)
	}
}

// A block that fits the measured column must stay on ONE line — the break is
// for text that genuinely does not fit, not a decoration applied to everything.
func TestJordanLinesKeepsAShortBlockOnOneLine(t *testing.T) {
	got := jordanLines([]Word{w("like", 0, 0.1), w("this.", 0.1, 0.2)}, -1, "&HFFFF00&")
	if strings.Contains(got, `\N`) {
		t.Errorf("short block was broken across lines: %q", got)
	}
}

// However long the block, it may never become three lines.
func TestJordanLinesNeverProducesThreeLines(t *testing.T) {
	long := []Word{w("this", 0, 0.1), w("makes", 0.1, 0.2), w("me", 0.2, 0.3),
		w("want", 0.3, 0.4), w("to", 0.4, 0.5), w("throw", 0.5, 0.6)}
	got := jordanLines(long, -1, "&HFFFF00&")
	if n := strings.Count(got, `\N`); n != 1 {
		t.Errorf("got %d breaks (%d lines), want exactly 1 break: %q", n, n+1, got)
	}
}

// The measured numbers off Jordan's own render. A regression here is a
// caption that no longer looks like his, which is invisible in a unit test
// unless the numbers themselves are asserted.
func TestDefaultJordanStyleMatchesTheMeasuredReference(t *testing.T) {
	st := DefaultJordanStyle(1920, 1080)
	if st.FontName != "Montserrat ExtraBold" {
		t.Errorf("FontName = %q, want the font that scored 0.803 glyph IoU against his render", st.FontName)
	}
	// 114, not 80: libass sizes by ascent+descent, and for this face
	// cap = Fontsize/2 (measured). 80 rendered a 40px cap against his 57.
	if st.FontSize != 114 {
		t.Errorf("FontSize = %d, want 114 (renders the measured 57px cap at 1920)", st.FontSize)
	}
	if st.Outline != 10 {
		t.Errorf("Outline = %d, want 10 (measured ~11px of solid black around his glyphs)", st.Outline)
	}
	if st.MarginV != 487 {
		t.Errorf("MarginV = %d, want 487 (puts the text bottom 512px up, his number)", st.MarginV)
	}
	if st.MarginH != 183 {
		t.Errorf("MarginH = %d, want 183 so the text column is 66%% of 1080", st.MarginH)
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
	if err := WriteASS(&buf, cues, DefaultJordanStyle(1920, 1080), 1080, 1920); err != nil {
		t.Fatalf("WriteASS error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "PlayResX: 1080") || !strings.Contains(out, "PlayResY: 1920") {
		t.Errorf("ASS output missing PlayRes matching the output frame:\n%s", out)
	}
	if !strings.Contains(out, "Style: Jordan,") {
		t.Errorf("ASS output missing the Jordan style line:\n%s", out)
	}
	if !strings.Contains(out, "WrapStyle: 2") {
		t.Errorf("ASS output must forbid libass re-wrapping (WrapStyle 2):\n%s", out)
	}
	// PER WORD: two words -> two Dialogue events, each lighting one of them.
	events := strings.Count(out, "Dialogue: 0,")
	if events != 2 {
		t.Errorf("got %d Dialogue events for a 2-word block, want 2 (one per word):\n%s", events, out)
	}
	if !strings.Contains(out, `{\c&HFFFF00&}HI{\c&HFFFFFF&}`) {
		t.Errorf("ASS output never lights the FIRST word:\n%s", out)
	}
	if !strings.Contains(out, `{\c&HFFFF00&}THERE{\c&HFFFFFF&}`) {
		t.Errorf("ASS output never lights the SECOND word:\n%s", out)
	}
}
