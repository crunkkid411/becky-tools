package main

import (
	"os"
	"path/filepath"
	"testing"
)

// framesFromImage is the --image convention becky-vision already has and
// becky-ocr didn't (becky-AI-Agent-review-1.md F6). It must return exactly
// one frame, with provenance pointing at the image itself, for a real image
// file, and a clear error for a non-image or missing path.
func TestFramesFromImage_OneRealFile(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "photo.jpg")
	if err := os.WriteFile(img, []byte("not a real jpeg, just bytes"), 0o644); err != nil {
		t.Fatalf("write fixture image: %v", err)
	}

	frames, err := framesFromImage(img)
	if err != nil {
		t.Fatalf("framesFromImage(%q) error: %v", img, err)
	}
	if len(frames) != 1 {
		t.Fatalf("len(frames) = %d, want 1", len(frames))
	}
	if frames[0].FramePath != img {
		t.Errorf("FramePath = %q, want %q", frames[0].FramePath, img)
	}
	if frames[0].FrameIndex != 0 {
		t.Errorf("FrameIndex = %d, want 0", frames[0].FrameIndex)
	}
}

func TestFramesFromImage_RejectsNonImageExtension(t *testing.T) {
	dir := t.TempDir()
	notImage := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notImage, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := framesFromImage(notImage); err == nil {
		t.Error("expected an error for a non-image extension, got nil")
	}
}

func TestFramesFromImage_MissingFile(t *testing.T) {
	if _, err := framesFromImage(filepath.Join(t.TempDir(), "does-not-exist.png")); err == nil {
		t.Error("expected an error for a missing file, got nil")
	}
}

// gatherFrames must route to framesFromImage when only imagePath is given,
// leaving the manifest/frames-dir branches untouched.
func TestGatherFrames_ImageBranch(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(img, []byte("bytes"), 0o644); err != nil {
		t.Fatalf("write fixture image: %v", err)
	}

	frames, label, err := gatherFrames("", "", img, false)
	if err != nil {
		t.Fatalf("gatherFrames error: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("len(frames) = %d, want 1", len(frames))
	}
	if label == "" {
		t.Error("sourceLabel is empty")
	}
}
