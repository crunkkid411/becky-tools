// enroll.go — the REAL becky-name seams: the exec enroller (shells out to the existing
// becky-enroll teach path) and the OS image shower (opens the representative face in
// the default viewer). Both implement the small interfaces in internal/facenaming so
// the orchestration logic stays testable headless with fakes.
//
// Cloud cannot run either of these (no models, no display) — they are the documented
// local/hardware boundary (CLAUDE.md §4). The argv they build is the same one
// facenaming.EnrollArgs produces and dry-run prints, so what local runs is exactly
// what cloud proved offline.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"becky-go/internal/facenaming"
	"becky-go/internal/proc"
)

// execEnroller runs becky-enroll --clip <clip> --name <name> --kb <kb> [--device …]
// for each clip, appending to the KB. It surfaces the tail of stderr as the skip
// reason so a clip that won't enroll is recorded, not fatal.
type execEnroller struct {
	bin    string // resolved path to becky-enroll
	device string
}

var _ facenaming.Enroller = (*execEnroller)(nil)

// newExecEnroller resolves the becky-enroll binary (next to this exe, or --bin) and
// returns the real enroller.
func newExecEnroller(rc runConfig) *execEnroller {
	return &execEnroller{bin: resolveEnrollBin(rc.binDir), device: rc.device}
}

// Enroll shells out to becky-enroll for one clip under one name, appending to kb.
func (e *execEnroller) Enroll(clip, name, kb string) error {
	bin := e.bin
	if bin == "" {
		return fmt.Errorf("becky-enroll not found (pass --bin <dir>)")
	}
	args := []string{"--clip", clip, "--name", name, "--kb", kb}
	if e.device != "" {
		args = append(args, "--device", e.device)
	}
	cmd := exec.Command(bin, args...)
	proc.NoWindow(cmd) // no console flash on Windows
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("becky-enroll failed: %v: %s", err, tail(stderr.String()))
	}
	return nil
}

// resolveEnrollBin finds becky-enroll: --bin dir if given, else next to this binary.
func resolveEnrollBin(override string) string {
	name := facenaming.EnrollBinary
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if override != "" {
		if cand := filepath.Join(override, name); fileExists(cand) {
			return cand
		}
	}
	if exe, err := os.Executable(); err == nil {
		if cand := filepath.Join(filepath.Dir(exe), name); fileExists(cand) {
			return cand
		}
	}
	return ""
}

// osImageShower opens the representative face/clip in the OS default viewer beside the
// TUI (the most robust display method; inline terminal graphics are an Open Decision).
// Detached + degrade-never-crash: a missing viewer is reported, the loop continues.
type osImageShower struct{}

var _ facenaming.ImageShower = (*osImageShower)(nil)

// Show opens path in the platform default image viewer without blocking the TUI.
func (osImageShower) Show(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("no representative image to show")
	}
	if !fileExists(path) {
		return fmt.Errorf("representative not found: %s", path)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	proc.NoWindow(cmd)
	return cmd.Start() // detached; do not Wait
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 400 {
		return s[len(s)-400:]
	}
	return s
}
