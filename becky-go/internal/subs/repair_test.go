package subs

import (
	"strings"
	"testing"
)

// words builds a chunk from plain text, with throwaway timings.
func chunkOf(text string) []Word {
	var out []Word
	for i, s := range strings.Fields(text) {
		out = append(out, Word{Word: s, Start: float64(i) * 0.2, End: float64(i)*0.2 + 0.15})
	}
	return out
}

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

// TestRepairKeepsNumberWithItsUnit is Jordan's rule, in his own example:
// "can you post" / "ten times a day?" is correct because "ten" belongs to
// "ten times a day"; "can you post ten" / "times a day?" is wrong.
func TestRepairKeepsNumberWithItsUnit(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			"spelled-out number is not stranded",
			[]string{"can you post ten", "times a day?"},
			[]string{"can you post", "ten times a day?"},
		},
		{
			"digit number is not stranded",
			[]string{"don't post 27", "times a day"},
			[]string{"don't post", "27 times a day"},
		},
		{
			// Pushing "twenty-seven" out of a TWO-word line would leave
			// "posting" alone on screen — the stranded-caption defect, seen
			// verbatim on the real edit at 40.07s with no pause in the audio.
			// The no-strand rule outranks the number rule here; the pair
			// itself is a chunking mistake splitAtBiggestPause no longer
			// makes (it keeps "posting twenty-seven times" together).
			"hyphenated number stays when moving it would strand a word",
			[]string{"posting twenty-seven", "times a day"},
			[]string{"posting twenty-seven", "times a day"},
		},
		{
			"already correct is left alone",
			[]string{"to post 10 times", "a day"},
			[]string{"to post 10 times", "a day"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var in [][]Word
			for _, line := range c.in {
				in = append(in, chunkOf(line))
			}
			got := render(RepairDangling(in, 22))
			if strings.Join(got, " | ") != strings.Join(c.want, " | ") {
				t.Errorf("got   %q\nwant  %q", strings.Join(got, " | "), strings.Join(c.want, " | "))
			}
		})
	}
}

func TestRepairPushesDanglingFunctionWords(t *testing.T) {
	in := [][]Word{chunkOf("four times a day and"), chunkOf("using trial reels")}
	got := render(RepairDangling(in, 22))
	want := []string{"four times a day", "and using trial reels"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("got   %q\nwant  %q", strings.Join(got, " | "), strings.Join(want, " | "))
	}
}

func TestRepairCascadesMultipleDanglers(t *testing.T) {
	// "on the" both dangle; both must move.
	in := [][]Word{chunkOf("it depends on the"), chunkOf("weather")}
	got := render(RepairDangling(in, 22))
	want := []string{"it depends", "on the weather"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("got   %q\nwant  %q", strings.Join(got, " | "), strings.Join(want, " | "))
	}
}

// TestRepairMergesContentlessLine covers the real defect seen on Jordan's edit:
// a caption that was nothing but "a" or "more" could not be fixed by pushing
// (that would empty the line), so it sat there splitting the phrase.
func TestRepairMergesContentlessLine(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"a", "thousand videos"}, []string{"a thousand videos"}},
		{[]string{"more", "doesn't mean"}, []string{"more doesn't mean"}},
		{[]string{"at", "then"}, []string{"at then"}},
		{[]string{"a", "month, that's fine"}, []string{"a month, that's fine"}},
	}
	for _, c := range cases {
		var in [][]Word
		for _, line := range c.in {
			in = append(in, chunkOf(line))
		}
		got := render(RepairDangling(in, 22))
		if strings.Join(got, " | ") != strings.Join(c.want, " | ") {
			t.Errorf("got   %q\nwant  %q", strings.Join(got, " | "), strings.Join(c.want, " | "))
		}
	}
}

// TestRepairMovesNumberEvenWhenTheNextLineIsFull is the other real defect:
// "at least ten" / "million views in the last" was blocked by ONE character.
// A number never gets stranded, cap or no cap.
func TestRepairMovesNumberEvenWhenTheNextLineIsFull(t *testing.T) {
	in := [][]Word{chunkOf("at least ten"), chunkOf("million views in the last")}
	got := render(RepairDangling(in, 22))
	if got[0] != "at least" || !strings.HasPrefix(got[1], "ten million") {
		t.Errorf("got %q, want the number moved to its unit", strings.Join(got, " | "))
	}
}

