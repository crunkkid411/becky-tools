package main

// save.go (no build tag — compiled into both the gui and headless builds, and unit-
// tested headlessly) holds the pure session-save logic: deciding WHERE a "save" /
// "save as" writes, and the marshal+write itself. Closes GAP-ANALYSIS #2 ("the canvas
// can load+edit but cannot save"). The GUI wiring (gui_spine.go) is a thin call over
// this.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/dawmodel"
)

// deriveSavePath decides the file a save writes to:
//   - asName given      → "<name>.json" in the session's (or target's) directory,
//   - else sessionPath  → overwrite the loaded session,
//   - else target dir/file → "becky-session.json" beside it,
//   - else              → "becky-session.json" in the cwd.
//
// It never returns "" for a non-degenerate input.
func deriveSavePath(sessionPath, target, asName string) string {
	dir := dirOf(sessionPath, target)
	if n := strings.TrimSpace(asName); n != "" {
		if !strings.HasSuffix(strings.ToLower(n), ".json") {
			n += ".json"
		}
		// If the name already carries a directory, honor it; else place it in dir.
		if filepath.Dir(n) != "." || dir == "" {
			return n
		}
		return filepath.Join(dir, n)
	}
	if strings.TrimSpace(sessionPath) != "" {
		return sessionPath
	}
	if dir == "" {
		return "becky-session.json"
	}
	return filepath.Join(dir, "becky-session.json")
}

// dirOf returns the directory to anchor a save in: the session file's dir, else the
// target's dir (or the target itself if it's a directory), else "".
func dirOf(sessionPath, target string) string {
	if s := strings.TrimSpace(sessionPath); s != "" {
		return filepath.Dir(s)
	}
	t := strings.TrimSpace(target)
	if t == "" {
		return ""
	}
	if fi, err := os.Stat(t); err == nil && fi.IsDir() {
		return t
	}
	return filepath.Dir(t)
}

// saveArrangementJSON marshals an arrangement to indented JSON and writes it.
func saveArrangementJSON(arr *dawmodel.Arrangement, path string) error {
	data, err := json.MarshalIndent(arr, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
