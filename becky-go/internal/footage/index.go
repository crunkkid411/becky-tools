// Package footage is the read-only case-folder index that anchors becky-clip's
// 500 GB retrieval funnel (SPEC-BECKY-CLIP §8, R-AI §3.1). It walks a case folder
// for video files, finds each video's transcript sidecar (.srt/.en.srt/.json3 via
// internal/sidecar) and its <video>.beckymeta.json metadata sidecar, and exposes
// deterministic candidate retrieval (GrepTranscripts) — the Tier-0 base of the
// funnel.
//
// HARD INVARIANT: the original video bytes are NEVER opened or modified. The
// index reads only filenames, transcript text, and the small JSON metadata
// sidecar. The ONLY write this package performs is SaveMeta, which writes the
// <video>.beckymeta.json sidecar — never the video. (CLAUDE.md: "never modify
// originals"; degrade-never-crash on any unreadable file.)
//
// Semantic retrieval (vector search over forensic.db) is delegated to the
// existing becky-search binary by the caller (exec); this package provides only
// the deterministic keyword grep so go test stays green with no DB/model present.
package footage

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/sidecar"
)

// videoExts is the set of file extensions treated as source video. Lowercased,
// dot-prefixed. Conservative: common container formats a detective's footage
// library uses; audio-only and image files are excluded.
var videoExts = map[string]bool{
	".mp4": true, ".mov": true, ".mkv": true, ".avi": true,
	".m4v": true, ".webm": true, ".mpg": true, ".mpeg": true,
	".wmv": true, ".flv": true, ".ts": true, ".mts": true,
	".m2ts": true, ".3gp": true, ".vob": true,
}

// Meta is the per-video metadata sidecar shape (SPEC-BECKY-CLIP §4 ClipMeta /
// the <video>.beckymeta.json contract). All fields optional; a missing sidecar
// yields a zero Meta, not an error. Date is ISO YYYY-MM-DD. SourceFPS feeds the
// forensic original-timecode burn.
type Meta struct {
	Date      string  `json:"date,omitempty"`       // recording date, ISO YYYY-MM-DD
	Link      string  `json:"link,omitempty"`       // source URL if known
	Person    string  `json:"person,omitempty"`     // primary person on screen
	Location  string  `json:"location,omitempty"`   // location if known
	SourceFPS float64 `json:"source_fps,omitempty"` // original frame rate for timecode burn
}

// Video is one indexed source video and its discovered sidecars. TranscriptPath
// is "" when no transcript sidecar was found; Meta is zero when no beckymeta
// sidecar was found. The video bytes themselves are never read.
type Video struct {
	Path           string `json:"path"`            // absolute path to the source video
	Name           string `json:"name"`            // basename (with extension)
	TranscriptPath string `json:"transcript_path"` // sidecar (.srt/.vtt/.json3) or ""
	HasTranscript  bool   `json:"has_transcript"`
	Meta           Meta   `json:"meta"`
}

// FolderIndex is the in-memory map of a case folder: every video plus its
// sidecars. It carries no media bytes — filenames + small JSON only, so it stays
// in the megabytes regardless of the folder's terabytes.
type FolderIndex struct {
	Root   string  `json:"root"`   // absolute case-folder root (the search scope)
	Videos []Video `json:"videos"` // sorted by Path for deterministic output
}

// Index walks folder recursively and builds a FolderIndex: every video file (by
// extension), with its transcript sidecar and beckymeta sidecar resolved.
// Read-only: it lists directory entries and reads transcript/JSON sidecars; it
// never opens a video. Unreadable subdirectories are skipped (degrade, never
// crash). The returned Videos slice is sorted by Path so the output is
// deterministic across platforms.
func Index(folder string) (FolderIndex, error) {
	abs, err := filepath.Abs(folder)
	if err != nil {
		abs = folder
	}
	idx := FolderIndex{Root: abs, Videos: []Video{}}

	walkErr := filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Unreadable entry/dir: skip its subtree but keep walking the rest.
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !videoExts[ext] {
			return nil
		}
		v := Video{
			Path: path,
			Name: filepath.Base(path),
		}
		if sub := sidecar.FindSubtitle(path); sub != "" {
			v.TranscriptPath = sub
			v.HasTranscript = true
		}
		if m, ok := loadMetaQuiet(path); ok {
			v.Meta = m
		}
		idx.Videos = append(idx.Videos, v)
		return nil
	})
	if walkErr != nil {
		return idx, walkErr
	}

	sort.Slice(idx.Videos, func(i, j int) bool { return idx.Videos[i].Path < idx.Videos[j].Path })
	return idx, nil
}

// VideoByName returns the indexed video whose basename equals name (the GUI/AI
// refer to a source by filename, not absolute path). The second result is false
// when no such video is indexed.
func (fi FolderIndex) VideoByName(name string) (Video, bool) {
	for _, v := range fi.Videos {
		if v.Name == name {
			return v, true
		}
	}
	return Video{}, false
}

// WithTranscripts returns only the videos that have a transcript sidecar — the
// subset the funnel can grep.
func (fi FolderIndex) WithTranscripts() []Video {
	out := make([]Video, 0, len(fi.Videos))
	for _, v := range fi.Videos {
		if v.HasTranscript {
			out = append(out, v)
		}
	}
	return out
}
