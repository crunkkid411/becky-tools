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
			"hyphenated number is not stranded",
			[]string{"posting twenty-seven", "times a day"},
			[]string{"posting", "twenty-seven times a day"},
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

func TestIsDangling(t *testing.T) {
	dangling := []string{"the", "a", "and", "to", "of", "10", "27", "ten", "twenty-seven", "gotta", "your", "more"}
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
