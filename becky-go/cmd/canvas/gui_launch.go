//go:build gui

// gui_launch.go — the HUB. becky-canvas is the ONE place Jordan opens his tools
// from, so he never hunts through folders. Each launch button opens a REAL,
// standalone becky tool in its OWN window/process (the one-tool-one-job rule):
//
//	DRUM → becky-drummachine.exe  (the 16-pad Maschine-class drum machine window)
//	DAW  → becky-reaper open       (becky authors a session + opens REAPER)
//	CLIP → becky-clip.exe          (forensic transcript video editor)
//	NLE  → becky-nle.exe           (AI video editor)
//	ASK  → becky-ask.exe           (natural-language chat — a terminal app)
//
// Launch is detached + degrade-never-crash: a missing exe surfaces as one quiet
// neon line in the output panel, never a panic. GUI exes (windowsgui) open with no
// console; becky-ask is a TUI, so it opens in its own console window.
package main

import (
	"fmt"
	"os/exec"
	"path/filepath"

	"gioui.org/layout"
	"gioui.org/widget"
	"golang.org/x/exp/shiny/materialdesign/icons"

	"becky-go/internal/proc"
)

// hubLauncher holds the hub buttons' click state + icons (decoded once). It lives
// on the App so click state persists across frames.
type hubLauncher struct {
	drum, daw, clip, nle, ask      widget.Clickable
	iDrum, iDaw, iClip, iNle, iAsk *widget.Icon
}

// newHubLauncher decodes the launch-button icons (nil on failure → the dock draws a
// placeholder; degrade, never crash).
func newHubLauncher() *hubLauncher {
	mk := func(b []byte) *widget.Icon { ic, _ := widget.NewIcon(b); return ic }
	// Icons chosen to be visually distinct; tooltips provide the label.
	return &hubLauncher{
		iDrum: mk(icons.ImageGridOn),     // grid       → drum machine
		iDaw:  mk(icons.AVMusicVideo),    // music+film → REAPER DAW (distinct from piano mode)
		iClip: mk(icons.ActionPermMedia), // perm-media → forensic clip editor
		iNle:  mk(icons.AVVideocam),      // camera     → video editor (NLE, distinct from iClip)
		iAsk:  mk(icons.ActionSearch),    // search     → ask (chat front-door)
	}
}

// hubItems returns the launch buttons as dockItems so they reuse the dock's exact
// button look. Order is fixed and deterministic.
func (a *App) hubItems() []dockItem {
	h := a.hub
	return []dockItem{
		{icon: h.iDrum, tip: "Open Drum Machine", accent: colYellow, clicker: &h.drum},
		{icon: h.iDaw, tip: "Open REAPER DAW", accent: colElecBlue, clicker: &h.daw},
		{icon: h.iClip, tip: "Open Clip (forensic video)", accent: colNeonPink, clicker: &h.clip},
		{icon: h.iNle, tip: "Open Video editor (NLE)", accent: colDeepPurple, clicker: &h.nle},
		{icon: h.iAsk, tip: "Open Ask (chat)", accent: colNeonGreen, clicker: &h.ask},
	}
}

// handleHubInput dispatches hub-button clicks (called from handleInput each frame).
func (a *App) handleHubInput(gtx layout.Context) {
	h := a.hub
	switch {
	case h.drum.Clicked(gtx):
		a.openTool("drum", "Drum Machine")
	case h.daw.Clicked(gtx):
		a.openTool("daw", "REAPER DAW")
	case h.clip.Clicked(gtx):
		a.openTool("clip", "Clip")
	case h.nle.Clicked(gtx):
		a.openTool("nle", "Video editor")
	case h.ask.Clicked(gtx):
		a.openTool("ask", "Ask")
	}
}

// openTool launches a tool off the UI thread and reports the result as one quiet
// neon line. Never blocks the window.
func (a *App) openTool(key, label string) {
	a.outExpanded = true
	a.appendLine("opening " + label + "…")
	go func() {
		if err := a.launchTool(key); err != nil {
			a.appendLine("couldn't open " + label + ": " + firstLine(err.Error()))
		} else {
			a.appendLine(label + " is opening in its own window.")
		}
		a.window.Invalidate()
	}()
}

// launchTool resolves the right sibling exe and starts it DETACHED. GUI exes open
// directly; becky-ask is a terminal app so it gets a fresh console via `cmd /c
// start`. The REAPER button runs `becky-reaper open` (authors a session + opens
// REAPER), keeping all REAPER path logic in the reaper tool.
func (a *App) launchTool(key string) error {
	exe := func(base string) string {
		if isWindows() {
			return base + ".exe"
		}
		return base
	}

	switch key {
	case "drum":
		return startDetached(exe("becky-drummachine"))
	case "clip":
		return startDetached(exe("becky-clip"))
	case "nle":
		return startDetached(exe("becky-nle"))
	case "daw":
		return startDetached(exe("becky-reaper"), "open")
	case "ask":
		return startConsole(exe("becky-ask"))
	default:
		return fmt.Errorf("unknown tool %q", key)
	}
}

// startDetached resolves a sibling exe and starts it without waiting, so closing
// becky-canvas does not close the launched tool. NoWindow keeps the launcher from
// flashing a console (GUI children carry their own window).
func startDetached(name string, args ...string) error {
	path, err := resolveExe(name)
	if err != nil {
		return err
	}
	cmd := exec.Command(path, args...)
	cmd.Dir = filepath.Dir(path)
	proc.NoWindow(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}
	go func() { _ = cmd.Wait() }() // reap; do not block
	return nil
}

// startConsole opens a terminal app (becky-ask) in its OWN new console window via
// the cmd `start` builtin, since a windowsgui parent has no console to share. On
// non-Windows it falls back to a plain detached start.
func startConsole(name string) error {
	path, err := resolveExe(name)
	if err != nil {
		return err
	}
	if !isWindows() {
		return startDetached(name)
	}
	// cmd /c start "" /D <dir> <exe>  → a fresh console running the TUI.
	cmd := exec.Command("cmd", "/c", "start", "", "/D", filepath.Dir(path), path)
	proc.NoWindow(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
