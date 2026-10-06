package main

import "testing"

func TestLexiconFixesTextNeverTimes(t *testing.T) {
	fixes, bad := parseLexicon(defaultLexicon + "\nbroken line\na b => x y z\n")
	if len(bad) != 2 {
		t.Fatalf("both malformed lines must be reported, got %v", bad)
	}
	c := 0.9
	words := []Word{
		{Word: "I'm", Start: 0.0, End: 0.2},
		{Word: "Harry", Start: 0.3, End: 0.6, Confidence: &c},
		{Word: "Jordan,", Start: 0.7, End: 1.1},
		{Word: "from", Start: 1.2, End: 1.4},
		{Word: "Take", Start: 2.0, End: 2.2},
		{Word: "It", Start: 2.3, End: 2.4},
		{Word: "Back", Start: 2.5, End: 2.8},
		{Word: "2007.", Start: 2.9, End: 3.6},
	}
	got, log := applyLexicon(words, fixes)
	if wordsText(got) != "I'm Hair Jordan, from TakingBack2007." {
		t.Fatalf("text wrong: %q", wordsText(got))
	}
	if got[1].Start != 0.3 || got[1].End != 0.6 || got[2].Start != 0.7 || got[2].End != 1.1 {
		t.Fatalf("a word-for-word swap must keep each word's times: %+v", got[1:3])
	}
	if got[4].Start != 2.0 || got[4].End != 3.6 {
		t.Fatalf("a merge must span first start to last end: %+v", got[4])
	}
	if len(log) != 2 || log[0].Heard != "Harry Jordan," || log[0].Meant != "Hair Jordan," || log[1].Start != 2.0 {
		t.Fatalf("audit wrong: %+v", log)
	}
}

func TestLexiconLongestMatchWins(t *testing.T) {
	fixes, _ := parseLexicon("disc => DISC\ndisc tracks => diss tracks\n")
	got, _ := applyLexicon([]Word{{Word: "disc"}, {Word: "tracks"}}, fixes)
	if wordsText(got) != "diss tracks" {
		t.Fatalf("got %q", wordsText(got))
	}
}