func TestRepairNeverDropsAWord(t *testing.T) {
	in := [][]Word{chunkOf("and"), chunkOf("then we went")}
	total := 0
	for _, c := range RepairDangling(in, 22) {
		total += len(c)
	}
	if total != 4 {
		t.Errorf("word count = %d, want 4 - repair must never drop a word", total)
	}
}

func TestRepairRespectsLineLengthForNonNumbers(t *testing.T) {
	// Moving "the" forward would blow the next line far past the cap. Unlike a
	// number, a plain function word yields to readability.
	in := [][]Word{chunkOf("this is the"), chunkOf("absolutely enormous unmovable line")}
	got := render(RepairDangling(in, 22))
	if got[0] != "this is the" {
		t.Errorf("got %q, want the dangling word left in place when it cannot fit forward", got[0])
	}
}

func TestRepairLeavesTheLastLineAlone(t *testing.T) {
	// The last line ends where the CUT ends. The phrase stops there because the
	// editor stopped it, so ending on "can" is correct, not a defect.
	in := [][]Word{chunkOf("yeah you can")}
	got := render(RepairDangling(in, 22))
	if len(got) != 1 || got[0] != "yeah you can" {
		t.Errorf("got %q, want the cut-final line untouched", strings.Join(got, " | "))
	}
}

func TestRepairIsAPartition(t *testing.T) {
	in := [][]Word{chunkOf("can you post ten"), chunkOf("times a day? yeah"), chunkOf("you can")}
	var before []string
	for _, c := range in {
		for _, w := range c {
			before = append(before, w.Word)
		}
	}
	var after []string
	for _, c := range RepairDangling(in, 22) {
		for _, w := range c {
			after = append(after, w.Word)
		}
	}
	if strings.Join(before, " ") != strings.Join(after, " ") {
		t.Errorf("repair changed the word sequence:\n  before %q\n  after  %q",
			strings.Join(before, " "), strings.Join(after, " "))
	}
}

// TestRepairStrandsNoPrepositionWhenItsObjectIsPushedAway is the
// post_constantly bug: "against another" / "creator" pushes the dangling
// quantifier "another" onto the next line (correct), which then strands
// "against" alone unless "against" is ALSO recognised as a preposition that
// governs what follows — it belongs in the same class as "with"/"about"/
// "onto", already in danglingWords, and was just missing.
func TestRepairStrandsNoPrepositionWhenItsObjectIsPushedAway(t *testing.T) {
	in := [][]Word{chunkOf("against another"), chunkOf("creator")}
	got := render(RepairDangling(in, 22))
	want := []string{"against another creator"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("got   %q\nwant  %q", strings.Join(got, " | "), strings.Join(want, " | "))
	}
}

// TestRebalanceCapSplitsPicksNaturalPauseOverCapBoundary is the
// post_constantly bug: ChunkWords packs greedily with no lookahead, so "to
// grow on social" / "media" happens purely because the combined line is one
// character over MaxChars - not because of anything the speaker did. "social"
// is not dangling, so this is the plain re-split path: fold the pair back
// together and let splitAtBiggestPause choose the real pause (biggest gap,
// here before "social") instead of the cap boundary.
func TestRebalanceCapSplitsPicksNaturalPauseOverCapBoundary(t *testing.T) {
	in := [][]Word{
		{w("to", 0.00, 0.10), w("grow", 0.12, 0.22), w("on", 0.24, 0.34), w("social", 0.64, 0.84)},
		{w("media", 0.86, 0.96)},
	}
	got := render(rebalanceCapSplits(in, 22, 0.12))
	// The biggest pause sits before "social", but that boundary ends the left
	// line on the dangling "on" — which RepairDangling would then push
	// forward anyway. The splitter now goes straight to the final grouping.
	want := []string{"to grow", "on social media"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("got   %q\nwant  %q", strings.Join(got, " | "), strings.Join(want, " | "))
	}
}

