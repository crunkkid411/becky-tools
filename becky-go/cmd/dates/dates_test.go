package main

import (
	"os"
	"path/filepath"
	"testing"

	"becky-go/internal/datetri"
	"becky-go/internal/exifmeta"
)

// fakeTS is an in-memory TimestampSource for testing the OCR seam without files.
type fakeTS struct {
	dates []datetri.OCRDateCandidate
}

func (f fakeTS) BurnedInDates(string) []datetri.OCRDateCandidate { return f.dates }

// TestDateClip_FilenamePlusOCR_Documented exercises the full dateClip gather +
// triangulate path: a filename date token (medium) plus a strong burned-in OCR
// date on the same day -> DOCUMENTED. exiftool/ffprobe are absent in CI, so this
// asserts the model-free signals fuse correctly with no hardware.
func TestDateClip_FilenamePlusOCR_Documented(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "20250704_181431.mp4")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := fakeTS{dates: []datetri.OCRDateCandidate{
		{Text: "07/04/2025 6:14 PM", Confidence: 0.97, FrameTimestamp: 0},
	}}
	ex := exifmeta.NewExtractor("/nonexistent-exiftool", "/nonexistent-ffprobe")

	r := dateClip(ex, file, ts, 0.80, 1)

	if r.VerdictDate != "2025-07-04" {
		t.Fatalf("verdict_date = %q, want 2025-07-04", r.VerdictDate)
	}
	if r.Status != string(datetri.StatusDocumented) {
		t.Fatalf("status = %q, want DOCUMENTED (filename + strong OCR agree)", r.Status)
	}
	if r.SingleSignal {
		t.Fatalf("single_signal = true, want false (two signals)")
	}
	// Both the filename and OCR signal must appear in the verdict.
	var sawFilename, sawOCR bool
	for _, s := range r.Signals {
		if s.Source == datetri.SourceFilename {
			sawFilename = true
		}
		if s.Source == datetri.SourceOCR {
			sawOCR = true
			if s.OCRConfidence != 0.97 {
				t.Errorf("ocr_confidence = %v, want 0.97", s.OCRConfidence)
			}
		}
	}
	if !sawFilename || !sawOCR {
		t.Fatalf("signals missing filename(%v) or ocr(%v): %+v", sawFilename, sawOCR, r.Signals)
	}
}

// TestDateClip_NoSignals_DegradesNotPanic confirms the worst case (no readable
// metadata, no filename token, no OCR) returns a result, not a crash.
func TestDateClip_NoSignals_DegradesNotPanic(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "random_clip.mp4")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ex := exifmeta.NewExtractor("/nonexistent-exiftool", "/nonexistent-ffprobe")

	r := dateClip(ex, file, nil, 0.80, 1)

	if r.Status != string(datetri.StatusUnknown) {
		t.Fatalf("status = %q, want UNKNOWN", r.Status)
	}
	if r.VerdictDate != "" {
		t.Fatalf("verdict_date = %q, want empty", r.VerdictDate)
	}
	// Must never emit nil slices in JSON-facing fields.
	if r.Signals == nil || r.Conflicts == nil || r.Notes == nil {
		t.Fatalf("nil slice in result: %+v", r)
	}
}
