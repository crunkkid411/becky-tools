//go:build gui

// gui_browse.go — the "Browse…" path picker. becky's tools take a FILE OR FOLDER PATH,
// so the picker must hand back a real path string. gioui.org/x/explorer opens the
// native dialog but only returns the file's CONTENTS (an io.ReadCloser) and has no
// folder picker — wrong for a 500GB-video, path-driven workflow. So on Windows we drive
// the native common dialog through a tiny PowerShell snippet and read the chosen path
// from stdout. If anything goes wrong, Browse degrades to "just type the path in the box"
// (the text field always works) — never a crash.
package main

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// browseTimeout caps how long we wait on the picker dialog before giving up (the user
// may leave it open a while; this is generous but bounded so nothing wedges forever).
const browseTimeout = 5 * time.Minute

// browseForPath opens a native picker and returns the chosen path. wantFolder selects a
// folder picker instead of a file picker. An empty return with a nil error means the
// user cancelled. A non-nil error means the picker couldn't run (caller shows a hint and
// falls back to the text field). Only implemented for Windows; elsewhere it returns a
// friendly "type the path" error so the field-only path still works.
func browseForPath(wantFolder bool) (string, error) {
	if runtime.GOOS != "windows" {
		return "", errPickerUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), browseTimeout)
	defer cancel()

	script := filePickerPS
	if wantFolder {
		script = folderPickerPS
	}
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-STA",
		"-ExecutionPolicy", "Bypass", "-Command", script)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", errPickerTimeout
		}
		return "", errPickerUnavailable
	}
	return strings.TrimSpace(string(out)), nil
}

// errPickerUnavailable / errPickerTimeout are friendly sentinels the UI turns into a
// plain-language line. They are a tiny error type so callers can show them directly.
var (
	errPickerUnavailable = pickerError("The file picker couldn't open. Just paste or type the path into the Target box instead.")
	errPickerTimeout     = pickerError("The file picker was open too long and was closed. Paste the path into the Target box instead.")
)

// pickerError is a tiny error type carrying a Jordan-friendly message.
type pickerError string

func (e pickerError) Error() string { return string(e) }

// filePickerPS opens the native Open File dialog and prints the selected path. It prints
// nothing on cancel (so callers treat empty as "cancelled").
const filePickerPS = `
Add-Type -AssemblyName System.Windows.Forms | Out-Null
$d = New-Object System.Windows.Forms.OpenFileDialog
$d.Title = 'becky-canvas - choose a file'
$d.CheckFileExists = $true
if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { Write-Output $d.FileName }
`

// folderPickerPS opens the native Folder Browser dialog and prints the selected folder.
const folderPickerPS = `
Add-Type -AssemblyName System.Windows.Forms | Out-Null
$d = New-Object System.Windows.Forms.FolderBrowserDialog
$d.Description = 'becky-canvas - choose a folder'
if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { Write-Output $d.SelectedPath }
`

// browseForPathIn opens the native file picker with its initial directory pre-set to
// folder (the active Explorer window's path), passing it safely via an env var so the
// path is never injected into the script body. Falls back to the unscoped picker on
// non-Windows or when folder is empty — degrade, never crash.
func browseForPathIn(folder string) (string, error) {
	if runtime.GOOS != "windows" || folder == "" {
		return browseForPath(false)
	}
	ctx, cancel := context.WithTimeout(context.Background(), browseTimeout)
	defer cancel()
	// Pass the folder via env var (BECKY_BROWSE_DIR) to avoid path-injection in
	// the PS script. The script reads $env:BECKY_BROWSE_DIR as the initial directory.
	const script = `
Add-Type -AssemblyName System.Windows.Forms | Out-Null
$d = New-Object System.Windows.Forms.OpenFileDialog
$d.Title = 'becky-canvas - choose a file'
$d.InitialDirectory = $env:BECKY_BROWSE_DIR
$d.CheckFileExists = $true
if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { Write-Output $d.FileName }
`
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-STA",
		"-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.Env = append(os.Environ(), "BECKY_BROWSE_DIR="+folder)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", errPickerTimeout
		}
		return "", errPickerUnavailable
	}
	return strings.TrimSpace(string(out)), nil
}
