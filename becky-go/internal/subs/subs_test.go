package subs

import (
	"strings"
	"testing"
)

const eps = 1e-6

func closeTo(a, b float64) bool { return a-b < eps && b-a < eps }

func w(text string, start, end float64) Word {
	return Word{Word: text, Start: start, End: end}
}

func TestChunkWordsBreaksOnPause(t *testing.T) {
	// "hello world" flows (0.1s gap); "again" is after a 0.8s pause.
	words := []Word{
		w("hello", 10.5, 10.9),
		w("world", 11.0, 11.4),
		w("again", 12.2, 12.6),
	}
	got := ChunkWords(words, 22, 0.120)
	if len(got) != 2 {
		t.Fatalf("chunks = %d, want 2", len(got))
	}
	if len(got[0]) != 2 || got[0][0].Word != "hello" || got[0][1].Word != "world" {
		t.Errorf("chunk 0 = %+v, want [hello world]", got[0])
	}
	if len(got[1]) != 1 || got[1][0].Word != "again" {
		t.Errorf("chunk 1 = %+v, want [again]", got[1])
	}
}

func TestChunkWordsBreaksOnCharLimit(t *testing.T) {
	// No pauses at all: only the 22-char limit can break these. The full run
	// ("aaaaa bbbbb ccccc ddddd" = 23 chars) is over the cap, so it must break —
	// but with LOOKAHEAD, not greedily: the greedy fill gave 3 words + a
	// stranded "ddddd", which is the exact defect from Jordan's real edit
	// ("media", "videos", "fundamentals" alone on a line). The balanced split
	// leaves no lone word.
	words := []Word{
		w("aaaaa", 0.0, 0.1),
		w("bbbbb", 0.1, 0.2),
		w("ccccc", 0.2, 0.3),
		w("ddddd", 0.3, 0.4),
	}
	got := ChunkWords(words, 22, 0.120)
	if len(got) != 2 {
		t.Fatalf("chunks = %d, want 2", len(got))
	}
	if len(got[0]) != 2 || len(got[1]) != 2 {
		t.Errorf("chunks = %d + %d words, want 2 + 2 (no stranded lone word)", len(got[0]), len(got[1]))
	}
}

// TestChunkWordsDoesNotStrandTheCapOverflowWord is the root cause of the 8
// remaining one-word captions on Jordan's post_constantly edit: with
// ASR-quantised timings every gap is equal, the old greedy chunker filled the
// line to exactly the 22-char cap, and whatever word landed after the cap
// ("videos" here) was stranded alone. The rebalance pass then could not fix it
// either, because its re-split's >= tie-break on equal gaps picked the same
// greedy boundary back. The chunker must pick a split that leaves both lines
// multi-word.
func TestChunkWordsDoesNotStrandTheCapOverflowWord(t *testing.T) {
	// "and understand posting" = 22 chars exactly; " videos" overflows.
	words := []Word{
		w("and", 0.00, 0.10),
		w("understand", 0.12, 0.40),
		w("posting", 0.42, 0.60),
		w("videos", 0.62, 0.80),
	}
	got := ChunkWords(words, 22, 0.120)
	for i, c := range got {
		if len(c) < 2 {
			t.Fatalf("chunk %d = %+v is a lone word — the cap overflow was stranded", i, c)
		}
	}
	if len(got) != 2 || got[0][len(got[0])-1].Word != "understand" {
		t.Errorf("chunks = %v, want \"and understand\" | \"posting videos\"", renderChunks(got))
	}
}

// TestChunkWordsDoesNotStrandALongOverflowWord: a LONG lone word
// ("fundamentals", 12 chars) passes the char-based minPiece guard, so char
// length alone cannot catch this — the no-lone-word preference has to be about
// word count.
func TestChunkWordsDoesNotStrandALongOverflowWord(t *testing.T) {
	words := []Word{
		w("learn", 0.00, 0.20),
		w("the", 0.22, 0.30),
		w("actual", 0.32, 0.55),
		w("fundamentals", 0.57, 1.10),
	}
	got := ChunkWords(words, 22, 0.120)
	for i, c := range got {
		if len(c) < 2 {
			t.Fatalf("chunk %d = %+v is a lone word — long words must not be stranded either", i, c)
		}
	}
}

