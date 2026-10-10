package main

import (
	"errors"
	"strings"
	"testing"
)

const twoSpeakerDiarize = `{"file":"x.wav","duration":10,"speakers":[
 {"id":"SPEAKER_00","segments":[{"start":0,"end":4.0,"confidence":1}]},
 {"id":"SPEAKER_01","segments":[{"start":4.2,"end":9,"confidence":1}]}]}`

func withDiarize(t *testing.T, out string, err error) {
	t.Helper()
	old := runDiarize
	runDiarize = func(string, int) ([]byte, error) { return []byte(out), err }
	t.Cleanup(func() { runDiarize = old })
}

// The regression: every line of a two-person clip must carry WHO said it, and a caption line
// must break where the speaker changes even with no pause between the words.
func TestLabelSpeakersSplitsLinesAtSpeakerChange(t *testing.T) {
	withDiarize(t, twoSpeakerDiarize, nil)
	words := []Word{
		{Word: "hello", Start: 1.0, End: 1.4},
		{Word: "there", Start: 1.5, End: 3.9},
		{Word: "hi", Start: 4.0, End: 4.3}, // mostly in SPEAKER_01's span, no pause before it
		{Word: "back", Start: 4.3, End: 4.6},
	}
	n, note := labelSpeakers(words, "x.wav", 0)
	if n != 2 || note != "" {
		t.Fatalf("want 2 speakers and no note, got %d %q", n, note)
	}
	segs := segmentize(words)
	if len(segs) != 2 {
		t.Fatalf("want 2 lines (one per speaker), got %d: %+v", len(segs), segs)
	}
	if segs[0].Speaker != "SPEAKER_00" || segs[0].Text != "hello there" {
		t.Errorf("line 1 = %+v", segs[0])
	}
	if segs[1].Speaker != "SPEAKER_01" || segs[1].Text != "hi back" {
		t.Errorf("line 2 = %+v", segs[1])
	}
	srt, _ := render(Output{Segments: segs, Speakers: 2}, "srt")
	if !strings.Contains(srt, "SPEAKER_01: hi back") {
		t.Errorf("srt must show the speaker, got:\n%s", srt)
	}
}

// A word in a pause between spans takes the nearest speaker instead of going unlabelled.
func TestSpeakerAtGapTakesNearest(t *testing.T) {
	spans := []speakerSpan{{0, 4, "A"}, {6, 9, "B"}}
	if got := speakerAt(spans, 5.6, 5.8, ""); got != "B" {
		t.Errorf("want B, got %q", got)
	}
}

// Overlapping speech (festival clip, 2026-09-24): B's turn runs 20-25s while A cuts in at 21-22s,
// so both cover "boys come" fully. The tie must stay with the line's speaker (B), not flip to A
// because "A" sorts first; a word only A covers still goes to A.
func TestSpeakerAtOverlapTieKeepsPreviousSpeaker(t *testing.T) {
	spans := []speakerSpan{{20, 25, "B"}, {21, 22, "A"}, {30, 31, "A"}}
	if got := speakerAt(spans, 21.4, 21.8, "B"); got != "B" {
		t.Errorf("tie inside overlap: want B (previous speaker), got %q", got)
	}
	if got := speakerAt(spans, 21.4, 21.8, ""); got != "A" {
		t.Errorf("tie with no previous speaker: want A (lowest id), got %q", got)
	}
	if got := speakerAt(spans, 30.2, 30.6, "B"); got != "A" {
		t.Errorf("only A talks here: want A, got %q", got)
	}
	// A zero-length ASR word ("come" at 21.84-21.84) overlaps nothing; inside both spans it stays with B.
	if got := speakerAt(spans, 21.84, 21.84, "B"); got != "B" {
		t.Errorf("zero-length word inside overlap: want B, got %q", got)
	}
}

// Degrade, never crash: a failed speaker pass leaves words unlabelled with a plain note.
func TestLabelSpeakersDegrades(t *testing.T) {
	withDiarize(t, "", errors.New("becky-diarize not found"))
	words := []Word{{Word: "hi", Start: 0, End: 1}}
	n, note := labelSpeakers(words, "x.wav", 0)
	if n != 0 || !strings.Contains(note, "not labelled") || words[0].Speaker != "" {
		t.Errorf("got n=%d note=%q speaker=%q", n, note, words[0].Speaker)
	}
}

func TestSidecarPath(t *testing.T) {
	if got := sidecarPath(`E:\case\interview.mp4`); got != `E:\case\interview.transcript.json` {
		t.Errorf("got %q", got)
	}
}

func withDiarFix(t *testing.T, out string, err error) {
	t.Helper()
	old := runDiarFix
	runDiarFix = func(string) ([]byte, error) { return []byte(out), err }
	t.Cleanup(func() { runDiarFix = old })
}

// The wording check's corrections land on the words, so the caption line moves with them.
func TestCheckSpeakersAppliesFixesBeforeSegmenting(t *testing.T) {
	withDiarFix(t, `{"speakers":["SPEAKER_00","SPEAKER_00","SPEAKER_00","SPEAKER_01"],
		"fixes":[{"start":2,"end":2.5,"word":"today?","from":"SPEAKER_01","to":"SPEAKER_00"}],"unsure_words":4}`, nil)
	words := []Word{
		{Word: "how", Start: 0, End: 0.5, Speaker: "SPEAKER_00"},
		{Word: "are", Start: 0.6, End: 1, Speaker: "SPEAKER_00"},
		{Word: "today?", Start: 1.1, End: 1.5, Speaker: "SPEAKER_01"},
		{Word: "fine", Start: 1.6, End: 2, Speaker: "SPEAKER_01"},
	}
	fixes, line := checkSpeakers(words)
	if len(fixes) != 1 || words[2].Speaker != "SPEAKER_00" || !strings.Contains(line, "moved 1") {
		t.Fatalf("fix not applied: %+v %q %+v", fixes, line, words)
	}
	if segs := segmentize(words); len(segs) != 2 || segs[0].Text != "how are today?" {
		t.Fatalf("lines must follow the corrected labels: %+v", segs)
	}
}

func TestCheckSpeakersDegrades(t *testing.T) {
	withDiarFix(t, "", errors.New("becky-diarfix not found"))
	words := []Word{{Word: "a", Speaker: "SPEAKER_00"}, {Word: "b", Speaker: "SPEAKER_01"}}
	fixes, line := checkSpeakers(words)
	if fixes != nil || words[1].Speaker != "SPEAKER_01" || !strings.Contains(line, "not found") {
		t.Fatalf("a failed check must keep the sound-only labels and say why: %+v %q", words, line)
	}
}
