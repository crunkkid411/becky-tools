//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableANSI turns on virtual-terminal escape processing for stdout so
// --pretty's ANSI colors render in the classic conhost-based "Windows
// PowerShell" / cmd.exe, not just Windows Terminal (which already has it on
// by default). Without this the raw \x1b[...m codes print as garbage text.
// Mirrors search_library's console_windows.go.
func enableANSI() {
	h := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return // not a real console (e.g. output piped/redirected) - fine, no ANSI needed then
	}
	_ = windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
}
