package main

// transcribe_captions_test.go covers the captions-aware routing added to
// Transcribe/transcribeOne, fully offline: both the becky-captions exec
// (runCaptions) and the becky-transcribe exec (runTranscribe) are faked. It
// proves the two forensic-critical branches:
//
//   - use_official  → NO local ASR runs, NO "_LOCAL.srt" is written, and any
//     existing official ".en.srt" is left byte-for-byte untouched.
//   - local_needed  → local ASR writes "<stem>_LOCAL.srt" (NOT "<stem>.srt"),
//     and an existing original ".en.srt" beside it is left byte-for-byte
//     untouched (originals are sacred).
//
// It also covers becky-captions being absent (→ straight to local ASR) and the
// becky-captions exec erroring (→ degrade to local ASR, never block).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"becky-go/internal/captions"
)

// withFakeCaptions swaps runCaptions for a fake that returns the given Decision
// (and counts calls), and points BECKY_CAPTIONS at a real-but-fake binary so
// resolveCaptionsBin succeeds. Restores both after the test.
func withFakeCaptions(t *testing.T, dec captions.Decision) *int {
	t.Helper()
	calls := 0
	orig := runCaptions
	t.Cleanup(func() { runCaptions = orig })
	runCaptions = func(_ context.Context, _bin, _video string, _offline bool) (captions.Decision, error) {
		calls++
		return dec, nil
	}
	fakeBin := filepath.Join(t.TempDir(), captionsExeName())
	mustWrite(t, fakeBin, "not-a-real-binary")
	t.Setenv("BECKY_CAPTIONS", fakeBin)
	return &calls
}