// TestChunkWordsStillBreaksAtRealPausesFirst: the pause rule outranks the cap.
// A word after a real pause "genuinely stands alone" (Jordan's rule) and must
// stay its own caption, never be merged to fix a cosmetic lone-word count.
func TestChunkWordsStillBreaksAtRealPausesFirst(t *testing.T) {
	words := []Word{
		w("i", 0.00, 0.05),
		w("keep", 0.07, 0.20),
		w("it", 0.22, 0.30),
		w("simple", 0.32, 0.60),
		w("actually", 1.50, 1.90), // 0.9s pause — a real break
	}
	got := ChunkWords(words, 22, 0.120)
	if len(got) != 2 {
		t.Fatalf("chunks = %v, want the pause respected as a break", renderChunks(got))
	}
	if len(got[1]) != 1 || got[1][0].Word != "actually" {
		t.Errorf("chunk 1 = %+v, want [actually] alone (it follows a real pause)", got[1])
	}
}

func renderChunks(chunks [][]Word) []string {
	var out []string
	for _, c := range chunks {
		var parts []string
		for _, wd := range c {
			parts = append(parts, wd.Word)
		}
		out = append(out, strings.Join(parts, " "))
	}
	return out
}

func TestAutoGapSecondsAdaptsToTheASR(t *testing.T) {
	// A "tight" transcript (real word durations, near-zero gaps in connected
	// speech) must reproduce cli-cut's original 0.120s constant exactly.
	var tight []Word
	for i := 0; i < 40; i++ {
		s := float64(i) * 0.40
		tight = append(tight, w("word", s, s+0.38)) // 0.02s gaps
	}
	if got := AutoGapSeconds(tight); !closeTo(got, 0.120) {
		t.Errorf("tight transcript -> %.3f, want the 0.120 floor (cli-cut behaviour preserved)", got)
	}

	// A Parakeet-shaped transcript: half the words zero-duration, connected
	// speech spaced 0.16-0.24s. The threshold must rise above that spacing or
	// every word becomes its own caption.
	var loose []Word
	for i := 0; i < 40; i++ {
		s := float64(i) * 0.24
		loose = append(loose, w("word", s, s)) // zero duration -> 0.24s apparent gaps
	}
	got := AutoGapSeconds(loose)
	if got <= 0.120 {
		t.Errorf("parakeet-shaped transcript -> %.3f, want > 0.120 (0.24s spacing is speech, not a pause)", got)
	}
	if !closeTo(got, 0.24) {
		t.Errorf("parakeet-shaped transcript -> %.3f, want 0.240 (the p90 of its gaps)", got)
	}

	// Too little data to measure: fall back to the constant rather than guess.
	if got := AutoGapSeconds([]Word{w("a", 0, 1), w("b", 2, 3)}); !closeTo(got, 0.120) {
		t.Errorf("short transcript -> %.3f, want the 0.120 fallback", got)
	}
}

func TestQuantizeToFramesIsExactAt2997(t *testing.T) {
	const fps = 30000.0 / 1001.0 // true NTSC, one frame = 33.3667ms
	cues := []Cue{
		{Start: 0, End: 2.2, Text: "a"},
		{Start: 2.2, End: 3.0, Text: "b"},
		{Start: 3.0, End: 5.0, Text: "c"},
	}
	got := QuantizeToFrames(cues, fps)

	// Every boundary must land on a whole frame.
	for i, c := range got {
		for _, edge := range []struct {
			name string
			t    float64
		}{{"start", c.Start}, {"end", c.End}} {
			f := c.Start * fps
			if edge.name == "end" {
				f = c.End * fps
			}
			if d := f - float64(int64(f+0.5)); d > 1e-6 || d < -1e-6 {
				t.Errorf("cue %d %s = %.6fs = %.4f frames, want a whole frame", i, edge.name, edge.t, f)
			}
		}
	}
	// The gap-free invariant must survive quantisation - that is the whole point.
	for i := 0; i < len(got)-1; i++ {
		if got[i].End < got[i+1].Start-1e-9 {
			t.Errorf("quantising opened a gap between cue %d (ends %.6f) and %d (starts %.6f)",
				i, got[i].End, i+1, got[i+1].Start)
		}
	}
	// And nothing collapsed to zero length.
	for i, c := range got {
		if c.End <= c.Start {
			t.Errorf("cue %d collapsed to %.6f..%.6f", i, c.Start, c.End)
		}
	}
}

