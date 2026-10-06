package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func cw(words ...string) []Word {
	out := make([]Word, len(words))
	for i, w := range words {
		out[i] = Word{Word: w, Start: float64(i), End: float64(i) + 0.5}
	}
	return out
}

var testKnown = []string{"Hair Jordan", "TakingBack2007", "diss tracks"}

func knownMaps() (map[string]bool, map[string]bool) {
	terms, words := map[string]bool{}, map[string]bool{}
	for _, k := range testKnown {
		terms[strings.Join(normAll(strings.Fields(k)), "")] = true
		for _, w := range strings.Fields(k) {
			words[normWord(w)] = true
		}
	}
	return terms, words
}

func TestCleanupAcceptsLookAlikeSwapsOnly(t *testing.T) {
	terms, words := knownMaps()
	in := cw("I'm", "Harry", "Jordan,", "the", "beautifull", "guy", "in", "2007.")
	answer := strings.Fields("I am Hair Jordan, a beautiful man on 2017.")
	got, fixes := applyCleanupLine(in, answer, terms, words)
	var text []string
	for _, w := range got {
		text = append(text, w.Word)
	}
	// "I'm"->"I am" adds a word (ignored); "the"->"a" is too short; "guy"->"man"
	// is not alike; "in"->"on" is too short; numbers never change.
	if want := "I'm Hair Jordan, the beautiful guy in 2007."; strings.Join(text, " ") != want {
		t.Errorf("text = %q, want %q", strings.Join(text, " "), want)
	}
	if len(fixes) != 2 || fixes[0].Heard != "Harry" || fixes[0].Meant != "Hair" || fixes[1].Meant != "beautiful" {
		t.Errorf("fixes = %+v", fixes)
	}
	for i := range got {
		if got[i].Start != in[i].Start || got[i].End != in[i].End {
			t.Errorf("word %d moved: %+v", i, got[i])
		}
	}
}

func TestCleanupMergesOnlyKnownTerms(t *testing.T) {
	terms, words := knownMaps()
	in := cw("go", "take", "in", "back", "2007", "now")
	got, fixes := applyCleanupLine(in, strings.Fields("go TakingBack2007 now"), terms, words)
	if len(got) != 3 || got[1].Word != "TakingBack2007" || got[1].Start != 1 || got[1].End != 4.5 {
		t.Errorf("merge = %+v", got)
	}
	if len(fixes) != 1 || fixes[0].Heard != "take in back 2007" {
		t.Errorf("fixes = %+v", fixes)
	}
	// An unknown multi-word rewrite is ignored.
	got, fixes = applyCleanupLine(in, strings.Fields("go to take it back in 2007 now"), terms, words)
	if len(got) != len(in) || len(fixes) != 0 {
		t.Errorf("rewrite applied: %+v %+v", got, fixes)
	}
}

func TestCleanupWordsBatchesAndKeepsTimes(t *testing.T) {
	in := cw("Harry", "Jordan", "here.", "Disc", "tracks", "are", "fun.")
	calls := 0
	ask := func(user string, n int) (string, error) {
		calls++
		if n != 2 || !strings.Contains(user, "1| Harry Jordan here.") || !strings.Contains(user, "2| Disc tracks are fun.") {
			return "", fmt.Errorf("unexpected batch %d: %s", n, user)
		}
		b, _ := json.Marshal(map[string]any{"lines": []map[string]any{
			{"n": 1, "text": "Hair Jordan here."}, {"n": 2, "text": "Diss tracks are fun."}}})
		return string(b), nil
	}
	out, fixes, err := cleanupWords(in, testKnown, ask, func(string, ...any) {})
	if err != nil || calls != 1 {
		t.Fatalf("err %v, %d calls", err, calls)
	}
	if out[0].Word != "Hair" || out[3].Word != "Diss" || len(out) != len(in) || len(fixes) != 2 {
		t.Errorf("out = %+v fixes = %+v", out, fixes)
	}
}

func TestCleanupFailureKeepsTheRestAsHeard(t *testing.T) {
	in := cw("one.", "two.")
	out, _, err := cleanupWords(in, testKnown, func(string, int) (string, error) { return "{}", nil }, func(string, ...any) {})
	if err == nil || len(out) != 2 || out[0].Word != "one." {
		t.Errorf("err %v out %+v", err, out)
	}
}

func TestDiffRegions(t *testing.T) {
	got := diffRegions(strings.Fields("a b c d"), strings.Fields("a x c d e"))
	if len(got) != 2 || got[0].i0 != 1 || got[0].i1 != 2 || got[0].j0 != 1 || got[0].j1 != 2 ||
		got[1].i0 != 4 || got[1].i1 != 4 || got[1].j0 != 4 || got[1].j1 != 5 {
		t.Errorf("regions = %+v", got)
	}
	// "Harry" pairs with "Hair" even next to an added word.
	got = diffRegions(strings.Fields("i'm harry jordan"), strings.Fields("i am hair jordan"))
	if len(got) != 1 || len(got[0].ops) != 3 || got[0].ops[2] != (diffOp{1, 2}) {
		t.Errorf("look-alike pairing = %+v", got)
	}
}

func TestCleanupOnlyChangedLinesComeBack(t *testing.T) {
	in := cw("Harry", "Jordan", "here.", "Disc", "tracks", "are", "fun.")
	ask := func(string, int) (string, error) {
		return `{"lines":[{"n":2,"text":"Diss tracks are fun."},{"n":9,"text":"out of range"}]}`, nil
	}
	out, fixes, err := cleanupWords(in, testKnown, ask, func(string, ...any) {})
	if err != nil || len(out) != len(in) || out[0].Word != "Harry" || out[3].Word != "Diss" || len(fixes) != 1 {
		t.Errorf("err %v out %+v fixes %+v", err, out, fixes)
	}
}
