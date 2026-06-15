// Content-addressed source cache — the determinism anchor for becky-research.
//
// R3 (fetch) writes every captured page here, keyed by the sha256 of its URL, with
// the page's own content sha256 recorded inside. Once written a capture is never
// re-fetched (write-once); R4–R9 read ONLY the cache, never the live web. A tree
// hash over the whole cache dir (SnapshotSHA256) names the exact corpus a report
// was produced from, so "what did you read?" is answerable to the byte — the
// forensic provenance bar from SPEC §6.
package research

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

// Capture is the raw, frozen snapshot of one fetched source — the exact bytes R4–R9
// reason over. The JSON tags mirror SPEC §3 R3: {url, fetched_at, http_status,
// content_sha256, title, text}. FetchedAt is RFC3339 UTC (e.g. 2026-06-14T09:12:00Z).
type Capture struct {
	URL           string `json:"url"`
	FetchedAt     string `json:"fetched_at"`
	HTTPStatus    int    `json:"http_status"`
	ContentSHA256 string `json:"content_sha256"`
	Title         string `json:"title"`
	Text          string `json:"text"`
	LinkOK        bool   `json:"link_ok"`
}

// Cache is a write-once content-addressed store under a single directory.
type Cache struct {
	dir string
}

// NewCache opens (creating if needed) the cache directory. A capture's file name
// is the sha256 of its canonical URL, so identical inputs map to identical files
// across runs and machines — the reproducibility guarantee.
func NewCache(dir string) (*Cache, error) {
	if dir == "" {
		return nil, fmt.Errorf("cache: empty dir")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("cache mkdir %q: %w", dir, err)
	}
	return &Cache{dir: dir}, nil
}

// urlKey is the cache key for a URL: sha256(canonicalURL), hex. Keying on the
// CANONICAL url means two links to the same page share one cache entry.
func urlKey(rawURL string) string {
	sum := sha256.Sum256([]byte(CanonicalURL(rawURL)))
	return hex.EncodeToString(sum[:])
}

// path is the on-disk file for a URL key (<dir>/<urlKey>.json).
func (c *Cache) path(rawURL string) string {
	return filepath.Join(c.dir, urlKey(rawURL)+".json")
}

// Has reports whether a URL is already captured (so R3 never re-fetches it).
func (c *Cache) Has(rawURL string) bool {
	_, err := os.Stat(c.path(rawURL))
	return err == nil
}

// HashContent returns the sha256 (hex) of fetched page content — the value stored
// in Capture.ContentSHA256 and used for exact-dup detection. Identical content →
// identical key → reproducible runs.
func HashContent(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// Put writes a capture write-once: if the URL is already cached it is returned
// unchanged (never overwritten), so a re-run reuses the frozen snapshot. The
// stored ContentSHA256 is filled from Text if unset.
func (c *Cache) Put(cap Capture) (Capture, error) {
	if cap.ContentSHA256 == "" {
		cap.ContentSHA256 = HashContent([]byte(cap.Text))
	}
	p := c.path(cap.URL)
	if _, err := os.Stat(p); err == nil {
		return c.read(p) // already frozen — reuse, do not overwrite
	}
	b, err := json.MarshalIndent(cap, "", "  ")
	if err != nil {
		return Capture{}, fmt.Errorf("cache marshal %q: %w", cap.URL, err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		return Capture{}, fmt.Errorf("cache write %q: %w", p, err)
	}
	return cap, nil
}

// Get returns the cached capture for a URL, or ok=false if not present.
func (c *Cache) Get(rawURL string) (Capture, bool) {
	p := c.path(rawURL)
	cap, err := c.read(p)
	if err != nil {
		return Capture{}, false
	}
	return cap, true
}

func (c *Cache) read(p string) (Capture, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return Capture{}, fmt.Errorf("cache read %q: %w", p, err)
	}
	var cap Capture
	if err := json.Unmarshal(b, &cap); err != nil {
		return Capture{}, fmt.Errorf("cache parse %q: %w", p, err)
	}
	return cap, nil
}

// CacheFileName is the basename of a URL's cache file, derived from the URL key so
// a Windows-style cache dir still yields just "<key>.json" when reported in JSON.
func (c *Cache) CacheFileName(rawURL string) string {
	return urlKey(rawURL) + ".json"
}

// SnapshotSHA256 is a deterministic tree hash over the whole cache directory: the
// sha256 of the sorted "<filename>\n<filecontent-sha256>" lines for every *.json
// capture. It names the exact corpus a report was produced from, and CHANGES iff
// the captured bytes change — so a fresh live fetch is visible as a new snapshot
// hash, never a silent difference (SPEC §6). An empty cache hashes the empty string.
func (c *Cache) SnapshotSHA256() (string, error) {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return "", fmt.Errorf("snapshot read dir %q: %w", c.dir, err)
	}
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(c.dir, e.Name()))
		if err != nil {
			return "", fmt.Errorf("snapshot read %q: %w", e.Name(), err)
		}
		lines = append(lines, e.Name()+"\n"+HashContent(b))
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:]), nil
}

// Dir returns the cache directory path.
func (c *Cache) Dir() string { return c.dir }
