//go:build !gui

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"becky-go/internal/drummachine"
)

// runCapture runs the CLI with args, capturing stdout + stderr via temp files (run
// takes *os.File streams, matching os.Stdout/os.Stderr). Returns code, stdout, stderr.
func runCapture(t *testing.T, args []string) (int, string, string) {
	t.Helper()
	dir := t.TempDir()
	outF, err := os.Create(filepath.Join(dir, "out"))
	if err != nil {
		t.Fatalf("create out: %v", err)
	}
	defer outF.Close()
	errF, err := os.Create(filepath.Join(dir, "err"))
	if err != nil {
		t.Fatalf("create err: %v", err)
	}
	defer errF.Close()

	code := run(args, outF, errF)

	outB, _ := os.ReadFile(outF.Name())
	errB, _ := os.ReadFile(errF.Name())
	return code, string(outB), string(errB)
}

func TestRun_defaultMachineOK(t *testing.T) {
	code, out, _ := runCapture(t, nil)
	if code != exitOK {
		t.Fatalf("run(nil) = %d, want %d", code, exitOK)
	}
	if !strings.Contains(out, `"schemaVersion"`) || !strings.Contains(out, `"kit"`) {
		t.Errorf("default machine.json missing expected fields:\n%s", out)
	}
}

func TestRun_unknownFlagIsBadArgs(t *testing.T) {
	if code, _, _ := runCapture(t, []string{"--nope"}); code != exitBadArgs {
		t.Errorf("unknown flag should be exitBadArgs(%d), got %d", exitBadArgs, code)
	}
}

func TestRun_missingMachineDegrades(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.json")
	code, out, _ := runCapture(t, []string{"--machine", missing})
	if code != exitDegraded {
		t.Errorf("missing machine should degrade to exit %d, got %d", exitDegraded, code)
	}
	// Degrade-never-crash: a default machine is still emitted.
	if !strings.Contains(out, `"schemaVersion"`) {
		t.Errorf("degraded run should still emit a default machine, got:\n%s", out)
	}
}

func TestRun_loadsMachineOK(t *testing.T) {
	path := filepath.Join(t.TempDir(), "machine.json")
	b, err := drummachine.NewMachine().MarshalBytes()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if code, _, _ := runCapture(t, []string{"--machine", path}); code != exitOK {
		t.Errorf("loading a good machine should be exitOK(%d), got %d", exitOK, code)
	}
}

// TestRun_doSetTempo exercises the AI box path headlessly: a plain-English
// instruction is parsed + applied and the change shows in the emitted machine.
func TestRun_doSetTempo(t *testing.T) {
	code, out, errOut := runCapture(t, []string{"--do", "set the tempo to 140"})
	if code != exitOK {
		t.Fatalf("--do should be exitOK(%d), got %d (stderr: %s)", exitOK, code, errOut)
	}
	if !strings.Contains(out, `"tempo": 140`) {
		t.Errorf("expected tempo 140 in output machine, got:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(errOut), "tempo") {
		t.Errorf("expected a tempo summary on stderr, got: %s", errOut)
	}
}

// TestRun_doIsDeterministic confirms the same instruction yields byte-identical
// output (becky's offline+deterministic invariant).
func TestRun_doIsDeterministic(t *testing.T) {
	args := []string{"--do", "make it half-time"}
	_, out1, _ := runCapture(t, args)
	_, out2, _ := runCapture(t, args)
	if out1 != out2 {
		t.Errorf("non-deterministic output for %q:\n--- run1 ---\n%s\n--- run2 ---\n%s", args, out1, out2)
	}
}

// TestRun_doUnrecognisedIsFriendly confirms an instruction becky doesn't recognise
// is a friendly note + unchanged machine, NOT an error exit (degrade-never-crash).
func TestRun_doUnrecognisedIsFriendly(t *testing.T) {
	code, out, errOut := runCapture(t, []string{"--do", "xyzzy frobnicate the gizmo"})
	if code != exitOK {
		t.Errorf("an unrecognised instruction should still be exitOK(%d), got %d", exitOK, code)
	}
	if !strings.Contains(out, `"schemaVersion"`) {
		t.Errorf("unchanged machine should still be emitted, got:\n%s", out)
	}
	if strings.TrimSpace(errOut) == "" {
		t.Errorf("expected a friendly note on stderr for an unrecognised instruction")
	}
}

// TestApplyInstruction_transportLeavesMachineUnchanged confirms a "play" instruction
// (a transport signal) does not mutate the machine model.
func TestApplyInstruction_transportLeavesMachineUnchanged(t *testing.T) {
	m := drummachine.NewMachine()
	before, _ := m.MarshalBytes()
	next, summary := applyInstruction(m, "play")
	after, _ := next.MarshalBytes()
	if !bytes.Equal(before, after) {
		t.Errorf("transport (play) should not change the machine model")
	}
	if summary == "" {
		t.Errorf("transport should still return a summary")
	}
}

// TestApplyInstruction_addPatternChangesModel confirms a structural edit is applied
// (the model is the live UI state; this is what the window re-renders from).
func TestApplyInstruction_addPatternChangesModel(t *testing.T) {
	m := drummachine.NewMachine()
	want := m.PatternCount() + 1
	next, _ := applyInstruction(m, "new pattern")
	if got := next.PatternCount(); got != want {
		t.Errorf("new pattern: pattern count = %d, want %d", got, want)
	}
}
