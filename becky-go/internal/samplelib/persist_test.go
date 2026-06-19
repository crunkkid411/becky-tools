package samplelib

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// touchSample creates a minimal stub audio file with the given name.
func touchSample(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("RIFF"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// ── ScanWithCache round-trip ──────────────────────────────────────────────────

func TestScanWithCache_RoundTrip(t *testing.T) {
	libDir := t.TempDir()
	touchSample(t, libDir, "kick_01.wav")
	touchSample(t, libDir, "snare_01.wav")

	cacheFile := filepath.Join(t.TempDir(), "samplelib.json")
	opts := PersistedIndexOptions{
		IndexPath: cacheFile,
		ScanOpts:  ScanOptions{Recursive: false},
	}

	idx, err := ScanWithCache(libDir, opts)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if len(idx.Samples) != 2 {
		t.Fatalf("expected 2 samples, got %d", len(idx.Samples))
	}
	// Cache file should now exist.
	if _, err := os.Stat(cacheFile); os.IsNotExist(err) {
		t.Error("cache file not written after first scan")
	}

	// Second scan should return same result.
	idx2, err := ScanWithCache(libDir, opts)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if len(idx2.Samples) != len(idx.Samples) {
		t.Errorf("second scan: %d samples, want %d", len(idx2.Samples), len(idx.Samples))
	}
}

func TestScanWithCache_MtimeReuse(t *testing.T) {
	libDir := t.TempDir()
	p := touchSample(t, libDir, "kick_01.wav")

	cacheFile := filepath.Join(t.TempDir(), "samplelib.json")
	opts := PersistedIndexOptions{IndexPath: cacheFile}

	// Prime the cache.
	idx1, err := ScanWithCache(libDir, opts)
	if err != nil {
		t.Fatal(err)
	}
	role1 := idx1.Samples[0].Role

	// Role is stable on second scan without file change (mtime unchanged).
	idx2, err := ScanWithCache(libDir, opts)
	if err != nil {
		t.Fatal(err)
	}
	if idx2.Samples[0].Role != role1 {
		t.Errorf("role changed between scans without file change: %q -> %q", role1, idx2.Samples[0].Role)
	}

	// MODIFY the file (change content + bump mtime explicitly).
	if err := os.WriteFile(p, []byte("RIFF_NEW_CONTENT"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Force mtime change beyond filesystem resolution.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}

	// A changed file must be re-scanned: no crash, valid result.
	idx3, err := ScanWithCache(libDir, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx3.Samples) != 1 {
		t.Errorf("expected 1 sample after modification, got %d", len(idx3.Samples))
	}
}

func TestScanWithCache_CorruptCache_Degrades(t *testing.T) {
	libDir := t.TempDir()
	touchSample(t, libDir, "snare_01.wav")

	cacheFile := filepath.Join(t.TempDir(), "samplelib.json")
	// Write garbage to simulate a corrupt cache.
	if err := os.WriteFile(cacheFile, []byte("not_json{{{{"), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := PersistedIndexOptions{IndexPath: cacheFile}
	// Must not error — falls back to a fresh scan.
	idx, err := ScanWithCache(libDir, opts)
	if err != nil {
		t.Fatalf("corrupt cache must degrade gracefully: %v", err)
	}
	if len(idx.Samples) != 1 {
		t.Errorf("expected 1 sample, got %d", len(idx.Samples))
	}
}

func TestScanWithCache_MissingCacheFile_IsNotError(t *testing.T) {
	libDir := t.TempDir()
	touchSample(t, libDir, "clap_01.wav")

	// Point at a nonexistent cache file — should fall back to fresh scan silently.
	opts := PersistedIndexOptions{
		IndexPath: filepath.Join(t.TempDir(), "nonexistent", "cache.json"),
	}
	idx, err := ScanWithCache(libDir, opts)
	if err != nil {
		t.Fatalf("missing cache must not error: %v", err)
	}
	if len(idx.Samples) != 1 {
		t.Errorf("expected 1 sample, got %d", len(idx.Samples))
	}
}

func TestScanWithCache_DifferentRoot_RefreshesCacheKey(t *testing.T) {
	libA := t.TempDir()
	libB := t.TempDir()
	touchSample(t, libA, "kick_a.wav")
	touchSample(t, libB, "kick_b.wav")

	cacheFile := filepath.Join(t.TempDir(), "samplelib.json")
	opts := PersistedIndexOptions{IndexPath: cacheFile}

	// Cache libA.
	if _, err := ScanWithCache(libA, opts); err != nil {
		t.Fatal(err)
	}
	// Scan libB using the same cache file — must not return libA's entries.
	idx, err := ScanWithCache(libB, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Samples) != 1 {
		t.Fatalf("expected 1 sample for libB, got %d", len(idx.Samples))
	}
	if idx.Samples[0].Path != filepath.Join(libB, "kick_b.wav") {
		t.Errorf("sample path = %q, want kick_b.wav from libB", idx.Samples[0].Path)
	}
}

func TestDefaultIndexPath_NotEmpty(t *testing.T) {
	p := defaultIndexPath()
	if p == "" {
		t.Error("defaultIndexPath returned empty string")
	}
}