// TestRebalanceCapSplitsLeavesRealPauseAlone covers "can" / "you post" on the
// same edit: the words easily fit on one line together, so the break can only
// be the speaker's own pause. Jordan's rule: "a one-word line is acceptable
// when the word genuinely stands alone" - merging across a real pause trades
// a cosmetic problem for a timing one.
func TestRebalanceCapSplitsLeavesRealPauseAlone(t *testing.T) {
	in := [][]Word{
		{w("you", 0.00, 0.10), w("gotta", 0.12, 0.22), w("be", 0.24, 0.34)},
		{w("posting", 1.50, 1.70)}, // 1.16s later - a real pause, not the cap
	}
	got := render(rebalanceCapSplits(in, 22, 0.12))
	want := []string{"you gotta be", "posting"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("got   %q\nwant  %q", strings.Join(got, " | "), strings.Join(want, " | "))
	}
}

// TestRebalanceCapSplitsFoldsNumberWhenPushWouldStrandItsNeighbor is "a
// thousand" / "videos" from the real edit. "thousand" is a number and must
// stay with its unit ("videos"), but RepairDangling's own guard refuses to
// push it because that would strand "a" alone (below minPiece) - so the
// number never reaches its unit and "videos" is left stranded instead. Since
// the whole pair fits on one line anyway, folding it outright is not a split
// decision (nothing to get wrong) and satisfies the number-stays-with-its-
// unit rule with nothing left behind.
func TestRebalanceCapSplitsFoldsNumberWhenPushWouldStrandItsNeighbor(t *testing.T) {
	in := [][]Word{
		{w("a", 0.00, 0.10), w("thousand", 0.12, 0.22)},
		{w("videos", 0.24, 0.34)},
	}
	got := render(rebalanceCapSplits(in, 22, 0.12))
	want := []string{"a thousand videos"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("got   %q\nwant  %q", strings.Join(got, " | "), strings.Join(want, " | "))
	}
}

// TestRebalanceCapSplitsDefersToRepairDanglingWhenItWontStrand is "compares
// it against" / "other". "against" is also dangling, but here pushing it
// leaves "compares it" behind - well above minPiece, so RepairDangling's own
// push succeeds on its own. rebalanceCapSplits must leave this pair alone:
// re-splitting it here first (before RepairDangling runs) picked the pause
// before "it" on real data and produced "compares" / "it against other" -
// the SAME lone-word defect, just relocated to a different word.
func TestRebalanceCapSplitsDefersToRepairDanglingWhenItWontStrand(t *testing.T) {
	in := [][]Word{
		{w("compares", 0.00, 0.10), w("it", 0.12, 0.22), w("against", 0.24, 0.34)},
		{w("other", 0.36, 0.46)},
	}
	got := render(rebalanceCapSplits(in, 22, 0.12))
	want := []string{"compares it against", "other"} // unchanged - RepairDangling's job
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("got   %q\nwant  %q", strings.Join(got, " | "), strings.Join(want, " | "))
	}
	// And RepairDangling does finish the job, as it already did before this
	// change existed.
	got2 := render(RepairDangling(in, 22))
	want2 := []string{"compares it", "against other"}
	if strings.Join(got2, " | ") != strings.Join(want2, " | ") {
		t.Errorf("got   %q\nwant  %q", strings.Join(got2, " | "), strings.Join(want2, " | "))
	}
}

// TestPass1ChunksEliminatesTheLoneWordEndToEnd is the full pipeline
// (ChunkWords -> rebalanceCapSplits -> RepairDangling) on the same words as
// TestRebalanceCapSplitsPicksNaturalPauseOverCapBoundary, proving ChunkWords'
// own greedy pass actually produces that cap-driven split (not just the
// hand-built input above) and that RepairDangling's existing dangling-push
// still applies afterward: "on" trails the rebalanced first line and moves
// forward onto "social media".
func TestPass1ChunksEliminatesTheLoneWordEndToEnd(t *testing.T) {
	words := []Word{
		w("to", 0.00, 0.10), w("grow", 0.12, 0.22), w("on", 0.24, 0.34),
		w("social", 0.64, 0.84), w("media", 0.86, 0.96),
	}
	got := render(Pass1Chunks(words, 22, 0.12))
	want := []string{"to grow", "on social media"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("got   %q\nwant  %q", strings.Join(got, " | "), strings.Join(want, " | "))
	}
}

