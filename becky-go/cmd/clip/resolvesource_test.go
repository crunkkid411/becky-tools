package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A reel's clips can live anywhere on disk — loading a Vegas EDL is exactly the
// case where the footage is NOT in the folder the library happens to be
// browsing. Before this, resolveSource only accepted paths present in the
// browsed folder's index, so every clip of Jordan's post_constantly edit
// (footage on X:) was unresolvable while the library browsed E:, and Thumb()
// bailed before extracting anything: every clip drew the black "no thumbnail"
// placeholder permanently.
func TestResolveSourceAcceptsFootageOutsideTheBrowsedFolder(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "FLYV9992_convertedsnow2.mp4")
	if err := os.WriteFile(outside, []byte("not really a video"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &App{}
	a.folder = filepath.Join(dir, "some-other-browsed-folder") // index is empty on purpose

	v, ok := a.resolveSource(outside)
	if !ok {
		t.Fatalf("resolveSource(%q) = false; a file that exists on disk is a real source even when it is not in the browsed index", outside)
	}
	if filepath.Clean(v.Path) != filepath.Clean(outside) {
		t.Errorf("resolved Path = %q, want %q", v.Path, outside)
	}
	if v.Name != "FLYV9992_convertedsnow2.mp4" {
		t.Errorf("resolved Name = %q, want the file's base name", v.Name)
	}
}

func TestResolveSourceStillRejectsAPathThatDoesNotExist(t *testing.T) {
	a := &App{}
	if _, ok := a.resolveSource(filepath.Join(t.TempDir(), "no-such-file.mp4")); ok {
		t.Error("resolveSource accepted a path that is not on disk and not in the index")
	}
}
