//go:build gui

// gui_drumkit.go — "Load Kit" action for the canvas drum panel.
// Ports the folder-picker approach from cmd/drummachine/gui_kit.go: opens a
// PowerShell FolderBrowserDialog on a goroutine (never blocks the Gio frame loop)
// and stores the chosen folder in drumPanelState.kitDir.  execPlay then bakes those
// samples into the --play-machine JSON via WithDefaultKitSamples(kitDir).
// Degrade-never-crash: Windows-only warning on non-Windows; picker timeout is 5 min.
package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const drumKitBrowseTimeout = 5 * time.Minute

// startDrumKitLoad opens a Windows folder picker on a background goroutine.
// On success it updates d.kitDir and invalidates the window so the caption
// updates immediately.  All errors degrade to a single neon line — never crash.
func (a *App) startDrumKitLoad(d *drumPanelState) {
	if runtime.GOOS != "windows" {
		a.appendLine("load kit: folder picker is Windows-only — set BECKY_DRUM_KIT env instead")
		return
	}
	go func() {
		dir, err := pickDrumKitFolder("becky-canvas — choose a kit folder")
		if err != nil {
			a.appendLine("load kit: " + err.Error())
			return
		}
		if dir == "" {
			return // user cancelled
		}
		d.kitDir = dir
		a.window.Invalidate()
		a.appendLine("drum kit loaded: " + filepath.Base(dir))
	}()
}

// pickDrumKitFolder opens a Windows FolderBrowserDialog via PowerShell -STA and
// returns the selected path.  Returns ("", nil) when the user cancels.
func pickDrumKitFolder(title string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), drumKitBrowseTimeout)
	defer cancel()
	// Strip single-quotes from the title to avoid breaking the PS string literal.
	safeTitle := strings.ReplaceAll(title, "'", "")
	script := `
Add-Type -AssemblyName System.Windows.Forms | Out-Null
$d = New-Object System.Windows.Forms.FolderBrowserDialog
$d.Description = '` + safeTitle + `'
$d.ShowNewFolderButton = $false
if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { Write-Output $d.SelectedPath }
`
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-STA",
		"-ExecutionPolicy", "Bypass", "-Command", script)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
