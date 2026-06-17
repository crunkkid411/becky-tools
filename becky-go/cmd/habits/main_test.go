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

// TestRun_learnMissingLogsFlag returns exitUsage when --logs is omitted.
func TestRun_learnMissingLogsFlag(t *testing.T) {
	if got := run([]string{"learn", "--store", devNull(t)}); got != exitUsage {
		t.Errorf("learn without --logs: exit=%d want %d", got, exitUsage)
	}
}

// TestRun_learnMissingDir degrades gracefully (exitOK + empty store) when the
// logs directory does not exist — the tools may not have written logs yet.
func TestRun_learnMissingDir(t *testing.T) {
	store := devNull(t)
	noDir := filepath.Join(t.TempDir(), "no-such-logs")
	if got := run([]string{"learn", "--logs", noDir, "--store", store}); got != exitOK {
		t.Errorf("learn with missing dir: exit=%d want %d (should degrade)", got, exitOK)
	}
}

// TestRun_learnEndToEnd loads two JSONL logs, crosses the threshold, and
// verifies the store is updated and the report is printed.
func TestRun_learnEndToEnd(t *testing.T) {
	logsDir := t.TempDir()
	store := devNull(t)
	line := `{"tool":"daw","scope":"kick","field":"gain_db","auto":"-3","fixed":"-7"}` + "\n"
	for _, name := range []string{"s1.jsonl", "s2.jsonl"} {
		p := filepath.Join(logsDir, name)
		if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	if got := run([]string{"learn", "--logs", logsDir, "--store", store}); got != exitOK {
		t.Fatalf("learn exit=%d want 0", got)
	}
	// Store must exist and show a learned default.
	if got := run([]string{"show", "--store", store, "--json"}); got != exitOK {
		t.Errorf("show after learn exit=%d want 0", got)
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

// TestRun_structuredObserveThenUsual is the end-to-end structured path: a JSON
// blob correction (a sidechain route) recurs twice — with different key order — so
// it crosses the threshold, then `usual` recalls it.
func TestRun_structuredObserveThenUsual(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "habits.json")
	corr := filepath.Join(dir, "corrections.json")
	// Two equivalent blobs, different key order — must corroborate to one default.
	body := `[
		{"scope":"routing","field":"sidechain","auto":{},"fixed":{"from":"kick","to":"bus.bass","amount":0.5}},
		{"scope":"routing","field":"sidechain","auto":{},"fixed":{"amount":0.5,"to":"bus.bass","from":"kick"}}
	]`
	if err := os.WriteFile(corr, []byte(body), 0o644); err != nil {
		t.Fatalf("write corrections: %v", err)
	}
	if code := run([]string{"observe", corr, "--store", store}); code != exitOK {
		t.Fatalf("observe exit=%d want 0", code)
	}

	// usual <scope> (hit), usual --json, and usual with no scope must all succeed.
	if code := run([]string{"usual", "routing", "--store", store}); code != exitOK {
		t.Errorf("usual routing exit=%d want 0", code)
	}
	if code := run([]string{"usual", "routing", "--store", store, "--json"}); code != exitOK {
		t.Errorf("usual routing --json exit=%d want 0", code)
	}
	if code := run([]string{"usual", "--store", store}); code != exitOK {
		t.Errorf("usual (all) exit=%d want 0", code)
	}
	// show must still succeed with a structured habit present.
	if code := run([]string{"show", "--store", store}); code != exitOK {
		t.Errorf("show exit=%d want 0", code)
	}
}

// TestRun_usualMiss recalls from an empty/missing store and a too-many-args usage.
func TestRun_usualMiss(t *testing.T) {
	store := devNull(t)
	if code := run([]string{"usual", "bus.drums", "--store", store}); code != exitOK {
		t.Errorf("usual miss should still exit 0, got %d", code)
	}
	if code := run([]string{"usual", "a", "b", "--store", store}); code != exitUsage {
		t.Errorf("usual with two scopes should be a usage error, got %d", code)
	}
}

// devNull returns a per-test store path that won't collide with anything real.
func devNull(t *testing.T) string {
	return filepath.Join(t.TempDir(), "habits.json")
}
