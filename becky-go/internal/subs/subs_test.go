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
	// No pauses at all: only the 22-char limit can break these.
	// "aaaaa bbbbb ccccc" = 17 chars; adding " ddddd" would be 23 > 22.
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
	if len(got[0]) != 3 {
		t.Errorf("chunk 0 len = %d, want 3 (17 chars)", len(got[0]))
	}
	if len(got[1]) != 1 || got[1][0].Word != "ddddd" {
		t.Errorf("chunk 1 = %+v, want [ddddd]", got[1])
	}
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

func TestBuildFloorsShortCue(t *testing.T) {
	// Two chunks split by the char limit with almost no time between them: the
	// gap-filled first cue would be 0.05s, under the 0.10s floor.
	words := []Word{
		w("aaaaaaaaaaaaaaaaaaaaa", 0.00, 0.04), // 21 chars — next word must break
		w("b", 0.05, 0.90),
	}
	segs := []Segment{{Start: 0.0, End: 1.0, Words: words}}
	cues := Build(segs, DefaultOptions())
	if len(cues) != 2 {
		t.Fatalf("cues = %d, want 2: %+v", len(cues), cues)
	}
	if d := cues[0].End - cues[0].Start; d < 0.10-eps {
		t.Errorf("cue 0 duration = %.4f, want >= 0.10 (flash floor)", d)
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
	// The exact string cli-cut rendered with. White fill, black outline, no
	// shadow, bottom-centre, lifted 90 off the bottom.
	want := "FontName=ProximaNova-Semibold,FontSize=12,Bold=0," +
		"PrimaryColour=&H00FFFFFF,OutlineColour=&H00000000,BackColour=&H00000000," +
		"BorderStyle=1,Outline=2,Shadow=0," +
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
