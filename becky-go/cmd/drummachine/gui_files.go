//go:build gui

// gui_files.go — Open / Save for machine.json. Open reuses cmd/canvas's proven
// PowerShell file-picker pattern on Windows (the only place a native picker exists),
// degrading to a clear status line elsewhere. Save writes the live machine next to
// itself (the opened path, or a default beside the exe). All paths degrade-never-
// crash: a cancelled dialog, a bad file, or an IO error is one quiet status line.
package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"becky-go/internal/drummachine"
)

// browseTimeout caps how long the picker dialog may stay open before we give up.
const browseTimeout = 5 * time.Minute

// startOpen opens a machine.json. On Windows it shows the native file picker on a
// goroutine (never blocks the UI); elsewhere it reports that the picker is desktop-
// only (drag a machine.json onto the exe, or pass it on the command line).
func (a *App) startOpen() {
	if runtime.GOOS != "windows" {
		a.setStatus("Open uses the Windows file picker — drag a machine.json onto becky-drummachine, or pass it on the command line.")
		return
	}
	go func() {
		path, err := pickMachineFile()
		if err != nil {
			a.setStatus("couldn't open the picker: " + err.Error())
			return
		}
		if path == "" {
			return // cancelled
		}
		a.loadMachineFile(path)
	}()
}

// loadMachineFile loads a machine.json into the live state and re-renders. A bad file
// degrades to a status line and leaves the current machine in place (never crash).
func (a *App) loadMachineFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		a.setStatus("couldn't open " + filepath.Base(path) + ": " + err.Error())
		return
	}
	defer f.Close()
	m, err := drummachine.Load(f)
	if err != nil {
		a.setStatus("couldn't read " + filepath.Base(path) + " (keeping the current kit): " + err.Error())
		return
	}
	a.machine = m
	a.curPath = path
	if a.selected >= drummachine.PadCount {
		a.selected = 0
	}
	a.syncStepButtons()
	a.setStatus("loaded " + filepath.Base(path))
}

// startSave writes the live machine to disk. It saves to the opened path if there is
// one, else a default machine.json beside the exe (or the CWD). Degrade-never-crash.
func (a *App) startSave() {
	path := a.savePath()
	data, err := a.machine.MarshalBytes()
	if err != nil {
		a.setStatus("couldn't build the machine to save: " + err.Error())
		return
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		a.setStatus("couldn't save: " + err.Error())
		return
	}
	a.curPath = path
	a.setStatus("saved " + path)
}

// savePath returns the path to save to: the opened file, else machine.json beside the
// exe, else machine.json in the CWD.
func (a *App) savePath() string {
	if strings.TrimSpace(a.curPath) != "" {
		return a.curPath
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "machine.json")
	}
	return "machine.json"
}

// pickMachineFile shows the Windows OpenFileDialog (PowerShell), mirroring
// cmd/canvas/gui_browse.go. Returns the chosen path, "" if cancelled, or an error.
func pickMachineFile() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), browseTimeout)
	defer cancel()
	const script = `
Add-Type -AssemblyName System.Windows.Forms | Out-Null
$d = New-Object System.Windows.Forms.OpenFileDialog
$d.Title = 'becky-drummachine - choose a machine.json'
$d.Filter = 'Drum machine (*.json)|*.json|All files (*.*)|*.*'
$d.CheckFileExists = $true
if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { Write-Output $d.FileName }
`
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-STA",
		"-ExecutionPolicy", "Bypass", "-Command", script)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
