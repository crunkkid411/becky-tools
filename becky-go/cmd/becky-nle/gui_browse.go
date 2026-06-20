//go:build gui

// gui_browse.go — the native "Open a video" file picker. becky's tools take a FILE PATH,
// so the picker must hand back a real path string. gioui.org/x/explorer returns only the
// file CONTENTS (wrong for a path-driven, multi-GB-video workflow), so on Windows we drive
// the native common dialog through a tiny PowerShell snippet and read the chosen path from
// stdout (the same approach cmd/canvas/gui_browse.go uses). If anything goes wrong it
// degrades to a friendly "drag the path" message — never a crash.
package main

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// browseTimeout caps how long we wait on the picker dialog (generous but bounded so
// nothing wedges forever).
const browseTimeout = 5 * time.Minute

// browseForVideo opens a native video-file picker and returns the chosen path. An empty
// return with a nil error means the user cancelled. A non-nil error means the picker
// couldn't run (the caller shows a hint; drag-onto-exe + the command box still work).
// Only implemented for Windows; elsewhere it returns a friendly "drag a video" error.
func browseForVideo() (string, error) {
	if runtime.GOOS != "windows" {
		return "", pickerError("Drag a video onto becky-nle to open it (the native file picker is Windows-only).")
	}
	ctx, cancel := context.WithTimeout(context.Background(), browseTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-STA",
		"-ExecutionPolicy", "Bypass", "-Command", videoPickerPS)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", pickerError("The file picker was open too long and was closed. Drag a video onto becky-nle instead.")
		}
		return "", pickerError("The file picker couldn't open. Drag a video onto becky-nle instead.")
	}
	return strings.TrimSpace(string(out)), nil
}

// pickerError is a tiny error type carrying a Jordan-friendly message.
type pickerError string

func (e pickerError) Error() string { return string(e) }

// videoPickerPS opens the native Open File dialog filtered to common video containers and
// prints the selected path. It prints nothing on cancel (callers treat empty as cancel).
const videoPickerPS = `
Add-Type -AssemblyName System.Windows.Forms | Out-Null
$d = New-Object System.Windows.Forms.OpenFileDialog
$d.Title = 'becky-nle - choose a video'
$d.Filter = 'Video files|*.mp4;*.mov;*.mkv;*.avi;*.m4v;*.webm;*.wmv;*.flv;*.ts;*.mpg;*.mpeg|All files|*.*'
$d.CheckFileExists = $true
if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { Write-Output $d.FileName }
`
