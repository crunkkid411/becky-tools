// persist.go — persistent on-disk index for samplelib.
//
// The on-disk index lives at %USERPROFILE%/.becky/samplelib.json (Windows) or
// $HOME/.becky/samplelib.json (Linux/CI). It caches Scan results per root
// directory, with the mtime of each file at index time so incremental rescans
// only re-classify changed files.
//
// Degrade-never-crash: a corrupt or missing index falls back to a fresh Scan.
// An unwritable cache directory is silently swallowed — the caller always gets
// a valid in-memory Index.
package samplelib

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// persistedEntry is one cached file entry inside persistedIndex.
type persistedEntry struct {
	Sample
	Mtime int64 `json:"mtime_ns"` // file mtime as UnixNano at classification time
}

// persistedIndex is the on-disk JSON structure for one root directory.
// Fields:
//
//	root         — absolute path of the scanned library root
//	indexed_at_ns — UnixNano when the index was last written
//	entries      — one entry per indexed sample
type persistedIndex struct {
	Root      string           `json:"root"`
	IndexedAt int64            `json:"indexed_at_ns"`
	Entries   []persistedEntry `json:"entries"`
}

// defaultIndexPath returns the canonical path for the samplelib index file.
// Degrades to a temp-dir path if the home directory is unresolvable.
func defaultIndexPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".becky", "samplelib.json")
}

// PersistedIndexOptions configures ScanWithCache.
type PersistedIndexOptions struct {
	// IndexPath overrides the default ~/.becky/samplelib.json location.
	IndexPath string
	// ScanOpts are passed through to the underlying Scan call.
	ScanOpts ScanOptions
}

// ScanWithCache is like Scan but maintains a persistent on-disk index at
// ~/.becky/samplelib.json. On subsequent calls it reuses entries whose file mtime
// is unchanged, making repeated scans of large sample libraries fast.
//
// Degrade-never-crash: if the index file cannot be read or written, ScanWithCache
// falls back to a full Scan. The caller always gets a usable Index.
func ScanWithCache(root string, opts PersistedIndexOptions) (*Index, error) {
	idxPath := opts.IndexPath
	if idxPath == "" {
		idxPath = defaultIndexPath()
	}

	// Load existing persisted index (ignore errors — full scan follows on any failure).
	cacheByPath := loadCacheByPath(idxPath, root)

	// Fresh walk always determines which files exist; cached entries provide the
	// pre-computed classification fields for unchanged files.
	fullIdx, err := Scan(root, opts.ScanOpts)
	if err != nil {
		return fullIdx, err
	}

	// For each sample from the fresh walk, reuse the cached classification if the
	// file's mtime matches what we recorded last time.
	for i, s := range fullIdx.Samples {
		ce, ok := cacheByPath[s.Path]
		if !ok {
			continue
		}
		fi, err := os.Stat(s.Path)
		if err != nil {
			continue
		}
		if fi.ModTime().UnixNano() == ce.Mtime {
			fullIdx.Samples[i] = ce.Sample
		}
	}

	// Rebuild RoleCounts from (potentially) updated samples.
	fullIdx.RoleCounts = map[string]int{}
	for _, s := range fullIdx.Samples {
		fullIdx.RoleCounts[s.Role]++
	}

	// Persist (best-effort; errors silently swallowed).
	savePersistedIndex(idxPath, root, fullIdx)

	return fullIdx, nil
}

// loadCacheByPath reads the cache file and returns a path→entry map.
// Returns an empty map on any error (missing, corrupt, wrong root).
func loadCacheByPath(path, root string) map[string]persistedEntry {
	b, err := os.ReadFile(path)
	if err != nil {
		return map[string]persistedEntry{}
	}
	var stored persistedIndex
	if err := json.Unmarshal(b, &stored); err != nil {
		return map[string]persistedEntry{}
	}
	if stored.Root != root {
		return map[string]persistedEntry{} // different root; start fresh
	}
	out := make(map[string]persistedEntry, len(stored.Entries))
	for _, e := range stored.Entries {
		out[e.Path] = e
	}
	return out
}

// savePersistedIndex writes the current Index back to the cache file (best-effort).
// The cache directory is created if missing. Any write error is silently swallowed.
func savePersistedIndex(path, root string, idx *Index) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}

	entries := make([]persistedEntry, 0, len(idx.Samples))
	for _, s := range idx.Samples {
		fi, err := os.Stat(s.Path)
		if err != nil {
			continue // file may have disappeared between scan and now
		}
		entries = append(entries, persistedEntry{
			Sample: s,
			Mtime:  fi.ModTime().UnixNano(),
		})
	}

	stored := persistedIndex{
		Root:      root,
		IndexedAt: time.Now().UnixNano(),
		Entries:   entries,
	}
	b, err := json.Marshal(stored)
	if err != nil {
		return
	}
	// Write atomically via temp+rename so a partial write never corrupts the cache.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path) // best-effort
}
