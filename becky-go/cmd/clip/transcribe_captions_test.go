package main

// transcribe_captions_test.go covers the captions-aware routing added to
// Transcribe/transcribeOne, fully offline: both the becky-captions exec
// (runCaptions) and the becky-transcribe exec (runTranscribe) are faked. It
// proves the two forensic-critical branches:
//
//   - use_official  → NO local ASR runs, NO "_parakeet_transcription.srt" is written, and any
//     existing official ".en.srt" is left byte-for-byte untouched.
//   - local_needed  → local ASR writes "<stem>_parakeet_transcription.srt" (NOT "<stem>.srt"),
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
// complete official .en.srt is in place) → no _parakeet_transcription is written and the original
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
	if fileExists(filepath.Join(dir, "stream_[ABCDEFGHIJK]_parakeet_transcription.srt")) {
		t.Fatalf("use_official must NOT write a _parakeet_transcription sidecar")
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
// present → local ASR writes "<stem>_parakeet_transcription.srt" and the original .en.srt is
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

	wantLocal := filepath.Join(dir, "stream_[ABCDEFGHIJK]_parakeet_transcription.srt")
	if filepath.Clean(gotOut) != filepath.Clean(wantLocal) {
		t.Fatalf("local ASR output path = %q, want %q", gotOut, wantLocal)
	}
	if !fileExists(wantLocal) {
		t.Fatalf("expected _parakeet_transcription sidecar at %s", wantLocal)
	}
	if after := sha256Of(t, official); after != before {
		t.Fatalf("FORENSIC VIOLATION: original .en.srt changed: %s -> %s", before, after)
	}
}

// TestRetranscribe_BareSrt_NeverOverwritten is the exact-complaint test: a video
// with a BARE "<stem>.srt" (not ".en.srt"), the user hits "re-transcribe" (the ↻
// button), local ASR runs — and the original "<stem>.srt" is byte-for-byte
// unchanged while a SEPARATE "<stem>_parakeet_transcription.srt" is written. The tooltip's old
// "overwrites the .srt" claim was false; this proves it.
func TestRetranscribe_BareSrt_NeverOverwritten(t *testing.T) {
	app, dir, name := newClipFolder(t)

	bare := filepath.Join(dir, "stream_[ABCDEFGHIJK].srt")
	mustWrite(t, bare, cannedSRT)
	before := sha256Of(t, bare)

	// Force local ASR (as a re-transcribe would when the user wants a fresh local
	// pass): captions says local_needed.
	withFakeCaptions(t, captions.Decision{Action: captions.ActionLocalNeeded})

	origRun := runTranscribe
	t.Cleanup(func() { runTranscribe = origRun })
	gotOut := ""
	runTranscribe = func(_ context.Context, _bin, _video, srtOut string) error {
		gotOut = srtOut
		return os.WriteFile(srtOut, []byte("DIFFERENT local ASR content"), 0o644)
	}
	fakeBin := filepath.Join(t.TempDir(), transcribeExeName())
	mustWrite(t, fakeBin, "x")
	t.Setenv("BECKY_TRANSCRIBE", fakeBin)

	if _, err := app.Transcribe(name); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	wantLocal := filepath.Join(dir, "stream_[ABCDEFGHIJK]_parakeet_transcription.srt")
	if filepath.Clean(gotOut) != filepath.Clean(wantLocal) {
		t.Fatalf("local ASR output = %q, want the _parakeet_transcription sidecar %q", gotOut, wantLocal)
	}
	if !fileExists(wantLocal) {
		t.Fatalf("expected a separate _parakeet_transcription sidecar at %s", wantLocal)
	}
	if after := sha256Of(t, bare); after != before {
		t.Fatalf("FORENSIC VIOLATION: re-transcribe modified the original .srt: %s -> %s", before, after)
	}
}

// TestTranscribe_CaptionsAbsent_GoesLocal: with NO becky-captions resolvable, the
// sequence skips straight to local ASR and writes "<stem>_parakeet_transcription.srt".
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
	if !fileExists(filepath.Join(dir, "stream_[ABCDEFGHIJK]_parakeet_transcription.srt")) {
		t.Fatalf("captions-absent should produce a _parakeet_transcription sidecar")
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
	if !fileExists(filepath.Join(dir, "stream_[ABCDEFGHIJK]_parakeet_transcription.srt")) {
		t.Fatalf("captions-error should degrade to a _parakeet_transcription sidecar")
	}
}

