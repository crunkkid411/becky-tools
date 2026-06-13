// io.go — input plumbing for becky-cluster: deterministic ids, provenance hashing,
// glob expansion, harvesting appearances from existing becky-identify JSON, and
// parsing the voice helper's stdout. Keeps main.go focused on orchestration.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// sha12 returns the first 12 hex chars of the SHA-256 of s. Matches becky-embed's
// sha12 so a source-file provenance hash is consistent across tools.
func sha12(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:12]
}

// appearanceID derives a stable id for one appearance, mirroring the SPEC §7
// scheme: sha12(source_file) + ":" + modality + ":" + frame_index. Deterministic
// so re-runs are idempotent (same clip+modality+frame -> same id).
func appearanceID(sourceFile, modality string, frameIndex int) string {
	return fmt.Sprintf("%s:%s:%d", sha12(sourceFile), modality, frameIndex)
}

// expandClips resolves the positional inputs into clip paths: a directory expands
// to its video/audio files (one level), a glob expands to its matches, and a plain
// file passes through. Unreadable entries are skipped. Result is sorted + deduped
// for deterministic ordering.
func expandClips(inputs []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if !seen[abs] {
			seen[abs] = true
			out = append(out, abs)
		}
	}
	for _, in := range inputs {
		fi, err := os.Stat(in)
		switch {
		case err == nil && fi.IsDir():
			for _, f := range mediaFilesIn(in) {
				add(f)
			}
		case err == nil:
			add(in)
		default:
			// Treat as a glob pattern.
			matches, gerr := filepath.Glob(in)
			if gerr != nil || len(matches) == 0 {
				return nil, fmt.Errorf("no such file, dir, or glob match: %s", in)
			}
			for _, m := range matches {
				if mi, merr := os.Stat(m); merr == nil && !mi.IsDir() {
					add(m)
				}
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// mediaExts is the set of clip extensions expandClips picks up from a directory.
var mediaExts = map[string]bool{
	".mp4": true, ".mov": true, ".mkv": true, ".avi": true, ".webm": true,
	".m4v": true, ".wav": true, ".mp3": true, ".m4a": true, ".flac": true,
}

// mediaFilesIn returns the media files directly inside dir (non-recursive).
func mediaFilesIn(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if mediaExts[strings.ToLower(filepath.Ext(e.Name()))] {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files)
	return files
}

// identifyDoc is the subset of a becky-identify JSON we harvest appearances from.
// Each identify.json describes ONE source file; the unidentified[] entries are the
// strangers we want to cluster across the corpus. (Named identifications are known
// people, not the clustering target, but we still record them when they carry a
// usable embedding — currently they do not, so only provenance is harvested.)
type identifyDoc struct {
	File         string `json:"file"`
	Unidentified []struct {
		Type       string  `json:"type"`
		SpeakerID  string  `json:"speaker_id"`
		Confidence float64 `json:"confidence"`
	} `json:"unidentified"`
}

// harvestIdentifyGlob is a provenance-only harvest: today becky-identify discards
// embeddings after matching, so identify.json carries no vectors. This records
// which clips had an unidentified person of the requested modality so the operator
// sees coverage; vectors must come from --embed (raw clips) or --db (stored
// appearance_embeddings). Returns the matched files for the run notes.
func harvestIdentifyGlob(pattern, modality string) (files []string, err error) {
	matches, gerr := filepath.Glob(pattern)
	if gerr != nil {
		return nil, fmt.Errorf("bad --identify-glob pattern: %w", gerr)
	}
	for _, m := range matches {
		data, rerr := os.ReadFile(m)
		if rerr != nil {
			continue
		}
		var doc identifyDoc
		if json.Unmarshal(data, &doc) != nil {
			continue
		}
		for _, u := range doc.Unidentified {
			if modality == "both" || u.Type == modality {
				files = append(files, doc.File)
				break
			}
		}
	}
	sort.Strings(files)
	return files, nil
}

// voiceHelperJSON mirrors voice_embed.py's stdout (same shape cmd/identify parses).
type voiceHelperJSON struct {
	Skipped    bool   `json:"skipped"`
	Reason     string `json:"reason"`
	Dim        int    `json:"dim"`
	Embeddings []struct {
		Path   string    `json:"path"`
		Vector []float64 `json:"vector"`
	} `json:"embeddings"`
}

// parseVoiceHelperJSON tolerates banner noise by scanning bottom-up for the JSON
// line (sherpa/torch can print warnings before the result), mirroring faceembed.
func parseVoiceHelperJSON(s string) (voiceHelperJSON, bool) {
	if r, ok := tryVoiceJSON(strings.TrimSpace(s)); ok {
		return r, true
	}
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if r, ok := tryVoiceJSON(line); ok {
			return r, true
		}
	}
	return voiceHelperJSON{}, false
}

func tryVoiceJSON(s string) (voiceHelperJSON, bool) {
	var r voiceHelperJSON
	if json.Unmarshal([]byte(s), &r) == nil && (r.Skipped || r.Embeddings != nil || r.Dim > 0) {
		return r, true
	}
	return voiceHelperJSON{}, false
}
