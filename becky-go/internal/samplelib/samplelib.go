// Package samplelib scans a producer's messy sample library (e.g. X:\music-2\SAMPLES,
// X:\Splice) and builds a searchable, deterministic index a drum machine can query.
//
// It is pure Go (stdlib only) — no cgo, no build tags, no new module dependencies.
// It implements SPEC-BECKY-DRUM.md §4 (the [CLOUD] index half): role / loop-vs-one-shot
// / BPM / key classification from filename + parent-folder tokens and a minimal WAV
// header read, following becky's corroborate-then-conclude rule: a role is only
// CONCLUDED when the filename and the folder agree; a lone weak token stays
// low-confidence and nothing at all stays unknown. We never guess a role wrong over
// saying "unknown".
//
// Reading the WAV duration is header-only (fmt + data chunk sizes) — we never decode
// samples, so scanning stays fast and memory-light over multi-GB drives. Other audio
// formats (aif/aiff/flac) are indexed for path/name/role/token signals but carry an
// unknown duration (no header reader here; that is a clean Phase-2 follow-up, see below).
//
// Phase-2 follow-ups (intentionally NOT built here to avoid a cross-package
// dependency): read the WAV `acid`/`smpl` chunks (one-shot-vs-loop flag, float BPM,
// loop points, root note) via internal/sampledecode for far stronger loop/tempo/key
// signals, and an AIFF/FLAC header reader for their durations. Those slot in behind the
// same Sample fields without changing this API.
package samplelib

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"becky-go/internal/pathx"
)

// Confidence is how strongly a Sample's Role is concluded.
const (
	ConfHigh    = "high"    // filename AND folder agree on the role
	ConfLow     = "low"     // a lone token (filename OR folder) hints at the role
	ConfUnknown = "unknown" // no token agreed; role left as RoleUnknown
)

// Kind classifies a sample as a one-shot hit or a loop.
const (
	KindOneShot = "oneshot"
	KindLoop    = "loop"
	KindUnknown = "unknown"
)

// Role names (kept stable for callers; RoleUnknown means "we will not guess").
const (
	RoleKick    = "kick"
	RoleSnare   = "snare"
	RoleHat     = "hat"
	RoleClap    = "clap"
	RoleTom     = "tom"
	RoleCrash   = "crash"
	RoleRide    = "ride"
	RolePerc    = "perc"
	RoleBass    = "bass"
	RoleVocal   = "vocal"
	RoleFX      = "fx"
	RoleUnknown = "unknown"
)

// Sample is one indexed audio file.
type Sample struct {
	Path           string  `json:"path"`
	Name           string  `json:"name"` // base file name (separator-agnostic)
	Role           string  `json:"role"`
	RoleConfidence string  `json:"role_confidence"` // high | low | unknown
	Kind           string  `json:"kind"`            // oneshot | loop | unknown
	BPM            float64 `json:"bpm"`             // 0 = unknown
	Key            string  `json:"key"`             // "" = unknown
	DurationSec    float64 `json:"duration_sec"`    // 0 = unknown
	SizeBytes      int64   `json:"size_bytes"`
}

// Skip records a file we deliberately did not index, with a plain reason.
type Skip struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Index is the searchable result of a Scan. Samples are sorted by Path.
type Index struct {
	Root       string         `json:"root"`
	Samples    []Sample       `json:"samples"`
	Skipped    []Skip         `json:"skipped"`
	RoleCounts map[string]int `json:"role_counts"`
}

// ScanOptions controls a Scan.
type ScanOptions struct {
	Recursive bool     // walk subdirectories (default: top level only)
	Exts      []string // audio extensions to index; defaults to wav/aif/aiff/flac
	MaxFiles  int      // safety cap on files indexed (<=0 => DefaultMaxFiles)
}

// DefaultMaxFiles bounds an unbounded library scan.
const DefaultMaxFiles = 200000

// DefaultExts are the extensions Scan indexes when none are given.
var DefaultExts = []string{".wav", ".aif", ".aiff", ".flac"}

