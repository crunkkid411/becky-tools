//go:build windows

package winctx

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// explorerQueryTimeout caps the PowerShell COM round-trip. The query is
// instant in practice (in-process COM); 10 s is generous for any slow startup.
const explorerQueryTimeout = 10 * time.Second

// explorerFoldersPS queries Shell.Application for every open Explorer window
// and prints each window's folder path on its own line.
//
// Notes on the script:
//   - Shell.Application.Windows() enumerates all ShellWindow objects (may
//     include Internet Explorer or other IWebBrowser2 hosts on older Windows).
//   - We filter by Name containing "Explorer" and by the presence of a
//     Document.Folder object to skip non-folder views (e.g. the "Home" pane
//     or search results, which return null for Document.Folder).
//   - The try/catch avoids hard failures when a partially-initialised window
//     lacks a Document object — common on Windows 11 when a new tab is opening.
const explorerFoldersPS = `
$sh = New-Object -ComObject Shell.Application
$sh.Windows() | Where-Object { $_.Name -match 'Explorer' } | ForEach-Object {
    try {
        $path = $_.Document.Folder.Self.Path
        if ($path) { Write-Output $path }
    } catch { }
}
`

// foregroundFolderPS extends explorerFoldersPS to identify the foreground
// (active) Explorer window. It emits the foreground path on the first line
// with a sentinel prefix, then all remaining Explorer paths.
//
// Output format:
//
//	FOREGROUND:<path or empty>
//	<other-path-1>
//	<other-path-2>
//	...
//
// The Add-Type P/Invoke is used to call GetForegroundWindow from managed code.
// -ErrorAction SilentlyContinue on Add-Type prevents a hard stop if the type
// was already defined earlier in the session.
const foregroundFolderPS = `
Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public class BeckyWin32 {
    [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
}
'@ -ErrorAction SilentlyContinue

$fgHwnd = [BeckyWin32]::GetForegroundWindow()
$sh = New-Object -ComObject Shell.Application
$fgPath = ""
$others = @()
$sh.Windows() | Where-Object { $_.Name -match 'Explorer' } | ForEach-Object {
    try {
        $path = $_.Document.Folder.Self.Path
        if ($path) {
            if ($_.HWND -eq $fgHwnd) { $fgPath = $path }
            else { $others += $path }
        }
    } catch { }
}
Write-Output "FOREGROUND:$fgPath"
$others | ForEach-Object { Write-Output $_ }
`

// runPS executes a PowerShell script string and returns stdout as a string.
// It follows the same exec pattern established in cmd/canvas/gui_browse.go.
func runPS(ctx context.Context, script string) (string, error) {
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive",
		"-ExecutionPolicy", "Bypass", "-Command", script)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("winctx: powershell query failed: %w", err)
	}
	return string(out), nil
}

// openExplorerFolders is the Windows implementation of OpenExplorerFolders.
func openExplorerFolders() ([]ExplorerWindow, error) {
	ctx, cancel := context.WithTimeout(context.Background(), explorerQueryTimeout)
	defer cancel()
	raw, err := runPS(ctx, explorerFoldersPS)
	if err != nil {
		return nil, err
	}
	return parseExplorerOutput(raw), nil
}

// foregroundExplorerFolder is the Windows implementation of ForegroundExplorerFolder.
func foregroundExplorerFolder() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), explorerQueryTimeout)
	defer cancel()
	raw, err := runPS(ctx, foregroundFolderPS)
	if err != nil {
		return "", err
	}
	return parseForegroundOutput(raw), nil
}

// parseForegroundOutput extracts the foreground path from the output of
// foregroundFolderPS. The first line is "FOREGROUND:<path>". If that path is
// empty the foreground window was not Explorer, so we fall back to the first
// path in the remaining lines (any open Explorer window).
func parseForegroundOutput(raw string) string {
	const prefix = "FOREGROUND:"
	first, rest, _ := strings.Cut(raw, "\n")
	first = strings.TrimRight(first, "\r")
	if strings.HasPrefix(first, prefix) {
		if fg := strings.TrimSpace(first[len(prefix):]); fg != "" {
			return fg
		}
	}
	// Fall back to first available Explorer folder.
	if windows := parseExplorerOutput(rest); len(windows) > 0 {
		return windows[0].Path
	}
	return ""
}