func TestQuantizeToFramesNoOpWithoutRate(t *testing.T) {
	in := []Cue{{Start: 0.123456, End: 1.234567, Text: "a"}}
	got := QuantizeToFrames(in, 0)
	if !closeTo(got[0].Start, 0.123456) || !closeTo(got[0].End, 1.234567) {
		t.Errorf("fps 0 must leave times untouched, got %.6f..%.6f", got[0].Start, got[0].End)
	}
}

func TestBuildQuantisesWhenFPSSet(t *testing.T) {
	const fps = 30000.0 / 1001.0
	opt := DefaultOptions()
	opt.FPS = fps
	segs := []Segment{{Start: 0, End: 1.0, Words: []Word{w("one", 0.137, 0.611)}}}
	cues := Build(segs, opt)
	if len(cues) != 1 {
		t.Fatalf("cues = %d, want 1", len(cues))
	}
	f := cues[0].End * fps
	if d := f - float64(int64(f+0.5)); d > 1e-6 || d < -1e-6 {
		t.Errorf("Build did not frame-snap the cue end: %.6fs = %.4f frames", cues[0].End, f)
	}
}

// TestBuildSpansCutWhenSpeechIsContinuous is the post_constantly bug: a clip
// holding just "can" was captioned alone because BuildFromChunks chunked each
// clip independently, even though the next clip opens on "you post" with
// almost no silence trimmed at the join — the cut removed dead air, not a
// beat in the sentence. The caption must span the cut into one line instead
// of stranding "can" by itself.
func TestBuildSpansCutWhenSpeechIsContinuous(t *testing.T) {
	opt := Options{MaxChars: 22, GapSeconds: 0.25, Lowercase: true}
	// Same source, ONE shared word list with ABSOLUTE source times (as real segments have -
	// every clip of a source shares that source's word slice). "can you post" is three
	// consecutive words; the edit keeps it as two CONTIGUOUS clips (a frame-cut mid-phrase, no
	// content removed), so the boundary words "can" (ends 0.35) and "you" (starts 0.40) are
	// only 0.05s apart in the source -> the caption should span the cut.
	src := []Word{w("can", 0.05, 0.35), w("you", 0.40, 0.60), w("post", 0.65, 0.85)}
	segs := []Segment{
		{Start: 0.00, End: 0.38, Words: src}, // holds "can"
		{Start: 0.38, End: 0.90, Words: src}, // holds "you", "post"
	}
	cues := Build(segs, opt)
	if len(cues) != 1 {
		t.Fatalf("cues = %d, want 1 (the cut should be spanned): %+v", len(cues), cues)
	}
	if cues[0].Text != "can you post" {
		t.Errorf("text = %q, want %q", cues[0].Text, "can you post")
	}
	if !closeTo(cues[0].Start, 0) || !closeTo(cues[0].End, 0.9) {
		t.Errorf("cue = {%.4f %.4f}, want {0.0000 0.9000} (anchored to the outer cut points)",
			cues[0].Start, cues[0].End)
	}
}

