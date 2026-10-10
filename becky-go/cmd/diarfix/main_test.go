package main

import (
	"errors"
	"strings"
	"testing"
)

// The model card's own example: Nemotron-style labels put "today?" with speaker 2 and "you?"
// with speaker 1; the model moves both back. Our words keep their exact spelling.
func TestTransferModelCardExample(t *testing.T) {
	ours := strings.Fields("Hello, how are you doing today? I am doing well. What about you? I'm doing well, too. Thank you.")
	local := strings.Fields("1 1 1 1 1 2 2 2 2 2 2 2 1 1 1 1 1 1 1")
	comp := "<speaker:1> Hello, how are you doing today? <speaker:2> I am doing well. What about you? <speaker:1> I'm doing well, too. Thank you. [eod] junk"
	got, ok := transfer(comp, ours, local)
	if !ok {
		t.Fatal("a faithful reply must be trusted")
	}
	want := strings.Fields("1 1 1 1 1 1 2 2 2 2 2 2 2 1 1 1 1 1 1")
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
}

// The model numbers speakers by first appearance in ITS reply; our "1"/"2" must be matched by
// agreement, not by the digit.
func TestTransferMatchesSpeakersByAgreement(t *testing.T) {
	ours := strings.Fields("so what happened next well we left early okay")
	local := []string{"1", "1", "1", "1", "2", "2", "2", "2", "1"}
	comp := "<speaker:2> so what happened next <speaker:1> well we left early <speaker:2> okay"
	got, ok := transfer(comp, ours, local)
	if !ok || strings.Join(got, "") != "111122221" {
		t.Fatalf("swapped digits must map back: got %v ok=%v", got, ok)
	}
}

func TestTransferRejectsRewrittenReply(t *testing.T) {
	ours := strings.Fields("the quick brown fox jumps over the lazy dog today")
	local := strings.Fields("1 1 1 1 1 2 2 2 2 2")
	if _, ok := transfer("<speaker:1> something else entirely was said here by someone", ours, local); ok {
		t.Fatal("a reply that does not reproduce our words must be ignored")
	}
}

// A dropped word keeps its label; a speaker we never had is never invented.
func TestTransferDroppedWordAndNoNewSpeaker(t *testing.T) {
	ours := strings.Fields("yes I think so right now we go there today")
	local := strings.Fields("1 1 1 1 2 2 2 2 2 2")
	comp := "<speaker:1> yes I think so <speaker:2> right we go there <speaker:3> today" // drops "now"
	got, ok := transfer(comp, ours, local)
	if !ok || strings.Join(got, "") != "1111222222" {
		t.Fatalf("dropped word keeps its label, unknown speaker 3 is not invented: got %v ok=%v", got, ok)
	}
}

func TestUnsureWords(t *testing.T) {
	labels := strings.Fields("A A A A A A A A B B A A A A A A A A A A")
	got := unsureWords(labels, 2)
	want := "......XXXXXX........"
	var b strings.Builder
	for _, u := range got {
		if u {
			b.WriteByte('X')
		} else {
			b.WriteByte('.')
		}
	}
	if b.String() != want {
		t.Fatalf("got  %s\nwant %s", b.String(), want)
	}
}

func TestChunkStaysUnderLimitAndCoversAll(t *testing.T) {
	var text, labels []string
	for i := range 3000 {
		text = append(text, "word")
		labels = append(labels, []string{"S0", "S1"}[(i/37)%2])
	}
	spans := chunk(text, labels, 4000)
	next := 0
	for _, s := range spans {
		if s.from != next {
			t.Fatalf("gap or overlap at %d (span starts %d)", next, s.from)
		}
		if p := s.prompt(text); len(p) > 4000 {
			t.Fatalf("prompt %d chars > 4000", len(p))
		}
		if s.local[0] != "1" || s.back["1"] != labels[s.from] {
			t.Fatalf("span numbering must start at 1 with its first speaker: %+v", s.back)
		}
		next = s.to
	}
	if next != len(text) || len(spans) < 4 {
		t.Fatalf("covered %d of %d words in %d spans", next, len(text), len(spans))
	}
}

// End to end with a fake model: only unsure words may change, and a long monologue is protected
// even when the model wants to relabel it.
func TestCheckOnlyTouchesUnsureWords(t *testing.T) {
	var words []word
	add := func(spk string, ws ...string) {
		for _, w := range ws {
			words = append(words, word{Word: w, Speaker: spk})
		}
	}
	add("SPEAKER_00", strings.Fields("one two three four five six seven eight nine ten how are you doing")...)
	add("SPEAKER_01", strings.Fields("today? I am fine")...)
	fake := func(prompt string, _ int) (string, error) {
		// Move "today?" back to speaker 1 AND (wrongly) give the first word to speaker 2.
		return "<speaker:2> one <speaker:1> two three four five six seven eight nine ten how are you doing today? <speaker:2> I am fine [eod]", nil
	}
	res := check(words, 5, nil, fake, func(string, ...any) {})
	if len(res.Fixes) != 1 || res.Fixes[0].Word != "today?" || res.Fixes[0].To != "SPEAKER_00" {
		t.Fatalf("want exactly the boundary fix, got %+v", res.Fixes)
	}
	if res.Speakers[0] != "SPEAKER_00" {
		t.Fatal("a word far from any speaker change must keep its label")
	}
	if res.ChunksSent != 1 || res.ChunksTrusted != 1 {
		t.Fatalf("chunks sent/trusted = %d/%d", res.ChunksSent, res.ChunksTrusted)
	}
}

func TestCheckDegrades(t *testing.T) {
	words := []word{{Word: "hi", Speaker: "SPEAKER_00"}, {Word: "yo", Speaker: "SPEAKER_01"}}
	res := check(words, 5, errors.New("model not found"), nil, func(string, ...any) {})
	if len(res.Fixes) != 0 || res.Speakers[1] != "SPEAKER_01" || !strings.Contains(res.Note, "model not found") {
		t.Fatalf("missing model must keep labels and say why: %+v", res)
	}
	one := []word{{Word: "hi", Speaker: "SPEAKER_00"}, {Word: "there", Speaker: "SPEAKER_00"}}
	if res := check(one, 5, nil, nil, func(string, ...any) {}); res.ChunksSent != 0 || res.Note == "" {
		t.Fatalf("one voice: nothing to check, got %+v", res)
	}
}
