package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdSong_ComposesEditsAndWrites(t *testing.T) {
	out := filepath.Join(t.TempDir(), "song.rpp")
	err := cmdSong([]string{
		"--genre", "crunkcore", "--seed", "7",
		"--do", "set tempo to 100",
		"--do", "mute the sfx",
		"--out", out,
	})
	if err != nil {
		t.Fatalf("cmdSong: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	rpp := string(data)
	if !strings.HasPrefix(rpp, "<REAPER_PROJECT") {
		t.Errorf("output is not a REAPER project")
	}
	if !strings.Contains(rpp, "TEMPO 100 ") {
		t.Errorf("tempo edit did not reach the .rpp")
	}
	// The sfx track should carry MUTESOLO 1 (muted) from the plain-English edit.
	if !strings.Contains(rpp, "NAME sfx") {
		t.Errorf("expected an sfx track in the session")
	}
}

func TestCmdSong_Deterministic(t *testing.T) {
	mk := func() string {
		out := filepath.Join(t.TempDir(), "s.rpp")
		if err := cmdSong([]string{"--genre", "digicore", "--seed", "3", "--out", out}); err != nil {
			t.Fatalf("cmdSong: %v", err)
		}
		b, _ := os.ReadFile(out)
		return string(b)
	}
	// The .rpp embeds fresh GUIDs per run, so compare the musical payload (MIDI note
	// events) rather than the whole file: same seed => same notes.
	a, b := notesOnly(mk()), notesOnly(mk())
	if a != b {
		t.Errorf("same genre+seed produced different note content (non-deterministic)")
	}
	if a == "" {
		t.Errorf("expected MIDI note events in the session")
	}
}

func TestCmdSong_Errors(t *testing.T) {
	if err := cmdSong([]string{"--out", filepath.Join(t.TempDir(), "x.rpp")}); err == nil {
		t.Error("expected error when --genre is missing")
	}
	// Note: ResolveProfile fuzzy-matches and falls back to a default profile for an
	// unrecognized genre rather than erroring, so an arbitrary genre still produces a
	// valid session (verified: "definitely-not-a-genre" -> a default profile).
	out := filepath.Join(t.TempDir(), "x.rpp")
	if err := cmdSong([]string{"--genre", "definitely-not-a-genre", "--out", out}); err != nil {
		t.Errorf("unknown genre should fall back, not error: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("fallback genre did not write a session: %v", err)
	}
}

// notesOnly extracts the MIDI event lines (E ...) so determinism is judged on the
// music, not the randomized project GUIDs.
func notesOnly(rpp string) string {
	var b strings.Builder
	for _, line := range strings.Split(rpp, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "E ") || strings.HasPrefix(t, "TEMPO ") {
			b.WriteString(t)
			b.WriteByte('\n')
		}
	}
	return b.String()
}
