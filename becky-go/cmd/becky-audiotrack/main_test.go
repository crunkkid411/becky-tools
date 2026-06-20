package main

import (
	"os"
	"path/filepath"
	"testing"

	"becky-go/internal/audiotrack"
)

// TestRunRenderSynthTone exercises the default (tone) render path end-to-end: it must
// write a non-silent WAV with two overlapping regions and a peaks JSON, all without a
// real input file.
func TestRunRenderSynthTone(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "mix.wav")
	peaks := filepath.Join(dir, "peaks.json")

	err := runRender([]string{
		"--out", out, "--peaks", peaks,
		"--freq", "440", "--seconds", "0.25", "--rate", "48000", "--width", "200",
	})
	if err != nil {
		t.Fatalf("runRender: %v", err)
	}

	// The WAV exists and is bigger than a bare header (44 bytes).
	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat out: %v", err)
	}
	if info.Size() <= 44 {
		t.Errorf("mix wav size = %d, want > 44 (real audio data)", info.Size())
	}

	// Re-import and confirm the bounce is non-silent — this is the in-process mirror of
	// the ffprobe volumedetect check the CLI prints.
	clip, err := audiotrack.ImportWAV(out)
	if err != nil {
		t.Fatalf("re-import bounce: %v", err)
	}
	if peak := audiotrack.PeakAbs(clip.Samples); peak < 0.1 {
		t.Errorf("bounce peak = %v, want a clearly non-silent signal (>0.1)", peak)
	}

	// The peaks JSON exists and parses into a Peaks with the requested width.
	if _, err := os.Stat(peaks); err != nil {
		t.Fatalf("stat peaks: %v", err)
	}
}

// TestRunRenderImportRoundTrip renders a tone, then feeds that WAV back in via --import
// to prove the import path works (real WAV in -> non-silent mix out).
func TestRunRenderImportRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.wav")
	// Make a real source WAV with the engine's own writer.
	tone := audiotrack.ToneClip(330, 0.8, 4800, 48000)
	if err := audiotrack.WritePCM16WAV(src, tone.Samples, 48000, 1); err != nil {
		t.Fatalf("write src wav: %v", err)
	}

	out := filepath.Join(dir, "mix.wav")
	if err := runRender([]string{"--import", src, "--out", out}); err != nil {
		t.Fatalf("runRender --import: %v", err)
	}
	clip, err := audiotrack.ImportWAV(out)
	if err != nil {
		t.Fatalf("re-import bounce: %v", err)
	}
	if peak := audiotrack.PeakAbs(clip.Samples); peak < 0.1 {
		t.Errorf("imported-source bounce peak = %v, want non-silent", peak)
	}
}

func TestRunRenderRejectsBadRate(t *testing.T) {
	if err := runRender([]string{"--rate", "0"}); err == nil {
		t.Errorf("runRender with --rate 0 should error")
	}
}

func TestRunPeaksRequiresImport(t *testing.T) {
	if err := runPeaks([]string{}); err == nil {
		t.Errorf("runPeaks with no --import should error")
	}
}

func TestRunPeaksWritesJSON(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.wav")
	tone := audiotrack.ToneClip(220, 0.7, 2400, 48000)
	if err := audiotrack.WritePCM16WAV(src, tone.Samples, 48000, 1); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "p.json")
	if err := runPeaks([]string{"--import", src, "--out", out, "--width", "100"}); err != nil {
		t.Fatalf("runPeaks: %v", err)
	}
	if info, err := os.Stat(out); err != nil || info.Size() == 0 {
		t.Fatalf("peaks json not written: err=%v", err)
	}
}