// Jordan (2026-07-24): "words from before or after a significant jumpcut are still placed
// together." A cut that removed CONTENT (a big source-time jump) must NOT be spanned.
func TestBuildDoesNotSpanSignificantJumpcut(t *testing.T) {
	opt := Options{MaxChars: 22, GapSeconds: 0.25, Lowercase: true}
	// Clip A keeps "...media" (ends 2.0s); clip B keeps "you should post" from 28s LATER in
	// the same source. The boundary words are ~28s apart -> a real jumpcut -> separate captions.
	src := []Word{
		w("media", 1.70, 2.00),
		w("you", 30.00, 30.20), w("should", 30.25, 30.55), w("post", 30.60, 30.90),
	}
	segs := []Segment{
		{Start: 1.5, End: 2.05, Words: src},  // holds "media"
		{Start: 29.9, End: 31.0, Words: src}, // holds "you should post"
	}
	cues := Build(segs, opt)
	if len(cues) < 2 {
		t.Fatalf("a jumpcut must break, not merge: got %d cue(s): %+v", len(cues), cues)
	}
	if cues[0].Text != "media" {
		t.Errorf("first caption = %q, want %q (must not merge across the jumpcut)", cues[0].Text, "media")
	}
}

// Jordan (2026-07-24): captions should break at ? and ! even when the pause is short.
func TestChunkWordsBreaksAtQuestionAndExclamation(t *testing.T) {
	// No pauses between words and a generous MaxChars, so ONLY the ? and ! force the breaks.
	words := []Word{
		w("do", 0.00, 0.20), w("it?", 0.22, 0.40),
		w("yes!", 0.42, 0.60), w("now", 0.62, 0.80),
	}
	chunks := ChunkWords(words, 40, 0.5)
	got := render(chunks)
	want := []string{"do it?", "yes!", "now"}
	if len(got) != len(want) {
		t.Fatalf("want %d chunks (break after ? and after !), got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("chunk %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestBuildDoesNotSpanCutOnARealPause is the counterpart: when the silence
// trimmed around a cut is longer than the pause threshold, that IS a real
// break in the speech (the same measure ChunkWords already uses to break
// WITHIN a segment), so the cut must stay a hard caption boundary.
func TestBuildDoesNotSpanCutOnARealPause(t *testing.T) {
	opt := Options{MaxChars: 22, GapSeconds: 0.12, Lowercase: true}
	segs := []Segment{
		{Start: 0, End: 0.5, Words: []Word{w("can", 0.05, 0.35)}},                        // 0.15s trimmed after "can"
		{Start: 0, End: 0.6, Words: []Word{w("you", 0.05, 0.35), w("post", 0.45, 0.55)}}, // 0.05s trimmed before "you"
	}
	cues := Build(segs, opt)
	if len(cues) != 2 {
		t.Fatalf("cues = %d, want 2 (0.20s trimmed silence exceeds the 0.12s threshold): %+v", len(cues), cues)
	}
	if cues[0].Text != "can" || cues[1].Text != "you post" {
		t.Errorf("texts = %q / %q, want %q / %q", cues[0].Text, cues[1].Text, "can", "you post")
	}
}

// TestBuildDoesNotSpanCutPastMaxChars guards the other direction: two clips
// can be speech-continuous and still not worth spanning if doing so would
// blow the line out past what fits on screen. Bounded by the same
// MaxChars+overflowSlack give repair.go uses to keep a pushed phrase
// readable, not unlimited.
func TestBuildDoesNotSpanCutPastMaxChars(t *testing.T) {
	opt := Options{MaxChars: 22, GapSeconds: 0.25, Lowercase: true}
	segs := []Segment{
		{Start: 0, End: 1.0, Words: []Word{
			w("seriously", 0.05, 0.35), w("though", 0.45, 0.65), w("man", 0.75, 0.85),
		}}, // 0.15s trimmed after "man"
		{Start: 0, End: 1.0, Words: []Word{
			w("totally", 0.05, 0.35), w("awesome", 0.45, 0.75), w("dude", 0.85, 0.95),
		}}, // 0.05s trimmed before "totally"
	}
	cues := Build(segs, opt)
	if len(cues) != 2 {
		t.Fatalf("cues = %d, want 2 (merged text is 43 chars, past MaxChars+slack): %+v", len(cues), cues)
	}
	if cues[0].Text != "seriously though man" || cues[1].Text != "totally awesome dude" {
		t.Errorf("texts = %q / %q, want the two clips left unmerged", cues[0].Text, cues[1].Text)
	}
}

func TestWordsInRangeKeepsStraddlingWords(t *testing.T) {
	words := []Word{
		w("before", 1.0, 1.5), // fully before
		w("edge", 1.8, 2.2),   // straddles the in point
		w("inside", 3.0, 3.4), // fully inside
		w("tail", 4.8, 5.2),   // straddles the out point
		w("after", 6.0, 6.5),  // fully after
	}
	got := WordsInRange(words, 2.0, 5.0)
	var names []string
	for _, g := range got {
		names = append(names, g.Word)
	}
	want := "edge,inside,tail"
	if strings.Join(names, ",") != want {
		t.Errorf("WordsInRange = %v, want %s", names, want)
	}
}

// TestBuildSnapsToCutPoints is the load-bearing test: captions must start and
// end exactly on the cut, with no gaps between them. A regression here is the
// "captions flash off screen for a millisecond" bug this package exists to fix.
func TestBuildSnapsToCutPoints(t *testing.T) {
	words := []Word{
		w("hello", 10.5, 10.9),
		w("world", 11.0, 11.4),
		w("again", 12.2, 12.6),
		w("next", 20.4, 20.8),
	}
	segs := []Segment{
		{Start: 10.0, End: 13.0, Words: words}, // 3.0s, output [0,3)
		{Start: 20.0, End: 22.0, Words: words}, // 2.0s, output [3,5)
	}

	cues := Build(segs, DefaultOptions())
	if len(cues) != 3 {
		t.Fatalf("cues = %d, want 3: %+v", len(cues), cues)
	}

	want := []Cue{
		{Start: 0.0, End: 2.2, Text: "hello world"},
		{Start: 2.2, End: 3.0, Text: "again"},
		{Start: 3.0, End: 5.0, Text: "next"},
	}
	for i, wc := range want {
		if !closeTo(cues[i].Start, wc.Start) || !closeTo(cues[i].End, wc.End) || cues[i].Text != wc.Text {
			t.Errorf("cue %d = {%.4f %.4f %q}, want {%.4f %.4f %q}",
				i, cues[i].Start, cues[i].End, cues[i].Text, wc.Start, wc.End, wc.Text)
		}
	}

	// The invariants, stated directly.
	if !closeTo(cues[0].Start, 0) {
		t.Errorf("first cue starts at %.4f, want 0 (must snap to the cut)", cues[0].Start)
	}
	if !closeTo(cues[1].End, 3.0) {
		t.Errorf("last cue of segment 1 ends at %.4f, want 3.0 (must snap to the cut)", cues[1].End)
	}
	for i := 0; i < len(cues)-1; i++ {
		if cues[i].End < cues[i+1].Start-eps {
			t.Errorf("gap between cue %d (ends %.4f) and cue %d (starts %.4f) — captions would flash off",
				i, cues[i].End, i+1, cues[i+1].Start)
		}
	}
}

func TestBuildSilentSegmentAdvancesTimelineWithoutCue(t *testing.T) {
	words := []Word{w("only", 0.2, 0.6)}
	segs := []Segment{
		{Start: 0.0, End: 1.0, Words: nil},   // silent, 1.0s
		{Start: 0.0, End: 1.0, Words: words}, // speech, 1.0s at output offset 1.0
	}
	cues := Build(segs, DefaultOptions())
	if len(cues) != 1 {
		t.Fatalf("cues = %d, want 1", len(cues))
	}
	if !closeTo(cues[0].Start, 1.0) || !closeTo(cues[0].End, 2.0) {
		t.Errorf("cue = {%.4f %.4f}, want {1.0000 2.0000} — silent segment must still advance the timeline",
			cues[0].Start, cues[0].End)
	}
}

func TestBuildFloorNeverOverlapsNextCue(t *testing.T) {
	// Two chunks split by the char limit with almost no time between them: the
	// gap-filled first cue is 0.05s, under the 0.10s floor.
	//
	// cli-cut floored it to 0.10s and let it run 0.05s PAST the next caption's
	// start. That is where the doubled text on screen came from: libass renders
	// both overlapping cues, stacked. MinDuration exists to stop a caption
	// blinking OFF for a few frames — when the next caption is already arriving
	// there is no blink to prevent, so the incoming caption wins.
	words := []Word{
		// 21 + 8 chars: 30 combined, past even the burn slack, so the line
		// MUST break (a shorter second word would now be kept whole instead).
		w("aaaaaaaaaaaaaaaaaaaaa", 0.00, 0.04),
		w("bbbbbbbb", 0.05, 0.90),
	}
	segs := []Segment{{Start: 0.0, End: 1.0, Words: words}}
	cues := Build(segs, DefaultOptions())
	if len(cues) != 2 {
		t.Fatalf("cues = %d, want 2: %+v", len(cues), cues)
	}
	if cues[0].End > cues[1].Start+eps {
		t.Errorf("cue 0 ends %.4f, cue 1 starts %.4f — captions must never overlap",
			cues[0].End, cues[1].Start)
	}
}

func TestBuildFloorsShortCueWhenThereIsRoom(t *testing.T) {
	// The floor still does its job where it can: one cue, a 0.04s word, and a
	// whole segment of room after it.
	words := []Word{w("hi", 0.00, 0.04)}
	segs := []Segment{{Start: 0.0, End: 1.0, Words: words}}
	cues := Build(segs, DefaultOptions())
	if len(cues) != 1 {
		t.Fatalf("cues = %d, want 1: %+v", len(cues), cues)
	}
	if d := cues[0].End - cues[0].Start; d < 0.10-eps {
		t.Errorf("cue 0 duration = %.4f, want >= 0.10 (flash floor)", d)
	}
}

func TestWordStraddlingACutIsCaptionedOnce(t *testing.T) {
	// The post_constantly bug: Jordan cuts on the frame he wants, which lands
	// mid-word. "viral" spans 1.8-2.4 and the cut is at 2.0, so the word
	// overlapped BOTH clips and was captioned twice ("...going viral" / "viral").
	words := []Word{
		w("going", 1.20, 1.75),
		w("viral", 1.80, 2.40),
		w("next", 2.50, 2.90),
	}
	segs := []Segment{
		{Source: "a.mp4", Start: 1.0, End: 2.0, Words: words},
		{Source: "a.mp4", Start: 2.0, End: 3.0, Words: words},
	}
	per := WordsPerSegment(segs)

	count := 0
	for _, seg := range per {
		for _, got := range seg {
			if got.Word == "viral" {
				count++
			}
		}
	}
	if count != 1 {
		t.Fatalf("\"viral\" appears in %d clips, want exactly 1", count)
	}
	// 0.2s of it is in clip 0, 0.4s in clip 1 — the clip holding more wins.
	if len(per[1]) == 0 || per[1][0].Word != "viral" {
		t.Errorf("clip 1 words = %v, want it to open on \"viral\" (it holds 0.4s vs 0.2s)", per[1])
	}
}

func TestWordFullyInsideTwoClipsIsKeptTwice(t *testing.T) {
	// A deliberately repeated moment must still caption twice — the de-dupe only
	// settles words that STRADDLE a cut, never ones a clip fully contains.
	words := []Word{w("again", 1.20, 1.40)}
	segs := []Segment{
		{Source: "a.mp4", Start: 1.0, End: 2.0, Words: words},
		{Source: "a.mp4", Start: 1.0, End: 2.0, Words: words},
	}
	per := WordsPerSegment(segs)
	if len(per[0]) != 1 || len(per[1]) != 1 {
		t.Errorf("per-clip words = %v — a repeated clip must caption its words both times", per)
	}
}

func TestBuildPostSpeechHoldBridgesShortGap(t *testing.T) {
	// Segments are laid end to end by Build, so a gap only exists if a segment
	// has zero duration. Assert the no-op case explicitly: a gapless reel must
	// not have its last cue extended past its own cut.
	segs := []Segment{
		{Start: 0.0, End: 1.0, Words: []Word{w("one", 0.1, 0.5)}},
		{Start: 0.0, End: 1.0, Words: []Word{w("two", 0.1, 0.5)}},
	}
	cues := Build(segs, DefaultOptions())
	if len(cues) != 2 {
		t.Fatalf("cues = %d, want 2", len(cues))
	}
	if !closeTo(cues[0].End, 1.0) {
		t.Errorf("cue 0 ends at %.4f, want exactly 1.0 (its own cut point)", cues[0].End)
	}
	if !closeTo(cues[1].Start, 1.0) {
		t.Errorf("cue 1 starts at %.4f, want exactly 1.0", cues[1].Start)
	}
}

func TestNormalizeMatchesShippedLook(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Hello World.", "hello world"},
		{"  spaced   out  ", "spaced out"},
		{"trailing,", "trailing"},
		{"keep's it;", "keep's it"},
	}
	for _, c := range cases {
		if got := normalize(c.in, true); got != c.want {
			t.Errorf("normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := normalize("Keep Case.", false); got != "Keep Case" {
		t.Errorf("normalize with lower=false = %q, want %q", got, "Keep Case")
	}
}

func TestSRTTime(t *testing.T) {
	cases := []struct {
		sec  float64
		want string
	}{
		{0, "00:00:00,000"},
		{1.5, "00:00:01,500"},
		{61.25, "00:01:01,250"},
		{3661.007, "01:01:01,007"},
		{-1, "00:00:00,000"},
	}
	for _, c := range cases {
		if got := SRTTime(c.sec); got != c.want {
			t.Errorf("SRTTime(%v) = %q, want %q", c.sec, got, c.want)
		}
	}
}

func TestWriteSRT(t *testing.T) {
	var b strings.Builder
	err := WriteSRT(&b, []Cue{
		{Start: 0, End: 2.2, Text: "hello world"},
		{Start: 2.2, End: 3, Text: "again"},
	})
	if err != nil {
		t.Fatalf("WriteSRT: %v", err)
	}
	want := "1\r\n00:00:00,000 --> 00:00:02,200\r\nhello world\r\n\r\n" +
		"2\r\n00:00:02,200 --> 00:00:03,000\r\nagain\r\n\r\n"
	if b.String() != want {
		t.Errorf("WriteSRT =\n%q\nwant\n%q", b.String(), want)
	}
}

func TestDefaultStyleForceStyle(t *testing.T) {
	// cli-cut's style, with the outline at 1 instead of its 2 — Jordan judged 2
	// slightly too heavy on screen. White fill, black outline, no shadow,
	// bottom-centre, lifted 90 off the bottom.
	want := "FontName=ProximaNova-Semibold,FontSize=12,Bold=0," +
		"PrimaryColour=&H00FFFFFF,OutlineColour=&H00000000,BackColour=&H00000000," +
		"BorderStyle=1,Outline=1,Shadow=0," +
		"Alignment=2,MarginV=90"
	if got := DefaultStyle().ForceStyle(); got != want {
		t.Errorf("ForceStyle() =\n%s\nwant\n%s", got, want)
	}
}

func TestEscapeFilterPathEscapesDriveColon(t *testing.T) {
	got := EscapeFilterPath(`X:\tmp\master.srt`)
	if !strings.Contains(got, `X\:/tmp/master.srt`) {
		t.Errorf("EscapeFilterPath = %s, want escaped drive colon and forward slashes", got)
	}
	if !strings.HasPrefix(got, "'") || !strings.HasSuffix(got, "'") {
		t.Errorf("EscapeFilterPath = %s, want single-quoted", got)
	}
}
