package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A word-level becky-transcribe JSON: pauses at 0.5s and 3.0s should drive the
// pace-based break, NOT a fixed character count.
const wordJSON = `{
  "file": "talk.mp4",
  "duration": 6.0,
  "words": [
    {"word":"you","start":0.0,"end":0.2},
    {"word":"know","start":0.2,"end":0.4},
    {"word":"what","start":0.4,"end":0.6},
    {"word":"i","start":0.6,"end":0.8},
    {"word":"miss","start":1.6,"end":1.9},
    {"word":"good","start":2.0,"end":2.4},
    {"word":"content","start":5.0,"end":5.6}
  ]
}`

func TestCaptionChunks_PaceBasedAndContiguous(t *testing.T) {
	dir := t.TempDir()
	writeBytes(t, filepath.Join(dir, "talk.mp4"), "video-bytes")
	writeBytes(t, filepath.Join(dir, "talk.transcript.json"), wordJSON)

	app := NewApp()
	app.workDir = t.TempDir()
	if _, err := app.OpenFolder(dir); err != nil {
		t.Fatalf("OpenFolder: %v", err)
	}
	cues, err := app.CaptionChunks("talk.mp4")
	if err != nil {
		t.Fatalf("CaptionChunks: %v", err)
	}
	if len(cues) < 2 {
		t.Fatalf("expected multiple pace-based chunks, got %d: %+v", len(cues), cues)
	}
	// Contiguous: no gaps between consecutive cues (Jordan's no-gap rule).
	for i := 0; i+1 < len(cues); i++ {
		if cues[i+1].Start-cues[i].End > 0.05 {
			t.Errorf("gap between cue %d and %d: %.3f -> %.3f", i, i+1, cues[i].End, cues[i+1].Start)
		}
	}
	// The 1.2s pause (i @0.8 -> miss @1.6) should have broken the first phrase.
	if cues[0].Text != "you know what i" {
		t.Errorf("first chunk = %q, want pace break after 'i'", cues[0].Text)
	}
}

// TestCaptionChunks_AttachesWords: every cue carries its own words with SOURCE
// timings, in order, covering the whole transcript exactly once. The native
// timeline splits a caption on these (Jordan: split by real word timestamps, not a
// guessed character position).
func TestCaptionChunks_AttachesWords(t *testing.T) {
	dir := t.TempDir()
	writeBytes(t, filepath.Join(dir, "talk.mp4"), "video-bytes")
	writeBytes(t, filepath.Join(dir, "talk.transcript.json"), wordJSON)

	app := NewApp()
	app.workDir = t.TempDir()
	if _, err := app.OpenFolder(dir); err != nil {
		t.Fatalf("OpenFolder: %v", err)
	}
	cues, err := app.CaptionChunks("talk.mp4")
	if err != nil {
		t.Fatalf("CaptionChunks: %v", err)
	}
	total := 0
	for _, c := range cues {
		total += len(c.Words)
	}
	if total != 7 {
		t.Errorf("attached %d words across cues, want all 7 exactly once", total)
	}
	if len(cues[0].Words) != 4 || cues[0].Words[0].Word != "you" || cues[0].Words[3].Word != "i" {
		t.Fatalf("cue 0 words = %+v, want [you know what i]", cues[0].Words)
	}
	// The times must be the SOURCE times a split needs — "i" ends at 0.8, the pause
	// before "miss" is where a split near there should land.
	if cues[0].Words[3].Start != 0.6 || cues[0].Words[3].End != 0.8 {
		t.Errorf("'i' timing = %.2f..%.2f, want 0.60..0.80", cues[0].Words[3].Start, cues[0].Words[3].End)
	}
}

func TestCaptionChunks_ErrorsWhenUnresolved(t *testing.T) {
	dir := t.TempDir()
	app := NewApp()
	app.workDir = t.TempDir()
	if _, err := app.OpenFolder(dir); err != nil {
		t.Fatalf("OpenFolder: %v", err)
	}
	// A name that is neither an indexed video nor a real path must ERROR (so the
	// native lane retries until the folder is indexed) - not silently return empty.
	if _, err := app.CaptionChunks("nope.mp4"); err == nil {
		t.Error("CaptionChunks on an unresolved name should error (drives retry), got nil")
	}
}

func writeBytes(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
