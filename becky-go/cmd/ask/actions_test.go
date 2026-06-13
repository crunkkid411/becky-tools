// actions_test.go — proves FEATURE 2 (quick-action button -> the EXACT becky-*
// command on the target, no typing). Each test asserts the precise argv a row
// would run, so the mapping is verifiable without executing anything.
package main

import (
	"strings"
	"testing"
)

// fileTarget builds a single-file Target for an existing temp file with ext.
func fileTarget(t *testing.T, ext string) Target {
	t.Helper()
	dir := t.TempDir()
	return resolveTarget([]string{makeFile(t, dir, "clip"+ext)})
}

func TestQuickActions_VideoOffersTheFiveOps(t *testing.T) {
	// Arrange — a dropped video.
	tgt := fileTarget(t, ".mp4")

	// Act
	got := quickActionsFor(tgt)

	// Assert — Transcribe/Identify/Describe/Cut apply to a raw video; OCR does NOT
	// (it needs frames/a folder). So a video offers exactly four rows.
	ids := map[actionID]bool{}
	for _, a := range got {
		ids[a.ID] = true
	}
	for _, want := range []actionID{actTranscribe, actIdentify, actDescribe, actCut} {
		if !ids[want] {
			t.Errorf("video target should offer %q", want)
		}
	}
	if ids[actOCR] {
		t.Errorf("OCR should NOT be offered for a raw video (it reads frames/a folder)")
	}
}

func TestCommandFor_TranscribeBuildsExactArgv(t *testing.T) {
	// Arrange
	tgt := fileTarget(t, ".mp4")
	a, _ := actionByID(actTranscribe)

	// Act
	cmd := commandFor(a, tgt)

	// Assert — the exact command a button press WOULD run.
	want := []string{"becky-transcribe", tgt.Primary()}
	if !equalArgv(cmd, want) {
		t.Errorf("Transcribe command = %v, want %v", cmd, want)
	}
}

func TestCommandFor_IdentifyAddsKB(t *testing.T) {
	tgt := fileTarget(t, ".mp4")
	a, _ := actionByID(actIdentify)
	cmd := commandFor(a, tgt)
	want := []string{"becky-identify", tgt.Primary(), "--kb", "kb-final"}
	if !equalArgv(cmd, want) {
		t.Errorf("Identify command = %v, want %v", cmd, want)
	}
}

func TestCommandFor_DescribeUsesValidateBackend(t *testing.T) {
	tgt := fileTarget(t, ".mp4")
	a, _ := actionByID(actDescribe)
	cmd := commandFor(a, tgt)
	want := []string{"becky-validate", tgt.Primary(), "--backend", "gemma4-local"}
	if !equalArgv(cmd, want) {
		t.Errorf("Describe command = %v, want %v", cmd, want)
	}
}

func TestCommandFor_CutBuildsArgv(t *testing.T) {
	tgt := fileTarget(t, ".mp4")
	a, _ := actionByID(actCut)
	cmd := commandFor(a, tgt)
	want := []string{"becky-cut", tgt.Primary()}
	if !equalArgv(cmd, want) {
		t.Errorf("Cut command = %v, want %v", cmd, want)
	}
}

func TestCommandFor_OCROnFolderUsesFramesDir(t *testing.T) {
	// Arrange — OCR applies to a folder (of frames).
	dir := t.TempDir()
	tgt := resolveTarget([]string{dir})
	a, _ := actionByID(actOCR)

	// Act
	cmd := commandFor(a, tgt)

	// Assert
	want := []string{"becky-ocr", "--frames-dir", dir}
	if !equalArgv(cmd, want) {
		t.Errorf("OCR(folder) command = %v, want %v", cmd, want)
	}
}

func TestCommandFor_OCRDoesNotApplyToRawVideo(t *testing.T) {
	// Arrange — a raw video is not OCR-able directly.
	tgt := fileTarget(t, ".mp4")
	a, _ := actionByID(actOCR)

	// Act
	cmd := commandFor(a, tgt)

	// Assert — no command is built (the gate refuses).
	if cmd != nil {
		t.Errorf("OCR on a raw video should build no command, got %v", cmd)
	}
}

func TestQuickActionsFor_NoTargetIsEmpty(t *testing.T) {
	if got := quickActionsFor(Target{}); got != nil {
		t.Errorf("no target should offer no actions, got %v", got)
	}
}

func TestCommandString_QuotesSpaces(t *testing.T) {
	cmd := []string{"becky-transcribe", `C:\My Cases\clip 1.mp4`}
	got := commandString(cmd)
	if !strings.Contains(got, `"C:\My Cases\clip 1.mp4"`) {
		t.Errorf("commandString should quote a path with spaces; got %q", got)
	}
}

// equalArgv compares two argv slices for exact equality.
func equalArgv(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
