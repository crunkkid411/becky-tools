// target.go — the "target context": the file(s) or folder a user drags onto
// becky-ask.exe (passed as argv) or pastes/types into the chat. On Windows,
// dropping a file/folder onto the .exe hands its path(s) to the program as
// command-line arguments; becky-ask treats those paths as "this is what I'm
// referring to" so the obvious operations can run with no typing.
//
// This file is pure path logic (no TUI, no exec) so it is fully unit-testable
// headless — which is how the no-TTY verification proves "argv path -> Target".
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// targetKind distinguishes a single file, a folder, or several files — the three
// shapes the brief calls for ("a file (or even an entire folder)" and "multiple
// files").
type targetKind int

const (
	targetNone targetKind = iota
	targetFile
	targetDir
	targetMulti
)

// Target is the dropped/typed context becky-ask reasons about. Paths are absolute
// and verified to exist; missing or unreadable inputs are dropped (with a note)
// rather than faked, so a stale path never drives a command.
type Target struct {
	Paths   []string   // absolute, existing paths (file(s) or a single dir)
	Kind    targetKind // file | dir | multi | none
	Missing []string   // raw args that did not resolve to an existing path (for an honest note)
}

// HasTarget reports whether at least one real path resolved.
func (t Target) HasTarget() bool { return len(t.Paths) > 0 }

// Primary returns the first resolved path — the one a single-target op runs on.
func (t Target) Primary() string {
	if len(t.Paths) == 0 {
		return ""
	}
	return t.Paths[0]
}

// Label renders the one-line "Target: <…>" string shown in the UI. It keeps the
// base name(s) so the bar stays readable even for deep paths, and is explicit
// about a folder vs N files.
func (t Target) Label() string {
	switch t.Kind {
	case targetNone:
		return ""
	case targetDir:
		return fmt.Sprintf("folder %s", filepath.Base(t.Primary()))
	case targetFile:
		return filepath.Base(t.Primary())
	case targetMulti:
		names := make([]string, 0, len(t.Paths))
		for _, p := range t.Paths {
			names = append(names, filepath.Base(p))
		}
		// Keep it short: show the first two, then a count.
		if len(names) > 2 {
			return fmt.Sprintf("%s, %s + %d more (%d files)",
				names[0], names[1], len(names)-2, len(names))
		}
		return strings.Join(names, ", ")
	}
	return ""
}

// IsImageLike reports whether the primary target looks like a still image, which
// is the only single-file shape becky-ocr can read directly (it OCRs frames, not
// raw video). Used to decide whether the OCR quick-action applies to a file.
func (t Target) IsImageLike() bool {
	if t.Kind != targetFile {
		return false
	}
	return isImageExt(filepath.Ext(t.Primary()))
}

// IsVideoLike reports whether the primary target looks like a video/audio clip
// the ingest ops (transcribe/identify/describe/cut) operate on.
func (t Target) IsVideoLike() bool {
	if t.Kind != targetFile {
		return false
	}
	return isMediaExt(filepath.Ext(t.Primary()))
}

// resolveTarget turns raw argv (or pasted) paths into a Target. It strips the
// surrounding quotes Windows adds when a path contains spaces, makes each path
// absolute, and keeps only those that exist. A lone existing directory becomes a
// targetDir; one existing file a targetFile; several files a targetMulti. Args
// that don't resolve are recorded in Missing for an honest "couldn't find" note.
func resolveTarget(args []string) Target {
	var t Target
	var dirs, files []string
	for _, raw := range args {
		p := cleanPath(raw)
		if p == "" {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		fi, statErr := os.Stat(abs)
		if statErr != nil {
			t.Missing = append(t.Missing, raw)
			continue
		}
		if fi.IsDir() {
			dirs = append(dirs, abs)
		} else {
			files = append(files, abs)
		}
	}

	switch {
	case len(dirs) > 0:
		// A folder is the dominant context; if both a folder and files were dropped,
		// the folder wins (it is the broader "this is what I mean"). Use the first dir.
		t.Paths = []string{dirs[0]}
		t.Kind = targetDir
	case len(files) == 1:
		t.Paths = files
		t.Kind = targetFile
	case len(files) > 1:
		t.Paths = files
		t.Kind = targetMulti
	default:
		t.Kind = targetNone
	}
	return t
}

// cleanPath trims whitespace and the double quotes Windows wraps around dropped
// paths that contain spaces (e.g. "C:\My Cases\clip 1.mp4").
func cleanPath(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.Trim(s, `"`)
	return strings.TrimSpace(s)
}

// isMediaExt reports whether ext (with leading dot, any case) is a video/audio
// container becky's ingest tools accept.
func isMediaExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".mp4", ".mov", ".mkv", ".avi", ".webm", ".m4v", ".wmv", ".flv",
		".mp3", ".wav", ".m4a", ".aac", ".flac", ".ogg", ".opus":
		return true
	}
	return false
}

// isImageExt reports whether ext (with leading dot, any case) is a still image
// becky-ocr can read directly.
func isImageExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".png", ".bmp", ".webp", ".tif", ".tiff", ".gif":
		return true
	}
	return false
}
