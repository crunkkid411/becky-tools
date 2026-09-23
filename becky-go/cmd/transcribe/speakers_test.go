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
	if got := speakerAt(spans, 5.6, 5.8); got != "B" {
		t.Errorf("want B, got %q", got)
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
