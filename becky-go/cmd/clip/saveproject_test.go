package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestSaveProject: first plain Save asks for a name; a named save lands in the
// export folder (<folder>\render) and never overwrites; a later plain Save
// updates the same file.
func TestSaveProject(t *testing.T) {
	app, dir := openFixture(t)
	app.AddClip(filepath.Join(dir, "ring.mp4"), 1, 3, "money")

	if _, err := app.SaveProject(""); !errors.Is(err, errNeedsProjectName) {
		t.Fatalf("first plain save: want errNeedsProjectName, got %v", err)
	}
	p1, err := app.SaveProject("my cut")
	if err != nil {
		t.Fatalf("named save: %v", err)
	}
	if want := filepath.Join(dir, "render", "my cut.reel.json"); p1 != want {
		t.Fatalf("named save path = %q, want %q", p1, want)
	}
	p2, err := app.SaveProject("my cut")
	if err != nil {
		t.Fatalf("second named save: %v", err)
	}
	if want := filepath.Join(dir, "render", "my cut_2.reel.json"); p2 != want {
		t.Fatalf("save-as same name = %q, want %q (no overwrite)", p2, want)
	}
	p3, err := app.SaveProject("")
	if err != nil {
		t.Fatalf("plain save after save-as: %v", err)
	}
	if p3 != p2 {
		t.Fatalf("plain save went to %q, want the current project %q", p3, p2)
	}
	for _, p := range []string{p1, p2} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("project file missing: %v", err)
		}
	}
}
