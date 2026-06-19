// kitportable.go — relative path storage, content hash, and relink helper for kits.
//
// A kit saved on one machine has absolute sample paths that break on another machine
// (or when the sample library is moved). This file implements:
//
//   - RelativiseKitPaths: converts Pad.SamplePath (and Sound variant paths) to paths
//     relative to a given root directory, and computes a SHA-256 content hash per pad
//     for the relinker.
//
//   - RelinkKit: given a new root directory, restores absolute paths using exact
//     relative resolution first, SHA-256 content matching as a fallback.
//
// Design: pure Go, offline, deterministic, degrade-never-crash (CLAUDE.md).
// A sample that cannot be relinked stays as-is with a note; nothing panics.
package drummachine

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/pathx"
	"becky-go/internal/sampler"
)

// KitPortableResult is returned by RelativiseKitPaths and RelinkKit.
type KitPortableResult struct {
	Kit   Kit
	Notes []string // non-fatal observations
}

// RelativiseKitPaths returns a copy of the kit where every absolute sample path is
// converted to a path relative to root. Paths that are already relative, or that lie
// outside root (cannot be made relative), are left as-is with a note.
//
// For each Pad.Sound, all Variant.SamplePath values are updated. Pad.SamplePath
// mirrors the first variant path (or stays as-is if no Sound).
//
// Content hashes are returned in the second return value (pad index → hex SHA-256)
// so callers can save them in a sidecar alongside machine.json. The FROZEN
// sampler.Sound contract is not modified to carry hashes.
func RelativiseKitPaths(kit Kit, root string) (KitPortableResult, map[int]string) {
	out := cloneKit(kit)
	var notes []string
	padHashes := map[int]string{}

	for i := range out.Pads {
		pad := &out.Pads[i]
		if pad.Sound != nil {
			cp := *pad.Sound
			relSound(&cp, root, &notes)
			pad.Sound = &cp
			pad.SamplePath = soundFirstPath(pad.Sound)
			if pad.SamplePath != "" {
				abs := pad.SamplePath
				if !filepath.IsAbs(abs) {
					abs = filepath.Join(root, abs)
				}
				if h := hashFile(abs); h != "" {
					padHashes[i] = h
				}
			}
		} else if pad.SamplePath != "" {
			rel, ok := makeRelative(pad.SamplePath, root)
			if ok {
				pad.SamplePath = rel
			} else {
				notes = append(notes, "pad "+padIndexStr(i)+": cannot relativise "+pathx.Base(pad.SamplePath))
			}
			if h := hashFile(pad.SamplePath); h != "" {
				padHashes[i] = h
			}
		}
	}
	return KitPortableResult{Kit: out, Notes: notes}, padHashes
}

// RelinkKit attempts to restore absolute paths in a kit that was saved with relative
// paths. For each pad:
//  1. Resolve relative path against newRoot — if the file exists, done.
//  2. If a SHA-256 hash is provided in padHashes, walk newRoot recursively for a
//     file whose hash matches and use the first match (sorted, deterministic walk).
//  3. If neither strategy works, leave the path as-is with a note.
func RelinkKit(kit Kit, newRoot string, padHashes map[int]string) KitPortableResult {
	out := cloneKit(kit)
	var notes []string

	for i := range out.Pads {
		pad := &out.Pads[i]
		want := pad.SamplePath
		if want == "" {
			continue
		}
		// Strategy 1: resolve relative/absolute against newRoot.
		candidate := want
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(newRoot, want)
		}
		if fileExistsKit(candidate) {
			if pad.Sound != nil {
				relinkSoundPaths(pad.Sound, want, candidate)
			}
			pad.SamplePath = candidate
			continue
		}
		// Strategy 2: SHA-256 content match.
		if wantHash, ok := padHashes[i]; ok && wantHash != "" {
			found := findByHash(newRoot, wantHash)
			if found != "" {
				if pad.Sound != nil {
					relinkSoundPaths(pad.Sound, want, found)
				}
				pad.SamplePath = found
				notes = append(notes, "pad "+padIndexStr(i)+" relinked by content hash to "+pathx.Base(found))
				continue
			}
		}
		notes = append(notes, "pad "+padIndexStr(i)+": could not relink "+pathx.Base(want))
	}
	return KitPortableResult{Kit: out, Notes: notes}
}

// ---- internal helpers -------------------------------------------------------

// relSound converts all SamplePath values inside snd to paths relative to root.
func relSound(snd *sampler.Sound, root string, notes *[]string) {
	for li := range snd.Layers {
		for vi := range snd.Layers[li].RoundRobin {
			v := &snd.Layers[li].RoundRobin[vi]
			if v.SamplePath == "" {
				continue
			}
			rel, ok := makeRelative(v.SamplePath, root)
			if ok {
				v.SamplePath = rel
			} else {
				*notes = append(*notes, "cannot relativise "+pathx.Base(v.SamplePath))
			}
		}
	}
}

// relinkSoundPaths replaces every variant SamplePath whose basename matches
// pathx.Base(oldRel) with newAbs inside snd.
func relinkSoundPaths(snd *sampler.Sound, oldRel, newAbs string) {
	oldBase := pathx.Base(oldRel)
	for li := range snd.Layers {
		for vi := range snd.Layers[li].RoundRobin {
			v := &snd.Layers[li].RoundRobin[vi]
			if pathx.Base(v.SamplePath) == oldBase {
				v.SamplePath = newAbs
			}
		}
	}
}

// makeRelative converts absPath to a path relative to root using filepath.Rel.
// Returns (absPath, false) on failure (path is outside root or on error).
func makeRelative(absPath, root string) (string, bool) {
	norm := func(p string) string {
		return filepath.Clean(strings.ReplaceAll(p, `\`, `/`))
	}
	rel, err := filepath.Rel(norm(root), norm(absPath))
	if err != nil || strings.HasPrefix(rel, "..") {
		return absPath, false
	}
	return rel, true
}

// hashFile computes the hex-encoded SHA-256 of a file. Returns "" on any error.
func hashFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// findByHash walks root (sorted walk via filepath.WalkDir) for a file whose
// SHA-256 matches want. Returns the first match path, or "" if none found.
func findByHash(root, want string) string {
	var result string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if h := hashFile(path); h == want {
			result = path
			return filepath.SkipAll
		}
		return nil
	})
	return result
}

// fileExistsKit reports whether path is a regular file.
func fileExistsKit(p string) bool {
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// padIndexStr formats a pad index as a string for notes.
func padIndexStr(i int) string {
	s := [16]string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "13", "14", "15"}
	if i >= 0 && i < len(s) {
		return s[i]
	}
	return "?"
}
