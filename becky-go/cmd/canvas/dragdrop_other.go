//go:build gui && !windows

// dragdrop_other.go is the non-Windows stub for OS file drag-and-drop.
// On non-Windows platforms Gio v0.10 does not expose an equivalent of
// Win32ViewEvent, so handleGioEvent is a no-op and enableFileDrop returns a
// typed "unsupported" error.  The caller logs the error once and falls back to
// the argv / Open-button paths — the window is never blocked or crashed.
package main

import (
	"errors"

	"gioui.org/io/event"
)

// errDragDropUnsupported is returned on platforms where IDropTarget is not available.
var errDragDropUnsupported = errors.New("OS file drag-drop not supported on this platform (Windows-only in Gio v0.10)")

// handleGioEvent is a no-op on non-Windows: no platform ViewEvent carries an HWND.
func handleGioEvent(_ *App, _ event.Event) {}

// enableFileDrop is a no-op on non-Windows.
func enableFileDrop(_ uintptr, _ func(paths []string)) (disable func(), err error) {
	return func() {}, errDragDropUnsupported
}
