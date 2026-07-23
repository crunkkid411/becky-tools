package qmdindex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"becky-go/internal/footage"
)

// sampleSRT has one cue at t=0 and a second cue past the 90s window boundary,
// so a converted .md should carry two separate "## [...]" windows.
const sampleSRT = "1\n00:00:00,000 --> 00:00:03,000\nhello world this is a test\n\n" +
	"2\n00:01:35,000 --> 00:01:38,000\nsecond window begins here\n\n"

func writeSRT(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestConvert_WritesFrontmatterAndCues(t *testing.T) {
	dir := t.TempDir()
	srt := filepath.Join(dir, "2026-06-14_My Stream_[abcdefghijk]_parakeet_transcription.srt")
	writeSRT(t, srt, sampleSRT)
	mdDir := filepath.Join(dir, "_md")

	out, err := Convert(srt, mdDir)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if filepath.Dir(out) != mdDir {
		t.Fatalf("md written outside mdDir: %s", out)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	got := string(raw)

	if !strings.Contains(got, `source: "2026-06-14_My Stream_[abcdefghijk]_parakeet_transcription.srt"`) {
		t.Errorf("frontmatter missing verbatim source line:\n%s", got)
	}
	if !strings.Contains(got, `video_id: "abcdefghijk"`) {
		t.Errorf("frontmatter missing video_id from the bracketed token:\n%s", got)
	}
	if !strings.Contains(got, `date: "2026-06-14"`) {
		t.Errorf("frontmatter missing date from the filename prefix:\n%s", got)
	}
	if !strings.Contains(got, "collection: transcripts") {
		t.Errorf("frontmatter missing collection: transcripts (the qmd collection key):\n%s", got)
	}
	if !strings.Contains(got, "**[00:00:00]** hello world this is a test") {
		t.Errorf("first cue missing/misformatted:\n%s", got)
	}
	if !strings.Contains(got, "**[00:01:35]** second window begins here") {
		t.Errorf("second window (>90s gap from the first) missing:\n%s", got)
	}

	// Idempotent: re-converting the same transcript overwrites the same path.
	out2, err := Convert(srt, mdDir)
	if err != nil {
		t.Fatalf("re-Convert: %v", err)
	}
	if out2 != out {
		t.Fatalf("re-Convert wrote a different path: %q vs %q", out2, out)
	}
}

func TestConvert_NoSegmentsErrors(t *testing.T) {
	dir := t.TempDir()
	srt := filepath.Join(dir, "empty.srt")
	writeSRT(t, srt, "")
	if _, err := Convert(srt, filepath.Join(dir, "_md")); err == nil {
		t.Fatal("an empty transcript should error, not write a blank .md into the index")
	}
}

func TestMDPath_DeterministicAndCollisionSafe(t *testing.T) {
	dir := t.TempDir()
	mdDir := filepath.Join(dir, "_md")
	srt := filepath.Join(dir, "video.srt")

	if p1, p2 := MDPath(srt, mdDir), MDPath(srt, mdDir); p1 != p2 {
		t.Fatalf("MDPath not deterministic: %q vs %q", p1, p2)
	}

	// Two very long, differently-suffixed stems that truncate to the same
	// prefix must NOT collide on one .md file (the hash suffix's whole job).
	long1 := filepath.Join(dir, strings.Repeat("a", 300)+"_one.srt")
	long2 := filepath.Join(dir, strings.Repeat("a", 300)+"_two.srt")
	if lp1, lp2 := MDPath(long1, mdDir), MDPath(long2, mdDir); lp1 == lp2 {
		t.Fatalf("two long stems sharing a truncated prefix collided at %q", lp1)
	}
}

func TestSweep_ConvertsMissingSkipsFreshReconvertsStale(t *testing.T) {
	dir := t.TempDir()
	videoSRT := filepath.Join(dir, "video.srt")
	orphanSRT := filepath.Join(dir, "orphan.srt")
	writeSRT(t, videoSRT, sampleSRT)
	writeSRT(t, orphanSRT, sampleSRT)
	mdDir := filepath.Join(dir, "_md")

	index := footage.FolderIndex{
		Videos: []footage.Video{
			{Path: filepath.Join(dir, "video.mp4"), Name: "video.mp4", TranscriptPath: videoSRT, HasTranscript: true},
		},
		Orphans: []footage.OrphanTranscript{
			{Path: orphanSRT, Title: "orphan"},
		},
	}

	converted, errs := Sweep(index, mdDir)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors on first sweep: %v", errs)
	}
	if converted != 2 {
		t.Fatalf("first sweep converted = %d, want 2 (both had no .md yet)", converted)
	}

	// Nothing changed - a re-sweep converts nothing (both are already fresh).
	if converted, errs = Sweep(index, mdDir); len(errs) != 0 || converted != 0 {
		t.Fatalf("re-sweep with no changes = (converted=%d errs=%v), want (0, nil)", converted, errs)
	}

	// Touching the .srt (a re-transcribe) makes ONLY that one stale again.
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(videoSRT, future, future); err != nil {
		t.Fatal(err)
	}
	if converted, errs = Sweep(index, mdDir); len(errs) != 0 || converted != 1 {
		t.Fatalf("sweep after re-transcribe = (converted=%d errs=%v), want (1, nil)", converted, errs)
	}
	// The re-transcribe UPDATES the same 2 locators in place - it must never
	// grow to 3 (a duplicate under a fresh name).
	if entries, err := os.ReadDir(mdDir); err != nil || len(entries) != 2 {
		t.Fatalf(".md files after re-transcribe = %v (err=%v), want exactly 2 (updated in place)", entries, err)
	}
}

// TestSweep_RecognizesExistingLocatorUnderADifferentFilename is the exact
// regression for a real bug found live: mdDir already carries a locator for
// this transcript, but under a DIFFERENT filename (the 2026-07-22 manual
// backfill used its own naming scheme, distinct from MDPath's). Matching only
// by filename made EVERY already-converted transcript look "missing" and
// re-convert it under a second name — measured live, once: 1128 duplicates
// from a single Sweep over the real E:\TakingBack2007 corpus. Matching by the
// frontmatter "source:" field (buildMDIndex) is the fix: an existing locator
// under any name must be recognized and left alone when it is still fresh.
func TestSweep_RecognizesExistingLocatorUnderADifferentFilename(t *testing.T) {
	dir := t.TempDir()
	srtPath := filepath.Join(dir, "video_parakeet_transcription.srt")
	writeSRT(t, srtPath, sampleSRT)
	mdDir := filepath.Join(dir, "_md")
	if err := os.MkdirAll(mdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(mdDir, "some_other_legacy_name_abc123.md")
	writeSRT(t, legacyPath, "---\nsource: \"video_parakeet_transcription.srt\"\ntitle: \"x\"\ncollection: transcripts\n---\n\nold body\n")
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(legacyPath, future, future); err != nil {
		t.Fatal(err)
	}

	index := footage.FolderIndex{
		Videos: []footage.Video{
			{Path: filepath.Join(dir, "video.mp4"), Name: "video.mp4", TranscriptPath: srtPath, HasTranscript: true},
		},
	}

	converted, errs := Sweep(index, mdDir)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if converted != 0 {
		t.Fatalf("converted = %d, want 0 - a fresher locator already exists under a different name", converted)
	}
	entries, err := os.ReadDir(mdDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf(".md files in mdDir = %d, want exactly 1 (no duplicate under a new name): %v", len(entries), names)
	}
}

// TestConvert_UpdatesExistingLocatorInPlaceInsteadOfDuplicating: the same bug,
// exercised through Convert directly (the per-transcript hook's path) — a
// STALE existing locator under a different name gets overwritten IN PLACE.
func TestConvert_UpdatesExistingLocatorInPlaceInsteadOfDuplicating(t *testing.T) {
	dir := t.TempDir()
	srtPath := filepath.Join(dir, "video_parakeet_transcription.srt")
	writeSRT(t, srtPath, sampleSRT)
	mdDir := filepath.Join(dir, "_md")
	if err := os.MkdirAll(mdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(mdDir, "legacy_name_abc123.md")
	writeSRT(t, legacyPath, "---\nsource: \"video_parakeet_transcription.srt\"\ntitle: \"x\"\ncollection: transcripts\n---\n\nold body\n")

	out, err := Convert(srtPath, mdDir)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if out != legacyPath {
		t.Fatalf("Convert wrote %q, want it to update the EXISTING locator %q in place", out, legacyPath)
	}
	entries, err := os.ReadDir(mdDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf(".md files = %d, want 1 (updated, not duplicated)", len(entries))
	}
}

func TestSweep_OneBadTranscriptDoesNotBlockTheRest(t *testing.T) {
	dir := t.TempDir()
	goodSRT := filepath.Join(dir, "good.srt")
	writeSRT(t, goodSRT, sampleSRT)
	mdDir := filepath.Join(dir, "_md")

	index := footage.FolderIndex{
		Videos: []footage.Video{
			{Path: filepath.Join(dir, "missing.mp4"), Name: "missing.mp4", TranscriptPath: filepath.Join(dir, "missing.srt"), HasTranscript: true},
			{Path: filepath.Join(dir, "good.mp4"), Name: "good.mp4", TranscriptPath: goodSRT, HasTranscript: true},
		},
	}

	converted, errs := Sweep(index, mdDir)
	if converted != 1 {
		t.Fatalf("converted = %d, want 1 (the good transcript, despite the missing one)", converted)
	}
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly 1 (for the missing transcript)", errs)
	}
}
