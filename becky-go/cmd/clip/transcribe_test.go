package main

// transcribe_test.go covers the becky-transcribe integration WITHOUT shelling the
// real ASR/ffmpeg/GPU: it overrides the runTranscribe seam with a fake that writes
// a canned .srt beside the source (exactly what becky-transcribe --format srt
// --output would produce), then asserts the transcribe→re-index flow flips
// has_transcript and makes the new cues searchable. Also covers TranscribeAll's
// degrade-per-video, Reindex's no-op-when-empty, the binary-not-found typed error,
// and the bridge verbs. Runs under `go test ./cmd/clip/...` on every OS.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cannedSRT is what the fake ASR writes — a valid SRT the indexer's
// sidecar.FindSubtitle resolves and Search can grep ("unlock" is the keyword the
// real end-to-end verify also searches for).
const cannedSRT = "1\r\n00:00:01,000 --> 00:00:03,000\r\nunlock me you guys have to unlock it\r\n\r\n" +
	"2\r\n00:00:04,000 --> 00:00:06,000\r\nthis is the second line\r\n\r\n"

// withFakeTranscribe swaps runTranscribe for a fake that writes cannedSRT to the
// requested sidecar path and a fake binary resolver (BECKY_TRANSCRIBE), restoring
// both after the test. It returns a pointer to a call counter so a test can assert
// how many videos were transcribed.
func withFakeTranscribe(t *testing.T) *int {
	t.Helper()
	calls := 0

	origRun := runTranscribe
	t.Cleanup(func() { runTranscribe = origRun })
	runTranscribe = func(_ context.Context, _bin, _video, srtOut string) error {
		calls++
		return os.WriteFile(srtOut, []byte(cannedSRT), 0o644)
	}

	// Point the binary resolver at a real (any) existing file so resolveTranscribeBin
	// succeeds offline without the actual exe present. The fake run ignores the path.
	fakeBin := filepath.Join(t.TempDir(), transcribeExeName())
	mustWrite(t, fakeBin, "not-a-real-binary")
	t.Setenv("BECKY_TRANSCRIBE", fakeBin)

	return &calls
}

// TestTranscribeOneFlipsHasTranscript is the load-bearing unit proof: a raw video
// with no .srt gets one written beside it, the folder re-indexes, has_transcript
// flips to true, and the new cues are searchable — the whole showstopper fix in
// one offline test.
func TestTranscribeOneFlipsHasTranscript(t *testing.T) {
	calls := withFakeTranscribe(t)
	app, _ := openFixture(t) // ring.mp4 (has .srt) + kitchen.mov (no transcript)

	// kitchen.mov starts WITHOUT a transcript.
	before, ok := pickVideo(app.folderView(), "kitchen.mov")
	if !ok || before.HasTranscript {
		t.Fatalf("precondition: kitchen.mov should start with no transcript, got %+v", before)
	}

	fv, err := app.Transcribe("kitchen.mov")
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("the ASR seam should run exactly once, ran %d", *calls)
	}

	after, ok := pickVideo(fv, "kitchen.mov")
	if !ok || !after.HasTranscript {
		t.Fatalf("kitchen.mov should have a transcript after Transcribe, got %+v", after)
	}

	// The sidecar landed NEXT TO the source (kitchen.srt), never the video.
	srt := filepath.Join(filepath.Dir(after.Path), "kitchen.srt")
	if !fileExists(srt) {
		t.Fatalf("expected sidecar at %s", srt)
	}

	// The new transcript is immediately searchable.
	hits := app.Search("unlock")
	found := false
	for _, h := range hits {
		if h.Name == "kitchen.mov" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the freshly-transcribed cue should be searchable; hits=%+v", hits)
	}
}

// TestTranscribeUnknownVideoErrors: a basename not in the open folder is a clear
// error, not a panic.
func TestTranscribeUnknownVideoErrors(t *testing.T) {
	withFakeTranscribe(t)
	app, _ := openFixture(t)
	if _, err := app.Transcribe("nope.mp4"); err == nil {
		t.Error("Transcribe of an unindexed video should error")
	}
}

// TestTranscribeAllOnlyMissingAndCounts: TranscribeAll transcribes ONLY videos
// lacking a transcript (ring.mp4 already has one), re-indexes, and reports the
// counts; all videos end up with transcripts.
func TestTranscribeAllOnlyMissingAndCounts(t *testing.T) {
	calls := withFakeTranscribe(t)
	app, _ := openFixture(t) // 2 videos: ring.mp4 (has), kitchen.mov (missing)

	res, err := app.TranscribeAll()
	if err != nil {
		t.Fatalf("TranscribeAll: %v", err)
	}
	if res.Transcribed != 1 || res.Failed != 0 {
		t.Fatalf("want 1 transcribed / 0 failed (only kitchen.mov was missing), got %+v", res)
	}
	if *calls != 1 {
		t.Fatalf("only the missing video should be transcribed, ran %d", *calls)
	}
	for _, v := range res.Folder.Videos {
		if !v.HasTranscript {
			t.Errorf("after TranscribeAll, %s should have a transcript", v.Name)
		}
	}
}

