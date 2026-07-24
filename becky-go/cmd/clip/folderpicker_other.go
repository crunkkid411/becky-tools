//go:build !windows

// folderpicker_other.go is the non-Windows stub for the native folder picker.
// becky-clip's real product is the Windows WebView2 build; on other OSes (and CI)
// there is no native dialog here, so pickFolder returns "" (the caller treats it
// as "no folder chosen" — a no-op). This keeps `go build ./...` green everywhere.
package main

// pickFolder is a no-op on non-Windows: it returns "" (nothing chosen) and no
// error, so App.PickFolder degrades to "user cancelled" rather than failing.
// startDir (the folder to open in on Windows) is ignored here.
func pickFolder(startDir string) (string, error) {
	return "", nil
}
