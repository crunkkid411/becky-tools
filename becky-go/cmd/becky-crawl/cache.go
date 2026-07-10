// cache.go — the crawl cache: keyed by repo slug + doc-corpus hash, stored
// alongside becky's other machine-level state (~/.becky/), never inside the
// scanned repo itself (read-only crawler, AUTOPILOT.md Law 16 — it never writes
// to the target repo).
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// cacheDir is where crawl results are cached, mirroring internal/config.Path()'s
// ~/.becky/ home for machine-level state.
func cacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".becky", "crawl-cache")
	}
	return filepath.Join(home, ".becky", "crawl-cache")
}

// cacheFilePath names the cache file for one (repo, corpus hash) pair. The hash
// is truncated to 16 hex chars for a short filename; the full hash still lives in
// the cached JSON's corpus_hash field for exactness.
func cacheFilePath(slug, hash string) string {
	h := hash
	if len(h) > 16 {
		h = h[:16]
	}
	return filepath.Join(cacheDir(), slug+"-"+h+".json")
}

func readCache(path string) (Output, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Output{}, false
	}
	var o Output
	if err := json.Unmarshal(data, &o); err != nil {
		return Output{}, false
	}
	return o, true
}

func writeCache(path string, o Output) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
