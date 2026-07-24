package sidecar

import (
	"os"
	"path/filepath"
	"testing"
)

// a becky-transcribe JSON with the segments array (the normal output shape).
const beckyJSONWithSegments = `{
  "file": "clip.mp4",
  "duration": 6.0,
  "text": "hello world goodbye",
  "words": [
    {"word": "hello", "start": 0.1, "end": 0.5},
    {"word": "world", "start": 0.6, "end": 1.0},
    {"word": "goodbye", "start": 4.0, "end": 4.6}
  ],
  "segments": [
    {"start": 0.1, "end": 1.0, "text": "hello world"},
    {"start": 4.0, "end": 4.6, "text": "goodbye"}
  ]
}`

func TestParseBeckyJSON_Segments(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "clip.json")
	if err := os.WriteFile(p, []byte(beckyJSONWithSegments), 0o644); err != nil {
		t.Fatal(err)
	}
	sub, err := ParseSubtitle(p)
	if err != nil {
		t.Fatalf("ParseSubtitle: %v", err)
	}
	if sub.Format != "json" {
		t.Errorf("Format = %q, want json", sub.Format)
	}
	if len(sub.Segments) != 2 {
		t.Fatalf("got %d segments, want 2: %+v", len(sub.Segments), sub.Segments)
	}
	if sub.Segments[0].Text != "hello world" || sub.Segments[0].Start != 0.1 || sub.Segments[0].End != 1.0 {
		t.Errorf("seg0 = %+v", sub.Segments[0])
	}
	if sub.Segments[1].Text != "goodbye" {
		t.Errorf("seg1 = %+v", sub.Segments[1])
	}
}

// a words-only becky-transcribe JSON (no segments) must still group into cues.
const beckyJSONWordsOnly = `{
  "file": "clip.mp4",
  "words": [
    {"word": "hello", "start": 0.1, "end": 0.5},
    {"word": "world", "start": 0.6, "end": 1.0},
    {"word": "goodbye", "start": 4.0, "end": 4.6}
  ]
}`

func TestParseBeckyJSON_WordsOnlyGroupsByPause(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "clip.json")
	if err := os.WriteFile(p, []byte(beckyJSONWordsOnly), 0o644); err != nil {
		t.Fatal(err)
	}
	sub, err := ParseSubtitle(p)
	if err != nil {
		t.Fatalf("ParseSubtitle: %v", err)
	}
	// The 3.0s gap before "goodbye" (>0.6s) starts a new segment.
	if len(sub.Segments) != 2 {
		t.Fatalf("got %d segments, want 2 (split on the 3s pause): %+v", len(sub.Segments), sub.Segments)
	}
	if sub.Segments[0].Text != "hello world" || sub.Segments[1].Text != "goodbye" {
		t.Errorf("segments = %+v", sub.Segments)
	}
}

// a .json that is NOT a transcript (a reel, meta, questions file) must be rejected
// so it degrades to "no cues" instead of a false "transcribed" with an empty list.
func TestParseBeckyJSON_RejectsNonTranscript(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "post.reel.json")
	if err := os.WriteFile(p, []byte(`{"name":"post","clips":[{"source":"a.mp4","in":0,"out":2}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSubtitle(p); err == nil {
		t.Error("ParseSubtitle should reject a reel .json (no segments/words), got nil error")
	}
}

func TestFindSubtitle_BeckyJSON(t *testing.T) {
	cases := []struct {
		name    string
		files   []string
		video   string
		wantEnd string // basename of the expected pick, "" = none
	}{
		{"bare stem json", []string{"clip.json"}, "clip.mp4", "clip.json"},
		{"transcript.json", []string{"clip.transcript.json"}, "clip.mp4", "clip.transcript.json"},
		{"srt beats json", []string{"clip.json", "clip.srt"}, "clip.mp4", "clip.srt"},
		// the beckymeta data sidecar must NEVER be mistaken for the transcript.
		{"ignores beckymeta", []string{"clip.mp4.beckymeta.json"}, "clip.mp4", ""},
		{"ignores reel/questions", []string{"clip.reel.json", "clip.questions.json"}, "clip.mp4", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte("{}"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got := FindSubtitle(filepath.Join(dir, tc.video))
			gotBase := ""
			if got != "" {
				gotBase = filepath.Base(got)
			}
			if gotBase != tc.wantEnd {
				t.Errorf("FindSubtitle = %q, want %q", gotBase, tc.wantEnd)
			}
		})
	}
}

func TestIsBeckyDataJSON(t *testing.T) {
	data := []string{"a.mp4.beckymeta.json", "post.reel.json", "post.questions.json",
		"x.info.json", "y.live_chat.json", "post.capstyle.json"}
	for _, n := range data {
		if !IsBeckyDataJSON(n) {
			t.Errorf("IsBeckyDataJSON(%q) = false, want true", n)
		}
	}
	transcripts := []string{"clip.json", "clip.transcript.json", "clip.srt", "clip.json3"}
	for _, n := range transcripts {
		if IsBeckyDataJSON(n) {
			t.Errorf("IsBeckyDataJSON(%q) = true, want false (it's a real transcript)", n)
		}
	}
}
