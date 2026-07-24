//go:build windows

// folderpicker_windows.go gives becky-clip a REAL native "choose folder" dialog
// on Windows so Jordan's "Open folder" button pops the standard Explorer-style
// picker instead of a type-the-path box (SPEC-BECKY-CLIP §12 P1 item). It stays
// cgo-free by shelling out to PowerShell's System.Windows.Forms.FolderBrowserDialog
// — no extra dependency, works on every Win10/11 box. A cancelled dialog returns
// "" with no error (the caller treats it as a no-op), so it never crashes.
package main

import (
	"os/exec"
	"strings"
)

// pickFolder opens the native Windows folder-chooser and returns the selected
// absolute path, or "" if the user cancelled. cgo-free: it runs PowerShell's
// FolderBrowserDialog in a single-threaded-apartment (-STA, required for the
// WinForms dialog) and reads the chosen path off stdout. An exec failure (no
// PowerShell, dialog error) is returned so the UI can fall back to a path box.
//
// startDir preselects the folder the picker opens IN. Jordan (item 5): "open folder
// needs to open to the last folder, not the default windows navigator" - the caller
// passes the currently-open case folder, and FolderBrowserDialog.SelectedPath both
// preselects it and scrolls the tree to it. Empty startDir = the old default location.
func pickFolder(startDir string) (string, error) {
	sel := ""
	if s := strings.TrimSpace(startDir); s != "" {
		// single-quote escape (double every ') so a folder name containing a quote
		// cannot break out of the PowerShell string literal.
		esc := strings.ReplaceAll(s, "'", "''")
		sel = `$f.SelectedPath = '` + esc + `'; `
	}
	script := `Add-Type -AssemblyName System.Windows.Forms; ` +
		`$f = New-Object System.Windows.Forms.FolderBrowserDialog; ` +
		`$f.Description = 'Pick your case folder'; ` +
		`$f.ShowNewFolderButton = $false; ` +
		sel +
		`if ($f.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { [Console]::Out.Write($f.SelectedPath) }`
	cmd := exec.Command("powershell", "-NoProfile", "-STA", "-Command", script)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
