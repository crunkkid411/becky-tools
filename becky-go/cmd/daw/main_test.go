package main

import (
	"os"
	"path/filepath"
	"testing"

	"becky-go/internal/music"
)

// writeFixtureMid writes a small valid SMF into a temp dir and returns its path.
func writeFixtureMid(t *testing.T) string {
	t.Helper()
	f := music.NewFile(480)
	meta := f.AddTrack()
	meta.Tempo(0, 140)
	meta.TimeSig(0, 4, 4)
	mel := f.AddTrack()
	mel.Name(0, "melody")
	mel.Note(0, 240, 0, 60, 88)
	mel.Note(118, 240, 0, 64, 88) // off-grid, so quantize has work to do
	path := filepath.Join(t.TempDir(), "song.mid")
	if err := os.WriteFile(path, f.Bytes(), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// TestRun_exitCodes covers usage/error/ok paths.
func TestRun_exitCodes(t *testing.T) {
	mid := writeFixtureMid(t)
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"no args is usage", nil, exitUsage},
		{"unknown command", []string{"frobnicate"}, exitUsage},
		{"help is ok", []string{"--help"}, exitOK},
		{"load missing file errors", []string{"load", "--in", "nope.mid"}, exitErr},
		{"load ok", []string{"load", "--in", mid}, exitOK},
		{"load json ok", []string{"load", "--in", mid, "--json"}, exitOK},
		{"edit requires op", []string{"edit", "--in", mid}, exitUsage},
		{"drumgrid ok", []string{"drumgrid", "--in", mid}, exitOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := run(c.args); got != c.want {
				t.Errorf("run(%v) = %d, want %d", c.args, got, c.want)
			}
		})
	}
}

// TestRun_editWritesAndRoundTrips: an edit op writes an output .mid that re-loads.
func TestRun_editWritesAndRoundTrips(t *testing.T) {
	mid := writeFixtureMid(t)
	out := filepath.Join(t.TempDir(), "edited.mid")
	code := run([]string{"edit", "--in", mid, "--out", out, "--op", "quantize", "--grid", "120", "--strength", "1"})
	if code != exitOK {
		t.Fatalf("edit run = %d, want 0", code)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("output not written: %v", err)
	}
	if code := run([]string{"load", "--in", out}); code != exitOK {
		t.Errorf("re-load edited = %d, want 0", code)
	}
}

// TestRun_editTranspose exercises a clip-wide op via the CLI.
func TestRun_editTranspose(t *testing.T) {
	mid := writeFixtureMid(t)
	if code := run([]string{"edit", "--in", mid, "--op", "transpose", "--semis", "12"}); code != exitOK {
		t.Errorf("transpose run = %d, want 0", code)
	}
}

// TestRun_badMidiDegrades: a non-MIDI input errors without panicking.
func TestRun_badMidiDegrades(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.mid")
	if err := os.WriteFile(bad, []byte("not a midi file"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("run panicked on bad midi: %v", r)
		}
	}()
	if code := run([]string{"load", "--in", bad}); code != exitErr {
		t.Errorf("bad midi load = %d, want %d", code, exitErr)
	}
}
