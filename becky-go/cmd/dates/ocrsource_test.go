package main

import (
	"os"
	"path/filepath"
	"testing"
)

const fixtureOCR = `{
  "tool": "becky-ocr v1.0.0",
  "results": [
    {
      "source_file": "E:/Cases/clip_reencoded.mp4",
      "timestamp": 0.0,
      "lines": [
        {"text": "07/04/2025 6:14 PM", "confidence": 0.97, "category": "candidate_timestamp"},
        {"text": "hello world", "confidence": 0.95, "category": "text"}
      ],
      "low_confidence_lines": [
        {"text": "03/01/2024", "confidence": 0.42, "category": "candidate_timestamp"}
      ]
    }
  ],
  "skipped": [],
  "notes": {}
}`

func TestOCRSource_ReadsCandidateTimestamps(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ocr.json")
	if err := os.WriteFile(p, []byte(fixtureOCR), 0o644); err != nil {
		t.Fatal(err)
	}
	src, err := newOCRSource(p)
	if err != nil {
		t.Fatalf("newOCRSource: %v", err)
	}
	// Matched by basename, regardless of the directory in the query path.
	got := src.BurnedInDates(`/some/other/dir/clip_reencoded.mp4`)
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2 (one asserted + one low-conf)", len(got))
	}
	// The asserted line is present with its confidence.
	var foundStrong bool
	for _, c := range got {
		if c.Text == "07/04/2025 6:14 PM" && c.Confidence == 0.97 {
			foundStrong = true
		}
	}
	if !foundStrong {
		t.Fatalf("expected the asserted 07/04/2025 candidate at conf 0.97, got %+v", got)
	}
	// A file with no OCR entry yields none.
	if n := len(src.BurnedInDates("unrelated.mp4")); n != 0 {
		t.Fatalf("unrelated file got %d candidates, want 0", n)
	}
}

func TestOCRSource_MissingFile(t *testing.T) {
	if _, err := newOCRSource(filepath.Join(t.TempDir(), "does-not-exist.json")); err == nil {
		t.Fatalf("expected an error for a missing ocr.json")
	}
}

func TestIsMedia(t *testing.T) {
	cases := map[string]bool{
		"a.mp4":           true,
		"a.MP4":           true,
		`C:\Cases\b.mov`:  true,
		"notes.txt":       false,
		"clip":            false,
		"/x/y/screen.mkv": true,
	}
	for in, want := range cases {
		if got := isMedia(in); got != want {
			t.Errorf("isMedia(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestExpandInputs_SkipsNonMedia(t *testing.T) {
	dir := t.TempDir()
	media := filepath.Join(dir, "20250704_181431.mp4")
	notes := filepath.Join(dir, "notes.txt")
	for _, f := range []string{media, notes} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files, skipped := expandInputs([]string{media, notes}, false)
	if len(files) != 1 || files[0] != media {
		t.Fatalf("files = %v, want only the mp4", files)
	}
	if len(skipped) != 1 || skipped[0].Reason != "not a media file" {
		t.Fatalf("skipped = %+v, want one non-media skip", skipped)
	}
}