// TestLocalSrtSidecarPath confirms the path construction is exactly "<stem>_parakeet_transcription.srt".
func TestLocalSrtSidecarPath(t *testing.T) {
	got := localSrtSidecarPath(filepath.Join("E:", "f", "vid_[ID000000001].mp4"))
	want := filepath.Join("E:", "f", "vid_[ID000000001]_parakeet_transcription.srt")
	if got != want {
		t.Fatalf("localSrtSidecarPath = %q, want %q", got, want)
	}
}

// TestReTranscribe_OfficialPresent_ForcesParakeetSecondaryKeepsOriginal is the
// "↻ re-transcribe" forensic contract: when a video ALREADY indexes as having a
// transcript (an official .en.srt is in place before the folder is opened), hitting
// re-transcribe forces a fresh Parakeet pass — it does NOT short-circuit on the
// official. The result is a SEPARATE "<stem>_parakeet_transcription.srt", the
// becky-captions check is skipped entirely, and the original .en.srt is byte-for-byte
// unchanged. (Contrast TestTranscribe_UseOfficial_NoLocalWritten, where the folder is
// opened BEFORE the official lands, so the video indexes WITHOUT a transcript and the
// caption-first "+" path runs instead.)
func TestReTranscribe_OfficialPresent_ForcesParakeetSecondaryKeepsOriginal(t *testing.T) {
	dir := t.TempDir()
	name := "stream_[ABCDEFGHIJK].mp4"
	mustWrite(t, filepath.Join(dir, name), "not-real-video-bytes")
	// The official transcript is in place BEFORE indexing, so the video indexes as
	// has_transcript=true — exactly the state in which the GUI shows the ↻ button.
	official := filepath.Join(dir, "stream_[ABCDEFGHIJK].en.srt")
	mustWrite(t, official, cannedSRT)
	before := sha256Of(t, official)

	app := NewApp()
	app.workDir = t.TempDir()
	if _, err := app.OpenFolder(dir); err != nil {
		t.Fatalf("OpenFolder: %v", err)
	}
	if v, ok := pickVideo(app.folderView(), name); !ok || !v.HasTranscript {
		t.Fatalf("precondition: video should index WITH a transcript, got %+v", v)
	}

	// A forced re-transcribe must NOT consult becky-captions at all.
	origCap := runCaptions
	t.Cleanup(func() { runCaptions = origCap })
	runCaptions = func(_ context.Context, _bin, _video string, _offline bool) (captions.Decision, error) {
		t.Fatalf("re-transcribe (forceLocal) must NOT call becky-captions")
		return captions.Decision{}, nil
	}
	fakeCapBin := filepath.Join(t.TempDir(), captionsExeName())
	mustWrite(t, fakeCapBin, "x")
	t.Setenv("BECKY_CAPTIONS", fakeCapBin)

	origRun := runTranscribe
	t.Cleanup(func() { runTranscribe = origRun })
	gotOut := ""
	runTranscribe = func(_ context.Context, _bin, _video, srtOut string) error {
		gotOut = srtOut
		return os.WriteFile(srtOut, []byte("fresh parakeet pass content"), 0o644)
	}
	tBin := filepath.Join(t.TempDir(), transcribeExeName())
	mustWrite(t, tBin, "x")
	t.Setenv("BECKY_TRANSCRIBE", tBin)

	if _, err := app.Transcribe(name); err != nil {
		t.Fatalf("Transcribe (re-transcribe): %v", err)
	}

	wantSecondary := filepath.Join(dir, "stream_[ABCDEFGHIJK]_parakeet_transcription.srt")
	if filepath.Clean(gotOut) != filepath.Clean(wantSecondary) {
		t.Fatalf("forced re-transcribe output = %q, want the separate secondary %q", gotOut, wantSecondary)
	}
	if !fileExists(wantSecondary) {
		t.Fatalf("expected a separate parakeet secondary at %s", wantSecondary)
	}
	if after := sha256Of(t, official); after != before {
		t.Fatalf("FORENSIC VIOLATION: re-transcribe modified the original .en.srt: %s -> %s", before, after)
	}
}
