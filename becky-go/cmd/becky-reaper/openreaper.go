package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"becky-go/internal/reaper"
)

// cmdOpen authors a fresh becky session (Jordan's Cubase-style bus tree at 132 BPM)
// and opens it in REAPER's GUI for him to work in — the "open my DAW" entry point
// the becky-canvas hub button calls (`becky-reaper open`). Degrade-never-crash: if
// REAPER isn't installed the .rpp is still written and the path is reported so it
// can be opened by hand.
//
//	becky-reaper open [--rpp existing.rpp]
func cmdOpen(args []string) error {
	rpp := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--rpp" && i+1 < len(args) {
			i++
			rpp = args[i]
		}
	}
	if rpp == "" {
		out := filepath.Join(os.TempDir(), "becky-session.rpp")
		p := reaper.JordanTemplate("")
		if err := os.WriteFile(out, []byte(reaper.WriteRPP(p)), 0o644); err != nil {
			return fmt.Errorf("write session: %w", err)
		}
		rpp = out
	}
	abs, _ := filepath.Abs(rpp)

	exe := reaperExe()
	if exe == "" {
		return fmt.Errorf("REAPER not found (set BECKY_REAPER); wrote the session to %s - open it by hand", abs)
	}
	fmt.Printf("opening %s in REAPER ...\n", abs)
	cmd := exec.Command(exe, abs, "-nosplash")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch REAPER: %w", err)
	}
	// Detach: REAPER is the DAW Jordan now works in; don't wait on its GUI.
	return nil
}
