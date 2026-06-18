package footage

// meta.go owns the <video>.beckymeta.json sidecar — the ONLY place per-video
// metadata is stored (SPEC-BECKY-CLIP §4). The original video is never touched:
// the sidecar lives beside it as "<full-video-name>.beckymeta.json", e.g.
// "2026-06-14_ring.mp4.beckymeta.json". A missing sidecar is not an error — it
// means empty Meta. SaveMeta writes the sidecar atomically (temp file + rename)
// so a crash mid-write can't corrupt an existing sidecar.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// metaSuffix is appended to the FULL video filename (including its extension) to
// form the metadata sidecar name. Appending to the full name (not the stem)
// avoids collisions when "clip.mp4" and "clip.mov" sit in one folder.
const metaSuffix = ".beckymeta.json"

// MetaPath returns the beckymeta sidecar path for a video.
func MetaPath(videoPath string) string {
	return videoPath + metaSuffix
}

// LoadMeta reads the <video>.beckymeta.json sidecar. A missing sidecar returns a
// zero Meta and nil error (empty metadata is the normal case). A present but
// unreadable/corrupt sidecar returns a wrapped error so a caller can surface it,
// but callers that prefer to degrade can use the package-internal loadMetaQuiet.
func LoadMeta(videoPath string) (Meta, error) {
	path := MetaPath(videoPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Meta{}, nil // no sidecar yet — empty meta, not an error
		}
		return Meta{}, fmt.Errorf("read beckymeta %s: %w", filepath.Base(path), err)
	}
	var m Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return Meta{}, fmt.Errorf("parse beckymeta %s: %w", filepath.Base(path), err)
	}
	return m, nil
}

// loadMetaQuiet is LoadMeta's degrade-never-crash sibling used during indexing:
// any failure (missing OR corrupt) yields (zero, false) so a single bad sidecar
// never aborts a whole-folder walk.
func loadMetaQuiet(videoPath string) (Meta, bool) {
	if _, statErr := os.Stat(MetaPath(videoPath)); statErr != nil {
		return Meta{}, false // no sidecar present
	}
	m, err := LoadMeta(videoPath)
	if err != nil {
		return Meta{}, false // present but corrupt — degrade, don't abort the walk
	}
	return m, true
}

// SaveMeta writes the <video>.beckymeta.json sidecar for videoPath. This is the
// ONLY mutating operation in package footage, and it writes the SIDECAR, never
// the video. The write is atomic (temp file in the same directory + rename) so a
// failed/interrupted write cannot leave a half-written sidecar. The video file
// is not opened, stat'd-for-write, or modified in any way.
func SaveMeta(videoPath string, m Meta) error {
	path := MetaPath(videoPath)
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal beckymeta: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".beckymeta-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp beckymeta in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write temp beckymeta: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp beckymeta: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("commit beckymeta %s: %w", filepath.Base(path), err)
	}
	return nil
}
