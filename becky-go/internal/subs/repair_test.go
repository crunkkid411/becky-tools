package subs

import (
	"strings"
	"testing"
)

// chunkOf builds a chunk from plain text, with throwaway timings. Shared helper.
func chunkOf(text string) []Word {
	var out []Word
	for i, s := range strings.Fields(text) {
		out = append(out, Word{Word: s, Start: float64(i) * 0.2, End: float64(i)*0.2 + 0.15})
	}
	return out
}

// render joins each chunk's words with spaces. Shared helper.
func render(chunks [][]Word) []string {
	var out []string
	for _, c := range chunks {
		var parts []string
		for _, w := range c {
			parts = append(parts, w.Word)
		}
		out = append(out, strings.Join(parts, " "))
	}
	return out
}

// ---- The clean deterministic chunker: Jordan's rules (2026-07-24) ----

// A comma, period, ? or ! ENDS the chunk. Tight timings + a big cap, so ONLY the
// punctuation forces the breaks. ChunkWords keeps the raw word (with its mark);
// normalize later drops the . and , and keeps ? and ! (tested separately below).
func TestChunkWordsBreaksAtPunctuation(t *testing.T) {
	words := []Word{
		w("you", 0.00, 0.20), w("know,", 0.22, 0.40),
		w("it's", 0.42, 0.60), w("okay.", 0.62, 0.80),
		w("really?", 0.82, 1.00), w("yes!", 1.02, 1.20), w("now", 1.22, 1.40),
	}
	got := render(ChunkWords(words, 40, 0.5))
	want := []string{"you know,", "it's okay.", "really?", "yes!", "now"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("got   %q\nwant  %q", strings.Join(got, " | "), strings.Join(want, " | "))
	}
}

// 22 is a HARD cap and a one-word line is allowed: with no pauses or punctuation the
// words pack greedily up to 22, and whatever overflows starts the next line - even if
// that leaves a single word. No phrase heuristics, no balancing.
func TestChunkWordsHardCapAllowsLoneWord(t *testing.T) {
	words := []Word{
		w("the", 0.00, 0.10), w("fundamentals", 0.12, 0.40), w("learned", 0.42, 0.60),
	}
	got := render(ChunkWords(words, 22, 0.5))
	// "the fundamentals" = 16; adding " learned" -> 24 > 22, so "learned" is its own line.
	want := []string{"the fundamentals", "learned"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("got %q, want %q", got, want)
	}
	for _, c := range got {
		if len(c) > 22 {
			t.Errorf("line %q exceeds the hard 22 cap (%d)", c, len(c))
		}
	}
}

// A word the speaker isolated with pauses on both sides is its own caption - single
// words are absolutely allowed.
func TestChunkWordsSingleWordFromPace(t *testing.T) {
	words := []Word{
		w("the", 0.00, 0.20), w("food", 0.25, 0.45),
		w("good", 1.10, 1.75), // 0.65s pause before AND after
		w("really", 2.40, 2.65), w("nice", 2.70, 2.95),
	}
	got := render(ChunkWords(words, 22, 0.25))
	want := []string{"the food", "good", "really nice"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("got %q, want %q", got, want)
	}
}

// End to end: normalize drops the . and , (split points, not printed marks) and keeps
// ? and ! - only those two survive. "you know, it's okay. right?" over one segment ->
// three cues, no commas or periods anywhere.
func TestBuildDropsPeriodsAndCommasKeepsQuestion(t *testing.T) {
	words := []Word{
		w("you", 0.00, 0.20), w("know,", 0.22, 0.40),
		w("it's", 0.42, 0.60), w("okay.", 0.62, 0.80),
		w("right?", 0.85, 1.10),
	}
	opt := Options{MaxChars: 22, GapSeconds: 0.5, Lowercase: true}
	cues := Build([]Segment{{Start: 0, End: 1.2, Words: words}}, opt)
	got := make([]string, len(cues))
	for i, c := range cues {
		got[i] = c.Text
	}
	want := []string{"you know", "it's okay", "right?"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Fatalf("got %q, want %q", got, want)
	}
	for _, c := range cues {
		if strings.ContainsAny(c.Text, ".,;:") {
			t.Errorf("cue %q still has . , ; or : - only ? and ! may survive", c.Text)
		}
	}
}

// The model review pass (off by default) can only suggest word ORDER; RepairModelGroups
// flattens it and re-chunks deterministically, so the rules always hold.
func TestRepairModelGroupsReChunksDeterministically(t *testing.T) {
	// A deliberately awful grouping that glues a ? mid-line and packs past the cap.
	groups := [][]Word{
		{w("you", 0.00, 0.20), w("know", 0.22, 0.40), w("what", 0.42, 0.60), w("i", 0.62, 0.70),
			w("miss?", 0.72, 0.95), w("good", 0.97, 1.30)},
	}
	got := render(RepairModelGroups(groups, 22, 0.5))
	for _, line := range got {
		fields := strings.Fields(line)
		for k, f := range fields {
			if k != len(fields)-1 && (strings.HasSuffix(f, "?") || strings.HasSuffix(f, "!")) {
				t.Errorf("line %q has a sentence end mid-line: %v", line, got)
			}
		}
		if len(line) > 22 {
			t.Errorf("line %q over the 22 cap: %v", line, got)
		}
	}
}
