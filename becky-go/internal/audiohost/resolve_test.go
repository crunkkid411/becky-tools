package audiohost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveHost_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "becky-audio-host-custom.exe")
	if err := os.WriteFile(exe, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BECKY_AUDIO_HOST", exe)

	got, ok := ResolveHost()
	if !ok {
		t.Fatal("expected resolve to succeed via env override")
	}
	if got != exe {
		t.Errorf("resolved %q, want %q", got, exe)
	}
}

func TestResolveHost_EnvMissingFileIgnored(t *testing.T) {
	// Env points at a nonexistent file: the override is skipped, not fatal.
	t.Setenv("BECKY_AUDIO_HOST", filepath.Join(t.TempDir(), "nope.exe"))
	got, _ := resolveHostVerbose()
	// It may still find a real build output on this machine, but a nonexistent
	// env path must never be the resolved one.
	if got != "" && filepath.Base(got) == "nope.exe" {
		t.Errorf("should not resolve a nonexistent env path, got %q", got)
	}
}

func TestResolveHost_RecordsSearchedPaths(t *testing.T) {
	t.Setenv("BECKY_AUDIO_HOST", filepath.Join(t.TempDir(), "absent.exe"))
	_, searched := resolveHostVerbose()
	if len(searched) == 0 {
		t.Error("expected searched paths to be recorded for the error message")
	}
}

func TestOpen_NotFoundError(t *testing.T) {
	t.Setenv("BECKY_AUDIO_HOST", filepath.Join(t.TempDir(), "absent.exe"))

	// On a clean machine no becky-audio-host is resolvable, so Open returns a
	// typed NotFoundError. If a real host happens to be installed next to the
	// test binary, skip rather than fail.
	c, err := Open(context.Background())
	if err == nil {
		c.Close()
		t.Skip("a real becky-audio-host was resolvable in this environment; skipping NotFound assertion")
	}
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("expected *NotFoundError, got %T: %v", err, err)
	}
	if len(nf.Searched) == 0 {
		t.Error("NotFoundError should list searched paths")
	}
	if msg := nf.Error(); msg == "" {
		t.Error("NotFoundError message is empty")
	}
}

func TestExeNameNonEmpty(t *testing.T) {
	if exeName() == "" {
		t.Error("exeName must not be empty")
	}
}

func TestAncestorsBounded(t *testing.T) {
	a := ancestors(filepath.Join("a", "b", "c", "d"))
	if len(a) == 0 {
		t.Fatal("ancestors returned nothing")
	}
	if a[0] != filepath.Clean(filepath.Join("a", "b", "c", "d")) {
		t.Errorf("first ancestor = %q, want the dir itself", a[0])
	}
}
