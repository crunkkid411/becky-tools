package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func sentencesOf(lines ...string) ([]Word, []Sentence) {
	var ws []Word
	var ss []Sentence
	t := 0.0
	for i, l := range lines {
		w0 := len(ws)
		for _, w := range strings.Fields(l) {
			ws = append(ws, Word{Word: w, Start: t, End: t + 0.2})
			t += 0.3
		}
		ss = append(ss, Sentence{ID: i, W0: w0, W1: len(ws) - 1, Text: l})
	}
	return ws, ss
}

// The goodbye-youtube-update "Yeah." answered his own question; it is not filler.
func TestOnlyHesitationSoundsAreFiller(t *testing.T) {
	for text, want := range map[string]bool{"Um": true, "Uh, um.": true, "Yeah.": false, "Alright.": false, "Okay.": false, "And uh yeah.": false} {
		if got := isHesitation(text); got != want {
			t.Errorf("isHesitation(%q) = %v, want %v", text, got, want)
		}
	}
}

// Run 10: the restore pass put the mic check "Talking talking." back.
func TestNotForViewersIsNeverPutBack(t *testing.T) {
	_, ss := sentencesOf("Talking talking.", "What's up guys?", "I'm leaving YouTube.")
	ds := make([]Decision, len(ss))
	for i := range ds {
		ds[i] = Decision{ID: i, Keep: true}
	}
	ds[2].Keep = false // cut for another reason, may go back
	protect := markExceptions(ds, ss, coarseCache{Off: []int{0}}, "test")
	restoreLines(ds, []int{0, 2}, protect, "test")
	if ds[0].Keep || ds[0].Label != "meta" {
		t.Errorf("mic check came back: %+v", ds[0])
	}
	if !ds[2].Keep {
		t.Error("an unprotected cut should still go back")
	}
}

func TestRetakeMustBeRepeatedInTheWords(t *testing.T) {
	_, ss := sentencesOf(
		"I can't believe that things have escalated this",
		"I can't believe how far this has gone.",
		"Some of my videos were restored.",
		"But more threats have come.",
	)
	if !okRetake(ss, retake{Abandoned: 0, Redo: 1}) {
		t.Error("a sentence started again with the same words is a retake")
	}
	if okRetake(ss, retake{Abandoned: 2, Redo: 3}) {
		t.Error("two different sentences are not a retake, whatever the model says")
	}
	if okRetake(ss, retake{Abandoned: 1, Redo: 0}) {
		t.Error("the redo comes after the attempt")
	}
}

func TestRestartInsideASentence(t *testing.T) {
	ws, ss := sentencesOf(
		"I'm gonna try to I'm gonna try not to drag this out.",
		"and he and he tries to hurt me",
		"it's like a very long stream, it's like two hour live stream.",
		"Thank you, guys. Thank you.",
	)
	got := restarts(ws, ss)
	var cut []string
	for _, i := range got {
		cut = append(cut, ws[i].Word)
	}
	if want := "I'm gonna try to and he"; strings.Join(cut, " ") != want {
		t.Errorf("restarts cut %q, want %q", strings.Join(cut, " "), want)
	}
}

func TestProjectIsSavedOverUnlessJordanSavedIt(t *testing.T) {
	dir := t.TempDir()
	veg := filepath.Join(dir, "x-video.veg")
	if p, over := projectPath(veg, dir); p != veg || over {
		t.Fatalf("no project yet: got %s %v", p, over)
	}
	if err := os.WriteFile(veg, []byte("becky"), 0o644); err != nil {
		t.Fatal(err)
	}
	stampProject(veg, dir)
	if p, over := projectPath(veg, dir); p != veg || !over {
		t.Fatalf("becky's own project: got %s %v, want it saved over", p, over)
	}
	if err := os.WriteFile(veg, []byte("Jordan worked on it"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, over := projectPath(veg, dir); over || !strings.HasSuffix(p, "x-video (2).veg") {
		t.Fatalf("Jordan saved it: got %s %v, want a new name", p, over)
	}
}

func TestPartsOverlapAndCoverEverything(t *testing.T) {
	ps := parts(100)
	seen := map[int]bool{}
	for _, p := range ps {
		for i := p[0]; i < p[1]; i++ {
			seen[i] = true
		}
	}
	if len(seen) != 100 || ps[0] != [2]int{0, 40} || ps[1][0] != 32 || !slices.Contains(ps, [2]int{64, 100}) {
		t.Fatalf("parts(100) = %v", ps)
	}
}

func TestChaptersStartAtZeroAndKeepTenSecondsApart(t *testing.T) {
	got := chapterList([]chapter{{3, "Intro"}, {8, "Too close"}, {40, "The strikes"}, {-1, "Cut line"}, {200, "Kickflip"}})
	if len(got) != 3 || got[0].At != 0 || got[1].Title != "The strikes" || got[2].At != 200 {
		t.Fatalf("chapters = %+v", got)
	}
	if chapterList([]chapter{{0, "a"}, {50, "b"}}) != nil {
		t.Fatal("fewer than 3 chapters must give none")
	}
	if p := []span{{10, 20}, {50, 60}}; editTime(p, 55) != 15 || editTime(p, 30) != -1 {
		t.Fatalf("editTime wrong: %v %v", editTime(p, 55), editTime(p, 30))
	}
	if ytClock(3725) != "1:02:05" || ytClock(65) != "1:05" {
		t.Fatal(ytClock(3725), ytClock(65))
	}
}
