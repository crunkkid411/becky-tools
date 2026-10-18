package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The helper's per-window lines must become a percentage in the progress file and stay
// out of the error text; every other line is kept for the error message.
func TestHelperStderrProgress(t *testing.T) {
	f := filepath.Join(t.TempDir(), "p.txt")
	prog = &progressReporter{file: f, name: "clip.mp4", total: 3}
	prog.stepf("listening")
	h := &helperStderr{}
	h.Write([]byte("[progress] window 1/4\nreal warn"))
	h.Write([]byte("ing\n[progress] window 2/4\n"))
	got, _ := os.ReadFile(f)
	if string(got) != "clip.mp4 - step 1 of 3: listening 50%" {
		t.Fatalf("progress file = %q", got)
	}
	if strings.TrimSpace(h.String()) != "real warning" {
		t.Fatalf("kept stderr = %q", h.String())
	}
	prog.done()
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Fatal("progress file not removed when done")
	}
}