// Scan walks root and builds an Index. It degrades, never crashes: an unreadable or
// malformed file is recorded in Skipped with a reason rather than aborting the scan.
// Symlinked directories are not followed (bounded + loop-safe). The walk is
// deterministic (sorted by path).
func Scan(root string, opts ScanOptions) (*Index, error) {
	if root == "" {
		return nil, errors.New("samplelib: empty root")
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("samplelib: root is not a directory")
	}

	exts := normalizeExts(opts.Exts)
	max := opts.MaxFiles
	if max <= 0 {
		max = DefaultMaxFiles
	}

	idx := &Index{Root: root, RoleCounts: map[string]int{}}
	capped := false

	walkFn := func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// Report the unreadable entry and keep going.
			idx.Skipped = append(idx.Skipped, Skip{Path: path, Reason: "walk error: " + walkErr.Error()})
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			if !opts.Recursive {
				return fs.SkipDir
			}
			// Do not descend into symlinked dirs (loop/escape safety).
			if isSymlink(d) {
				return fs.SkipDir
			}
			return nil
		}
		// Regular files only; never follow file symlinks.
		if isSymlink(d) {
			idx.Skipped = append(idx.Skipped, Skip{Path: path, Reason: "symlink skipped"})
			return nil
		}
		if len(idx.Samples) >= max {
			capped = true
			return fs.SkipAll
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !exts[ext] {
			idx.Skipped = append(idx.Skipped, Skip{Path: path, Reason: "non-audio extension"})
			return nil
		}
		s, skip := classify(path, root, d)
		if skip != nil {
			idx.Skipped = append(idx.Skipped, *skip)
			return nil
		}
		idx.Samples = append(idx.Samples, s)
		return nil
	}

	if err := filepath.WalkDir(root, walkFn); err != nil {
		return nil, err
	}
	if capped {
		idx.Skipped = append(idx.Skipped, Skip{Path: root, Reason: "max files cap reached (" + strconv.Itoa(max) + ")"})
	}

	sort.Slice(idx.Samples, func(i, j int) bool { return idx.Samples[i].Path < idx.Samples[j].Path })
	sort.Slice(idx.Skipped, func(i, j int) bool { return idx.Skipped[i].Path < idx.Skipped[j].Path })
	for _, s := range idx.Samples {
		idx.RoleCounts[s.Role]++
	}
	return idx, nil
}

func isSymlink(d fs.DirEntry) bool { return d.Type()&fs.ModeSymlink != 0 }

// normalizeExts lowercases, dot-prefixes, and de-dups the extension set.
func normalizeExts(in []string) map[string]bool {
	src := in
	if len(src) == 0 {
		src = DefaultExts
	}
	out := make(map[string]bool, len(src))
	for _, e := range src {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		out[e] = true
	}
	return out
}

// classify turns a file path into a Sample, or returns a Skip (e.g. unreadable stat,
// zero-size garbage). Uses pathx for separator-agnostic Base/Dir so Windows paths work
// even when running on Linux/CI.
func classify(path, root string, d fs.DirEntry) (Sample, *Skip) {
	fi, err := d.Info()
	if err != nil {
		return Sample{}, &Skip{Path: path, Reason: "stat failed: " + err.Error()}
	}
	if fi.Size() == 0 {
		return Sample{}, &Skip{Path: path, Reason: "empty file"}
	}

	name := pathx.Base(path)
	folder := pathx.Base(pathx.Dir(path))

	role, conf := guessRole(name, folder)
	bpm := bpmFromName(name)
	key := keyFromName(name)

	var dur float64
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".wav" {
		if d, derr := wavDurationSec(path); derr == nil {
			dur = d
		}
		// A bad WAV header is not fatal: we still index it with unknown duration.
	}

	kind := guessKind(bpm, dur)

	return Sample{
		Path:           path,
		Name:           name,
		Role:           role,
		RoleConfidence: conf,
		Kind:           kind,
		BPM:            bpm,
		Key:            key,
		DurationSec:    dur,
		SizeBytes:      fi.Size(),
	}, nil
}

// ---- Search / lookups -------------------------------------------------------

// Search returns samples whose name, role, or key contains query (case-insensitive),
// sorted by path. An empty query returns all samples.
func (ix *Index) Search(query string) []Sample {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		out := make([]Sample, len(ix.Samples))
		copy(out, ix.Samples)
		return out
	}
	var out []Sample
	for _, s := range ix.Samples {
		if strings.Contains(strings.ToLower(s.Name), q) ||
			strings.Contains(strings.ToLower(s.Role), q) ||
			(s.Key != "" && strings.Contains(strings.ToLower(s.Key), q)) {
			out = append(out, s)
		}
	}
	return out
}

// ByRole returns every sample with the given role, sorted by path.
func (ix *Index) ByRole(role string) []Sample {
	want := strings.ToLower(strings.TrimSpace(role))
	var out []Sample
	for _, s := range ix.Samples {
		if s.Role == want {
			out = append(out, s)
		}
	}
	return out
}

// Count returns the number of samples with the given role.
func (ix *Index) Count(role string) int { return ix.RoleCounts[strings.ToLower(role)] }
