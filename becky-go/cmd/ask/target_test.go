// target_test.go — proves FEATURE 1 (drag-and-drop / argv path -> Target). These
// tests use REAL temp files/dirs so the existence-checking resolution is exercised
// honestly (no mocking of the filesystem), which is exactly how a dropped path
// behaves on Windows.
package main

import (
	"os"
	"path/filepath"
	"testing"
)

// makeFile creates an empty temp file with the given name and returns its path.
func makeFile(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func TestResolveTarget_SingleFileSetsTarget(t *testing.T) {
	// Arrange — a real video-like file, the canonical "dragged a file" case.
	dir := t.TempDir()
	clip := makeFile(t, dir, "clip.mp4")

	// Act
	tgt := resolveTarget([]string{clip})

	// Assert — it becomes the Target, the primary path is the file, and the label
	// is the base name. This is the drag-drop->target proof.
	if !tgt.HasTarget() {
		t.Fatalf("expected a target for an existing file %q", clip)
	}
	if tgt.Kind != targetFile {
		t.Errorf("Kind = %v, want targetFile", tgt.Kind)
	}
	if tgt.Primary() != clip {
		t.Errorf("Primary() = %q, want %q", tgt.Primary(), clip)
	}
	if tgt.Label() != "clip.mp4" {
		t.Errorf("Label() = %q, want clip.mp4", tgt.Label())
	}
	if !tgt.IsVideoLike() {
		t.Errorf("clip.mp4 should be video-like")
	}
}

func TestResolveTarget_FolderSupported(t *testing.T) {
	// Arrange — "or even an entire folder".
	dir := t.TempDir()

	// Act
	tgt := resolveTarget([]string{dir})

	// Assert — a folder is a valid target (kind=dir), labelled as a folder.
	if !tgt.HasTarget() || tgt.Kind != targetDir {
		t.Fatalf("expected a folder target, got kind=%v has=%v", tgt.Kind, tgt.HasTarget())
	}
	if got := tgt.Label(); len(got) < 6 || got[:6] != "folder" {
		t.Errorf("folder Label() = %q, want a 'folder …' label", got)
	}
}

func TestResolveTarget_MultipleFiles(t *testing.T) {
	// Arrange — "support … multiple files".
	dir := t.TempDir()
	a := makeFile(t, dir, "a.mp4")
	b := makeFile(t, dir, "b.mp4")
	c := makeFile(t, dir, "c.mp4")

	// Act
	tgt := resolveTarget([]string{a, b, c})

	// Assert — all three kept, kind=multi.
	if tgt.Kind != targetMulti {
		t.Fatalf("Kind = %v, want targetMulti", tgt.Kind)
	}
	if len(tgt.Paths) != 3 {
		t.Errorf("kept %d paths, want 3", len(tgt.Paths))
	}
}

func TestResolveTarget_QuotedPathWithSpaces(t *testing.T) {
	// Arrange — Windows wraps dropped paths with spaces in double quotes.
	dir := t.TempDir()
	clip := makeFile(t, dir, "my clip.mp4")
	quoted := `"` + clip + `"`

	// Act
	tgt := resolveTarget([]string{quoted})

	// Assert — the quotes are stripped and the file resolves.
	if !tgt.HasTarget() || tgt.Primary() != clip {
		t.Errorf("quoted path did not resolve: Primary()=%q want %q", tgt.Primary(), clip)
	}
}

func TestResolveTarget_MissingPathRecorded(t *testing.T) {
	// Arrange — a path that does not exist must NOT become a fake target.
	missing := filepath.Join(t.TempDir(), "does-not-exist.mp4")

	// Act
	tgt := resolveTarget([]string{missing})

	// Assert — no target, and the bad arg is recorded for an honest note.
	if tgt.HasTarget() {
		t.Errorf("a non-existent path should not set a target")
	}
	if len(tgt.Missing) != 1 {
		t.Errorf("expected the missing path to be recorded, got %v", tgt.Missing)
	}
}

func TestResolveTarget_FolderWinsOverFiles(t *testing.T) {
	// Arrange — dropping a folder + loose files: the folder is the broader context.
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	loose := makeFile(t, dir, "loose.mp4")

	// Act
	tgt := resolveTarget([]string{sub, loose})

	// Assert — folder wins.
	if tgt.Kind != targetDir || tgt.Primary() != sub {
		t.Errorf("folder should win: kind=%v primary=%q", tgt.Kind, tgt.Primary())
	}
}

func TestTarget_ImageVsVideoClassification(t *testing.T) {
	dir := t.TempDir()
	img := resolveTarget([]string{makeFile(t, dir, "frame.png")})
	vid := resolveTarget([]string{makeFile(t, dir, "clip.mov")})

	if !img.IsImageLike() || img.IsVideoLike() {
		t.Errorf("frame.png should be image-like, not video-like")
	}
	if !vid.IsVideoLike() || vid.IsImageLike() {
		t.Errorf("clip.mov should be video-like, not image-like")
	}
}
