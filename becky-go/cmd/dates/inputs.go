// inputs.go — expand the CLI arguments into a list of media files to date, with
// a media-extension allow-list. A folder expands to its media files (optionally
// recursive); a file is taken as-is. Non-media and unreadable paths become
// skipped records (degrade-never-crash), not fatal errors.
package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/pathx"
)

// mediaExts is the set of recognized video/media extensions (lowercase, with dot).
var mediaExts = map[string]bool{
	".mp4": true, ".mov": true, ".mkv": true, ".m4v": true, ".avi": true,
	".webm": true, ".wmv": true, ".flv": true, ".mpg": true, ".mpeg": true,
	".3gp": true, ".ts": true, ".m2ts": true, ".mts": true,
}

// isMedia reports whether a path has a recognized media extension.
func isMedia(path string) bool {
	ext := strings.ToLower(filepath.Ext(pathx.Base(path)))
	return mediaExts[ext]
}

// expandInputs turns the args into (files-to-date, skipped). Deterministic order.
func expandInputs(args []string, recursive bool) ([]string, []SkipRecord) {
	var files []string
	var skipped []SkipRecord

	for _, a := range args {
		fi, err := os.Stat(a)
		if err != nil {
			skipped = append(skipped, SkipRecord{SourceFile: a, Reason: "cannot stat: " + err.Error()})
			continue
		}
		if fi.IsDir() {
			fs, sk := expandDir(a, recursive)
			files = append(files, fs...)
			skipped = append(skipped, sk...)
			continue
		}
		// A specific file: take it even if extension is unknown only when it looks
		// like media; otherwise skip with a reason (matches the spec example).
		if isMedia(a) {
			files = append(files, a)
		} else {
			skipped = append(skipped, SkipRecord{SourceFile: a, Reason: "not a media file"})
		}
	}

	sort.Strings(files)
	return dedupe(files), skipped
}

// expandDir lists media files in a directory (optionally recursive).
func expandDir(dir string, recursive bool) ([]string, []SkipRecord) {
	var files []string
	var skipped []SkipRecord

	if recursive {
		_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable subtrees, keep going
			}
			if d.IsDir() {
				return nil
			}
			if isMedia(p) {
				files = append(files, p)
			}
			return nil
		})
		return files, skipped
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		skipped = append(skipped, SkipRecord{SourceFile: dir, Reason: "cannot read dir: " + err.Error()})
		return files, skipped
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if isMedia(p) {
			files = append(files, p)
		}
	}
	return files, skipped
}

// dedupe removes duplicate paths while preserving (sorted) order.
func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
