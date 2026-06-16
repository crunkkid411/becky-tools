// Package winctx reports which Windows File Explorer folder(s) are currently
// open, so becky-canvas can infer the import target without a "dumb" Browse
// dialog.
//
// The public API is OS-agnostic: on non-Windows platforms OpenExplorerFolders
// and ForegroundExplorerFolder return (nil/empty, ErrUnsupportedOS) — callers
// should degrade gracefully. On Windows, both functions shell out to a short
// PowerShell snippet that queries the Shell.Application COM object; the output
// is parsed by parseExplorerOutput, which is pure Go and unit-testable on any
// platform.
//
// # becky invariants
//
// - Offline + deterministic: no network. PowerShell is a local OS facility.
// - Degrade, never crash: if PowerShell is unavailable or Explorer has no open
// windows, we return an empty slice and a nil error (not an error) — "nothing
// open" is a normal, non-fatal state. Real errors (exec failure, non-zero exit)
// surface as wrapped errors the caller can log and skip.
// - Build-tag safe: OS-specific code lives in winctx_windows.go behind
// //go:build windows; winctx_other.go provides the stub so Linux CI compiles
// without change.
package winctx

import "strings"

// ExplorerWindow is a single open Windows File Explorer window.
type ExplorerWindow struct {
	// Path is the absolute filesystem path shown in the window (e.g. C:\Users\Jordan\Desktop).
	Path string
	// Title is the window title as reported by Shell.Application (typically the
	// folder name, e.g. "Desktop"). May be empty if the COM object could not
	// supply it.
	Title string
}

// OpenExplorerFolders returns all currently open File Explorer windows and their
// folder paths. An empty slice with a nil error means Explorer is open but has
// no navigated folder (e.g. the "Home" landing page), or Explorer is not running
// at all. A non-nil error indicates the query could not be executed.
func OpenExplorerFolders() ([]ExplorerWindow, error) {
	return openExplorerFolders()
}

// ForegroundExplorerFolder returns the folder path of the foreground (active)
// File Explorer window. If the foreground window is not Explorer, it falls back
// to the first folder in OpenExplorerFolders. Returns ("", nil) when no Explorer
// folder is open — not an error. A non-nil error means the query failed.
func ForegroundExplorerFolder() (string, error) {
	return foregroundExplorerFolder()
}

// ErrUnsupportedOS is returned on non-Windows platforms where the Shell.Application
// COM query is unavailable.
var ErrUnsupportedOS = unsupportedOSError("winctx: querying Explorer windows is only supported on Windows")

// unsupportedOSError is a distinct sentinel type so callers can use
// errors.Is(err, winctx.ErrUnsupportedOS) for typed degrade handling.
type unsupportedOSError string

func (e unsupportedOSError) Error() string { return string(e) }

// parseExplorerOutput converts raw stdout from the PowerShell COM query into a
// slice of ExplorerWindow values. It is a pure function (no OS calls) so it can
// be unit-tested on any platform.
//
// Expected input format: one filesystem path per line (CRLF or LF). Blank lines
// and pure-whitespace lines are skipped. Duplicate paths (case-sensitive) are
// deduplicated while preserving first-occurrence order.
func parseExplorerOutput(raw string) []ExplorerWindow {
	seen := make(map[string]bool)
	var out []ExplorerWindow
	for line := range strings.SplitSeq(raw, "\n") {
		// Strip CR so CRLF and LF both work.
		path := strings.TrimRight(line, "\r")
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, ExplorerWindow{
			Path:  path,
			Title: explorerTitle(path),
		})
	}
	return out
}

// explorerTitle derives a human-friendly window title from a path — the last
// path component. It mirrors what Explorer itself shows in the title bar.
// Uses strings only (no filepath) because the path may use '\' even on Linux CI.
func explorerTitle(path string) string {
	if path == "" {
		return ""
	}
	// Strip any trailing separators (e.g. "C:\Users\Jordan\").
	trimmed := strings.TrimRight(path, `/\`)
	if trimmed == "" || trimmed == path[:len(trimmed)] && strings.IndexAny(trimmed, `/\`) < 0 && len(trimmed) < len(path) {
		// All separators were stripped but nothing useful remains (e.g. "/" → "")
		// or the original had separators only — return path as-is.
		return path
	}
	// If trimming removed characters, check whether what's left has no separator
	// (means the input was a root like "C:\" — drive letter only remains).
	// In that case, the full original path is the best title.
	if len(trimmed) < len(path) && strings.IndexAny(trimmed, `/\`) < 0 {
		return path
	}
	if idx := strings.LastIndexAny(trimmed, `/\`); idx >= 0 {
		return trimmed[idx+1:]
	}
	// No separator at all — whole string is the title.
	return trimmed
}