// TestRebalanceCapSplitsRescuesSegmentLeadingLoneWord is the model-path shape
// of the same defect: the review model put "i" alone at the HEAD of a segment
// (93.46s on the real edit — the gap after it is 0.32s quantisation at a
// 0.32s threshold, not a pause). The lone-after-multi pass above never looks
// at it and mergeContentless only sweeps danglers, so it survived every
// repair. A real pause still keeps the word alone.
func TestRebalanceCapSplitsRescuesSegmentLeadingLoneWord(t *testing.T) {
	in := [][]Word{
		{w("I", 203.92, 203.92)},
		{w("pressed", 204.24, 204.56), w("record", 204.72, 204.96),
			w("and", 205.20, 205.20), w("had", 205.36, 205.36)},
	}
	got := render(rebalanceCapSplits(in, 22, 0.32))
	want := []string{"I pressed record", "and had"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("got   %q\nwant  %q", strings.Join(got, " | "), strings.Join(want, " | "))
	}
	// Across a REAL pause the lone word genuinely stands alone - untouched.
	in2 := [][]Word{
		{w("actually", 1.00, 1.40)},
		{w("keep", 2.50, 2.70), w("it", 2.72, 2.80)},
	}
	got2 := render(rebalanceCapSplits(in2, 22, 0.32))
	want2 := []string{"actually", "keep it"}
	if strings.Join(got2, " | ") != strings.Join(want2, " | ") {
		t.Errorf("got   %q\nwant  %q", strings.Join(got2, " | "), strings.Join(want2, " | "))
	}
}

// The five tests below are the 2026-07-21 one-word-caption regressions,
// rebuilt from the REAL word timings in post_constantly's transcript
// (FLYV9992_convertedsnow2.transcript.json). ffmpeg silencedetect confirmed
// none of these spots has a real pause at the run's derived threshold
// (0.32s), so none of them may produce a one-word caption. Each asserts the
// exact grouping, not just "no lone word".

// "posting" was stranded at 40.07s: RepairDangling pushed the number
// "twenty-seven" out of a two-word line. The right grouping keeps the number
// with its unit by letting the first line run 4 chars into the burn slack.
func TestPass1KeepsPostingWithItsNumber(t *testing.T) {
	words := []Word{
		w("posting", 107.04, 107.36), w("twenty-seven", 107.60, 108.00),
		w("times", 108.16, 108.32), w("a", 108.48, 108.48),
		w("day,", 108.56, 108.80), w("it's", 108.88, 109.20),
	}
	got := render(Pass1Chunks(words, 22, 0.32))
	want := []string{"posting twenty-seven times", "a day, it's"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("got   %q\nwant  %q", strings.Join(got, " | "), strings.Join(want, " | "))
	}
}

// "anything" was stranded at 56.72s: the splitter broke before "because"
// (a dangling conjunction), and RepairDangling's push then left "anything,"
// alone. The splitter must pick the boundary after "i'm" instead.
func TestPass1DoesNotStrandAnything(t *testing.T) {
	words := []Word{
		w("don't", 139.28, 139.52), w("tell", 139.76, 139.84), w("me", 139.92, 139.92),
		w("anything,", 140.08, 140.40), w("because", 140.48, 140.48),
		w("I'm", 140.64, 140.80), w("not", 140.80, 140.80), w("gonna", 140.88, 141.04),
	}
	got := render(Pass1Chunks(words, 22, 0.32))
	want := []string{"don't tell me", "anything, because I'm", "not gonna"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("got   %q\nwant  %q", strings.Join(got, " | "), strings.Join(want, " | "))
	}
}