// TestTranscribeAllDegradesOnFailure: one video's ASR failure is recorded in
// Errors and counted as Failed; the batch still completes (degrade-never-crash)
// and re-indexes.
func TestTranscribeAllDegradesOnFailure(t *testing.T) {
	app, _ := openFixture(t)

	// Add a third video missing a transcript so two are pending.
	mustWrite(t, filepath.Join(app.folder, "garage.mp4"), "x")
	app.Reindex()

	origRun := runTranscribe
	t.Cleanup(func() { runTranscribe = origRun })
	runTranscribe = func(_ context.Context, _bin, video, srtOut string) error {
		if strings.Contains(video, "garage") {
			return &TranscribeError{msg: "fake ASR failure for garage.mp4"}
		}
		return os.WriteFile(srtOut, []byte(cannedSRT), 0o644)
	}
	fakeBin := filepath.Join(t.TempDir(), transcribeExeName())
	mustWrite(t, fakeBin, "x")
	t.Setenv("BECKY_TRANSCRIBE", fakeBin)

	res, err := app.TranscribeAll()
	if err != nil {
		t.Fatalf("TranscribeAll should not hard-error when one video fails: %v", err)
	}
	if res.Transcribed != 1 || res.Failed != 1 {
		t.Fatalf("want 1 done / 1 failed, got %+v", res)
	}
	if len(res.Errors) != 1 || res.Errors[0].Name != "garage.mp4" {
		t.Fatalf("the failed video should be reported by name, got %+v", res.Errors)
	}
}

// TestTranscribeBinNotFoundIsTypedError: with no resolvable binary, Transcribe
// returns the *TranscribeError typed message — never a panic.
func TestTranscribeBinNotFoundIsTypedError(t *testing.T) {
	app, _ := openFixture(t)
	// Force resolution to fail: a BECKY_TRANSCRIBE that does not exist. (PATH may or
	// may not have a real becky-transcribe on the dev box; the explicit bad override
	// short-circuits before PATH so the test is deterministic.)
	t.Setenv("BECKY_TRANSCRIBE", filepath.Join(t.TempDir(), "does-not-exist.exe"))

	_, err := app.Transcribe("kitchen.mov")
	if err == nil {
		t.Fatal("expected an error when becky-transcribe can't be located")
	}
	var te *TranscribeError
	if !asTranscribeError(err, &te) {
		t.Fatalf("error should be a *TranscribeError, got %T: %v", err, err)
	}
}

// TestReindexNoFolderIsNoOp: Reindex with no folder open returns an empty view,
// not a crash.
func TestReindexNoFolderIsNoOp(t *testing.T) {
	app := NewApp()
	app.workDir = t.TempDir()
	fv := app.Reindex()
	if len(fv.Videos) != 0 || fv.Root != "" {
		t.Fatalf("Reindex with no folder open should be an empty no-op, got %+v", fv)
	}
}

// TestReindexPicksUpExternalSidecar: a sidecar written out-of-band (not via
// Transcribe) is picked up on Reindex — proving the re-walk is real.
func TestReindexPicksUpExternalSidecar(t *testing.T) {
	app, dir := openFixture(t)
	// kitchen.mov has no transcript; drop one beside it directly.
	mustWrite(t, filepath.Join(dir, "kitchen.srt"), cannedSRT)

	fv := app.Reindex()
	v, ok := pickVideo(fv, "kitchen.mov")
	if !ok || !v.HasTranscript {
		t.Fatalf("Reindex should pick up the external kitchen.srt, got %+v", v)
	}
}

// TestBridgeTranscribeVerbs checks the default-deny dispatch wires transcribe /
// transcribe_all / reindex to their App methods and returns the {ok,data}
// envelope.
func TestBridgeTranscribeVerbs(t *testing.T) {
	withFakeTranscribe(t)
	app, _ := openFixture(t)

	// transcribe {name}
	r := callEnv(t, app, "transcribe", `{"name":"kitchen.mov"}`)
	if !r.OK {
		t.Fatalf("transcribe verb failed: %s", r.Error)
	}
	var fv FolderView
	remarshal(t, r.Data, &fv)
	if v, ok := pickVideo(fv, "kitchen.mov"); !ok || !v.HasTranscript {
		t.Fatalf("transcribe verb should flip has_transcript: %+v", fv.Videos)
	}

	// reindex {} returns a FolderView
	r = callEnv(t, app, "reindex", `{}`)
	if !r.OK {
		t.Fatalf("reindex verb failed: %s", r.Error)
	}

	// transcribe_all {} returns the result envelope (nothing left missing now).
	r = callEnv(t, app, "transcribe_all", `{}`)
	if !r.OK {
		t.Fatalf("transcribe_all verb failed: %s", r.Error)
	}
	var tar TranscribeAllResult
	remarshal(t, r.Data, &tar)
	if tar.Failed != 0 {
		t.Fatalf("transcribe_all should not report failures here: %+v", tar)
	}
}

// --- small test helpers ---

// pickVideo finds a video in a FolderView by basename.
func pickVideo(fv FolderView, name string) (VideoView, bool) {
	for _, v := range fv.Videos {
		if v.Name == name {
			return v, true
		}
	}
	return VideoView{}, false
}

// asTranscribeError reports whether err is (or wraps) a *TranscribeError.
func asTranscribeError(err error, target **TranscribeError) bool {
	if te, ok := err.(*TranscribeError); ok {
		*target = te
		return true
	}
	return false
}