func sha256Of(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// newClipFolder opens an App on a temp folder with one id-tokened video and no
// transcript. Returns the app, the folder, and the video basename.
func newClipFolder(t *testing.T) (*App, string, string) {
	t.Helper()
	dir := t.TempDir()
	name := "stream_[ABCDEFGHIJK].mp4"
	mustWrite(t, filepath.Join(dir, name), "not-real-video-bytes")
	app := NewApp()
	app.workDir = t.TempDir()
	if _, err := app.OpenFolder(dir); err != nil {
		t.Fatalf("OpenFolder: %v", err)
	}
	return app, dir, name
}

// TestTranscribe_UseOfficial_NoLocalWritten: becky-captions says use_official (a
// complete official .en.srt is in place) → no _LOCAL is written and the original
// .en.srt is unchanged.
func TestTranscribe_UseOfficial_NoLocalWritten(t *testing.T) {
	app, dir, name := newClipFolder(t)

	// An official transcript is already beside the video.
	official := filepath.Join(dir, "stream_[ABCDEFGHIJK].en.srt")
	mustWrite(t, official, cannedSRT)
	before := sha256Of(t, official)

	withFakeCaptions(t, captions.Decision{Action: captions.ActionUseOfficial, OfficialSRT: official})

	// runTranscribe must NOT run in this branch.
	transCalls := 0
	origRun := runTranscribe
	t.Cleanup(func() { runTranscribe = origRun })
	runTranscribe = func(_ context.Context, _bin, _video, srtOut string) error {
		transCalls++
		return os.WriteFile(srtOut, []byte(cannedSRT), 0o644)
	}
	fakeBin := filepath.Join(t.TempDir(), transcribeExeName())
	mustWrite(t, fakeBin, "x")
	t.Setenv("BECKY_TRANSCRIBE", fakeBin)

	fv, err := app.Transcribe(name)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if transCalls != 0 {
		t.Fatalf("use_official must NOT run local ASR, ran %d", transCalls)
	}
	if fileExists(filepath.Join(dir, "stream_[ABCDEFGHIJK]_LOCAL.srt")) {
		t.Fatalf("use_official must NOT write a _LOCAL sidecar")
	}
	if after := sha256Of(t, official); after != before {
		t.Fatalf("the original .en.srt was modified: %s -> %s", before, after)
	}
	// And it indexes as having a transcript (the official one).
	if v, ok := pickVideo(fv, name); !ok || !v.HasTranscript {
		t.Fatalf("video should have a transcript after use_official: %+v", v)
	}
}

// TestTranscribe_LocalNeeded_WritesLocalKeepsOriginal: becky-captions says
// local_needed (e.g. the stream was edited) while an original .en.srt is still
// present → local ASR writes "<stem>_LOCAL.srt" and the original .en.srt is
// byte-for-byte unchanged. This is the core forensic proof, in a unit test.
func TestTranscribe_LocalNeeded_WritesLocalKeepsOriginal(t *testing.T) {
	app, dir, name := newClipFolder(t)

	// A SHORT/edited official transcript is present (it would normally make the
	// video index as has_transcript, but captions said local_needed).
	official := filepath.Join(dir, "stream_[ABCDEFGHIJK].en.srt")
	mustWrite(t, official, cannedSRT)
	before := sha256Of(t, official)

	withFakeCaptions(t, captions.Decision{Action: captions.ActionLocalNeeded, Edited: true})

	origRun := runTranscribe
	t.Cleanup(func() { runTranscribe = origRun })
	gotOut := ""
	runTranscribe = func(_ context.Context, _bin, _video, srtOut string) error {
		gotOut = srtOut
		return os.WriteFile(srtOut, []byte(cannedSRT), 0o644)
	}
	fakeBin := filepath.Join(t.TempDir(), transcribeExeName())
	mustWrite(t, fakeBin, "x")
	t.Setenv("BECKY_TRANSCRIBE", fakeBin)

	if _, err := app.Transcribe(name); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	wantLocal := filepath.Join(dir, "stream_[ABCDEFGHIJK]_LOCAL.srt")
	if filepath.Clean(gotOut) != filepath.Clean(wantLocal) {
		t.Fatalf("local ASR output path = %q, want %q", gotOut, wantLocal)
	}
	if !fileExists(wantLocal) {
		t.Fatalf("expected _LOCAL sidecar at %s", wantLocal)
	}
	if after := sha256Of(t, official); after != before {
		t.Fatalf("FORENSIC VIOLATION: original .en.srt changed: %s -> %s", before, after)
	}
}

// TestTranscribe_CaptionsAbsent_GoesLocal: with NO becky-captions resolvable, the
// sequence skips straight to local ASR and writes "<stem>_LOCAL.srt".
func TestTranscribe_CaptionsAbsent_GoesLocal(t *testing.T) {
	app, dir, name := newClipFolder(t)

	// Force resolveCaptionsBin to fail: BECKY_CAPTIONS points at nothing.
	t.Setenv("BECKY_CAPTIONS", filepath.Join(t.TempDir(), "no-captions.exe"))

	// runCaptions must not even be called (resolve fails first); guard anyway.
	origCap := runCaptions
	t.Cleanup(func() { runCaptions = origCap })
	runCaptions = func(_ context.Context, _bin, _video string, _offline bool) (captions.Decision, error) {
		t.Fatalf("runCaptions must not run when becky-captions is unresolved")
		return captions.Decision{}, nil
	}

	origRun := runTranscribe
	t.Cleanup(func() { runTranscribe = origRun })
	runTranscribe = func(_ context.Context, _bin, _video, srtOut string) error {
		return os.WriteFile(srtOut, []byte(cannedSRT), 0o644)
	}
	fakeBin := filepath.Join(t.TempDir(), transcribeExeName())
	mustWrite(t, fakeBin, "x")
	t.Setenv("BECKY_TRANSCRIBE", fakeBin)

	if _, err := app.Transcribe(name); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if !fileExists(filepath.Join(dir, "stream_[ABCDEFGHIJK]_LOCAL.srt")) {
		t.Fatalf("captions-absent should produce a _LOCAL sidecar")
	}
}

// TestTranscribe_CaptionsErrors_DegradesToLocal: becky-captions resolves but its
// exec errors → we still fall back to local ASR (a broken caption check must
// never block making a transcript).
func TestTranscribe_CaptionsErrors_DegradesToLocal(t *testing.T) {
	app, dir, name := newClipFolder(t)

	fakeBin := filepath.Join(t.TempDir(), captionsExeName())
	mustWrite(t, fakeBin, "x")
	t.Setenv("BECKY_CAPTIONS", fakeBin)
	origCap := runCaptions
	t.Cleanup(func() { runCaptions = origCap })
	runCaptions = func(_ context.Context, _bin, _video string, _offline bool) (captions.Decision, error) {
		return captions.Decision{}, context.DeadlineExceeded
	}

	origRun := runTranscribe
	t.Cleanup(func() { runTranscribe = origRun })
	runTranscribe = func(_ context.Context, _bin, _video, srtOut string) error {
		return os.WriteFile(srtOut, []byte(cannedSRT), 0o644)
	}
	tBin := filepath.Join(t.TempDir(), transcribeExeName())
	mustWrite(t, tBin, "x")
	t.Setenv("BECKY_TRANSCRIBE", tBin)

	if _, err := app.Transcribe(name); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if !fileExists(filepath.Join(dir, "stream_[ABCDEFGHIJK]_LOCAL.srt")) {
		t.Fatalf("captions-error should degrade to a _LOCAL sidecar")
	}
}

// TestLocalSrtSidecarPath confirms the path construction is exactly "<stem>_LOCAL.srt".
func TestLocalSrtSidecarPath(t *testing.T) {
	got := localSrtSidecarPath(filepath.Join("E:", "f", "vid_[ID000000001].mp4"))
	want := filepath.Join("E:", "f", "vid_[ID000000001]_LOCAL.srt")
	if got != want {
		t.Fatalf("localSrtSidecarPath = %q, want %q", got, want)
	}
}