// "probably" was stranded at 115.68s (zero silence there even at -25dB):
// the splitter cut after "gonna", and RepairDangling's push of the dangling
// "gonna" out of the two-word "probably gonna" left "probably" alone.
func TestPass1DoesNotStrandProbably(t *testing.T) {
	words := []Word{
		w("probably", 242.80, 242.88), w("gonna", 243.04, 243.20),
		w("give", 243.28, 243.36), w("you", 243.44, 243.44), w("some", 243.52, 243.52),
		w("bad", 243.68, 243.76), w("outdated", 243.92, 244.24),
		w("advice", 244.40, 244.56), w("to", 244.72, 244.72), w("be", 244.88, 244.88),
		w("honest", 244.96, 245.12),
	}
	got := render(Pass1Chunks(words, 22, 0.32))
	want := []string{"probably gonna give", "you some bad outdated", "advice to be honest"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("got   %q\nwant  %q", strings.Join(got, " | "), strings.Join(want, " | "))
	}
}

// "learned" became a 33ms cue at 137.87s: the only in-cap split of
// "the fundamentals learned" strands "learned", so the chunk must be kept
// WHOLE — 24 chars, over the 22 cap but inside the burn slack, which beats a
// stranded word every time.
func TestPass1KeepsFundamentalsLearnedWhole(t *testing.T) {
	words := []Word{
		w("you", 279.60, 279.60), w("don't", 279.68, 279.84), w("have", 280.00, 280.00),
		w("the", 280.16, 280.16), w("fundamentals", 280.24, 280.64),
		w("learned", 280.80, 281.12),
	}
	got := render(Pass1Chunks(words, 22, 0.32))
	want := []string{"you don't have", "the fundamentals learned"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("got   %q\nwant  %q", strings.Join(got, " | "), strings.Join(want, " | "))
	}
}

// "a thousand" / "videos" split (68.30s) and "i" stranded (93.46s) because
// Parakeet quantises times to 0.08s: the gap before "videos" and the derived
// threshold are BOTH "0.32s", but as float64 subtractions they differ in the
// last bits, and strictly-greater flickered. A gap within gapEps of the
// threshold is not a pause.
func TestChunkWordsQuantisedGapAtThresholdIsNotAPause(t *testing.T) {
	// Runtime float64 subtraction (constant expressions would be exact and
	// hide the noise this test exists to pin).
	e1, s1, e2, s2, thr := 163.76, 164.08, 203.92, 204.24, 0.32
	if !(s1-e1 > thr) || !(s2-e2 > thr) {
		t.Fatal("fixture no longer exercises the float-noise case")
	}
	got := render(ChunkWords([]Word{
		w("a", 163.28, 163.28), w("thousand", 163.52, 163.76), w("videos", 164.08, 164.40),
	}, 22, 0.32))
	if len(got) != 1 || got[0] != "a thousand videos" {
		t.Errorf("got %q, want one chunk \"a thousand videos\"", strings.Join(got, " | "))
	}
	got = render(ChunkWords([]Word{
		w("I", 203.92, 203.92), w("pressed", 204.24, 204.56), w("record", 204.72, 204.96),
	}, 22, 0.32))
	if len(got) != 1 || got[0] != "I pressed record" {
		t.Errorf("got %q, want one chunk \"I pressed record\"", strings.Join(got, " | "))
	}
}

func TestIsDangling(t *testing.T) {
	dangling := []string{"the", "a", "and", "to", "of", "10", "27", "ten", "twenty-seven", "gotta", "your", "more", "against"}
	for _, s := range dangling {
		if !isDangling(s) {
			t.Errorf("isDangling(%q) = false, want true", s)
		}
	}
	// Punctuation must not hide a dangler, and real words must not be flagged.
	if !isDangling("the,") {
		t.Error(`isDangling("the,") = false, want true`)
	}
	for _, s := range []string{"day?", "times", "posting", "sand.", "wasted", "media"} {
		if isDangling(s) {
			t.Errorf("isDangling(%q) = true, want false", s)
		}
	}
	// Verbs and pronouns must NOT be danglers: "yeah you can" and "what it does"
	// are complete lines, and flagging these merged perfectly good breaks.
	for _, s := range []string{"can", "does", "is", "you", "it", "we", "have", "will"} {
		if isDangling(s) {
			t.Errorf("isDangling(%q) = true, want false - it ends a clause perfectly well", s)
		}
	}
}
