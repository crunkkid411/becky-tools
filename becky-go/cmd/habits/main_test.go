package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRun_exitCodes covers the dispatch contract: 2 on usage problems, 1 on
// runtime errors (bad/missing files), 0 on success.
func TestRun_exitCodes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"no args", nil, exitUsage},
		{"unknown command", []string{"frobnicate"}, exitUsage},
		{"help", []string{"--help"}, exitOK},
		{"observe without file", []string{"observe"}, exitUsage},
		{"observe missing file", []string{"observe", "no-such-file.json", "--store", devNull(t)}, exitErr},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := run(tc.args); got != tc.want {
				t.Errorf("run(%v)=%d want %d", tc.args, got, tc.want)
			}
		})
	}
}

// TestRun_observeThenShow is the end-to-end happy path: observe a corrections file
// twice over (so a fix crosses the threshold) into a temp store, then show it.
func TestRun_observeThenShow(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "habits.json")
	corr := filepath.Join(dir, "corrections.json")
	body := `[
		{"scope":"kick","field":"gain_db","auto":"-3","fixed":"-7"},
		{"scope":"kick","field":"gain_db","auto":"-3","fixed":"-7"}
	]`
	if err := os.WriteFile(corr, []byte(body), 0o644); err != nil {
		t.Fatalf("write corrections: %v", err)
	}
	if code := run([]string{"observe", corr, "--store", store}); code != exitOK {
		t.Fatalf("observe exit=%d want 0", code)
	}
	if _, err := os.Stat(store); err != nil {
		t.Fatalf("store not written: %v", err)
	}
	if code := run([]string{"show", "--store", store}); code != exitOK {
		t.Errorf("show exit=%d want 0", code)
	}
	if code := run([]string{"show", "--store", store, "--json"}); code != exitOK {
		t.Errorf("show --json exit=%d want 0", code)
	}
}

// TestRun_observeBadJSON degrades to a runtime error (1), not a panic.
func TestRun_observeBadJSON(t *testing.T) {
	dir := t.TempDir()
	corr := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(corr, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if code := run([]string{"observe", corr, "--store", filepath.Join(dir, "habits.json")}); code != exitErr {
		t.Errorf("bad JSON exit=%d want 1", code)
	}
}

// devNull returns a per-test store path that won't collide with anything real.
func devNull(t *testing.T) string {
	return filepath.Join(t.TempDir(), "habits.json")
}
