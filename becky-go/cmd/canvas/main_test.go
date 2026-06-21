//go:build !gui

package main

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleProject = `{
  "schemaVersion": 1,
  "tool": "becky-compose",
  "tempo": 140,
  "ppq": 480,
  "tracks": [
    {"id": "bass", "midi": "bass.mid", "channel": 1, "out": "bus.808"}
  ],
  "routing": []
}`

func TestRun_emptySceneOK(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"default-ask", nil, exitOK},
		{"explicit-mode", []string{"--mode", "drum"}, exitOK},
		{"json-synonym", []string{"--json", "--mode", "midi"}, exitOK},
	}
	for _, c := range cases {
		if got := run(c.args); got != c.want {
			t.Errorf("%s: run(%v)=%d want %d", c.name, c.args, got, c.want)
		}
	}
}

func TestRun_badModeIsBadArgs(t *testing.T) {
	if got := run([]string{"--mode", "piano"}); got != exitBadArgs {
		t.Errorf("bad --mode should be exitBadArgs(%d), got %d", exitBadArgs, got)
	}
}

func TestRun_unknownFlagIsBadArgs(t *testing.T) {
	if got := run([]string{"--nope"}); got != exitBadArgs {
		t.Errorf("unknown flag should be exitBadArgs(%d), got %d", exitBadArgs, got)
	}
}

func TestRun_loadsProjectOK(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project.json")
	if err := os.WriteFile(path, []byte(sampleProject), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	if got := run([]string{path}); got != exitOK {
		t.Errorf("loading a good project should be exitOK(%d), got %d", exitOK, got)
	}
}

func TestRun_missingProjectDegrades(t *testing.T) {
	// Degrade, never crash: a missing project still emits an empty scene + exit 1.
	missing := filepath.Join(t.TempDir(), "nope.json")
	if got := run([]string{missing}); got != exitDegraded {
		t.Errorf("missing project should degrade to exit %d, got %d", exitDegraded, got)
	}
}

func TestJoinModes_listsAllPlannedModes(t *testing.T) {
	got := joinModes()
	want := "ask|video|daw|midi|drum|audio"
	if got != want {
		t.Errorf("joinModes()=%q want %q", got, want)
	}
}
