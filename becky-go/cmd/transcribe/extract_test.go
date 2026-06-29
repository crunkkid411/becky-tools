package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestExtractAudioFillsTimelineGaps is the regression test for the corrupted-source
// timestamp-compression bug (becky-transcribe ending a 2:58:04 video's transcript at
// 2:10 because the source's audio dropped out mid-stream).
//
// It synthesizes a 6-second clip whose AUDIO has a genuine 2-second gap — samples exist
// only in [0,2] and [4,6], with the packets for [2,4] missing — the same shape as a
// yt-dlp-merged livestream VOD whose audio cuts out and comes back. Plain ffmpeg decoding
// concatenates the surviving samples and drops the gap, yielding a ~4 s WAV (every later
// timestamp then lands ~2 s early); the "aresample=async=1:first_pts=0" fill in
// extractAudio inserts silence and restores the full ~6 s, so transcript timestamps stay
// aligned to the video. The assertion is on the VALUE (extracted seconds vs timeline
// seconds), so the pre-fix behavior (~4 s) fails this test and the fix (~6 s) passes it.
//
// It SKIPS (never fails) when ffmpeg — or the lavfi/x264/aac encoders the fixture needs —
// is unavailable, so CI without media tooling stays green; it runs for real on the PC.
func TestExtractAudioFillsTimelineGaps(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH; skipping audio-extraction regression test")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "gappy.mp4")
	// 6 s video; audio present only in [0,2]+[4,6] -> a real 2 s gap in the audio track.
	// The single quotes are ffmpeg's own filtergraph quoting (the expression has commas).
	build := exec.Command(ffmpeg, "-y", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=d=6:s=160x120:r=30",
		"-f", "lavfi", "-i", "sine=f=440:d=6:r=44100",
		"-af", "aselect='between(t,0,2)+between(t,4,6)'",
		"-c:v", "libx264", "-c:a", "aac", "-shortest", src)
	if out, berr := build.CombinedOutput(); berr != nil {
		t.Skipf("could not synthesize gappy fixture (missing lavfi/x264/aac?): %v\n%s", berr, out)
	}

	wav, err := extractAudio(ffmpeg, src)
	if err != nil {
		t.Fatalf("extractAudio: %v", err)
	}
	defer os.Remove(wav)

	// extractAudio writes 16 kHz mono s16le with a canonical 44-byte WAV header, so the
	// duration is (fileSize - header) / bytesPerSecond. The 0.5 s margin absorbs codec
	// priming while keeping the broken ~4.0 s a hard FAIL and the fixed ~6.0 s a PASS.
	const (
		timelineSec = 6.0
		bytesPerSec = 16000 * 2
		wavHeader   = 44
	)
	fi, err := os.Stat(wav)
	if err != nil {
		t.Fatalf("stat extracted wav: %v", err)
	}
	gotSec := float64(fi.Size()-wavHeader) / bytesPerSec

	if gotSec < timelineSec-0.5 {
		t.Fatalf("extracted audio is %.2fs but the clip timeline is %.1fs; the audio gap was dropped instead of filled, which compresses every transcript timestamp (regression of the aresample=async fill in extractAudio)", gotSec, timelineSec)
	}
}
