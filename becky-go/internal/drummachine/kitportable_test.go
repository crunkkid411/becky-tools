package drummachine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"becky-go/internal/sampler"
)

// buildPadWithSound builds a Kit with pad 0 wired to a single-variant Sound
// pointing at realPath.
func buildPadWithSound(realPath string) Kit {
	k := DefaultKit()
	v := sampler.Variant{SamplePath: realPath}
	l := sampler.Layer{VelLo: 1, VelHi: 127, RoundRobin: []sampler.Variant{v}}
	snd := sampler.Sound{Layers: []sampler.Layer{l}}
	k.Pads[0].Sound = &snd
	k.Pads[0].SamplePath = realPath
	return k
}

// ── RelativiseKitPaths ────────────────────────────────────────────────────────

func TestRelativiseKitPaths_Basic(t *testing.T) {
	dir := t.TempDir()
	wavPath := touchWAV(t, dir, "kick.wav")

	k := buildPadWithSound(wavPath)
	res, hashes := RelativiseKitPaths(k, dir)

	pad0 := res.Kit.Pads[0]
	if filepath.IsAbs(pad0.SamplePath) {
		t.Errorf("SamplePath should be relative, got %q", pad0.SamplePath)
	}
	if pad0.SamplePath != "kick.wav" {
		t.Errorf("relative path = %q, want kick.wav", pad0.SamplePath)
	}
	// Hash should be populated
	if hashes[0] == "" {
		t.Error("expected content hash for pad 0")
	}
	if len(hashes[0]) != 64 {
		t.Errorf("hash length = %d, want 64 (hex SHA-256)", len(hashes[0]))
	}
}

func TestRelativiseKitPaths_OutsideRoot_NoteEmitted(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	wavPath := touchWAV(t, rootB, "snare.wav") // in B, not A

	k := buildPadWithSound(wavPath)
	res, _ := RelativiseKitPaths(k, rootA) // try to relativise to A

	// Path cannot be made relative to A; should stay absolute with a note.
	if !filepath.IsAbs(res.Kit.Pads[0].SamplePath) {
		t.Error("outside-root path should stay absolute")
	}
	found := false
	for _, n := range res.Notes {
		if strings.Contains(n, "cannot relativise") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected cannot-relativise note, got %v", res.Notes)
	}
}

// ── RelinkKit ────────────────────────────────────────────────────────────────

func TestRelinkKit_PathResolve(t *testing.T) {
	root := t.TempDir()
	_ = touchWAV(t, root, "kick.wav")

	// Kit was saved with a relative path.
	k := DefaultKit()
	k.Pads[0].SamplePath = "kick.wav"

	res := RelinkKit(k, root, nil)
	expected := filepath.Join(root, "kick.wav")
	if res.Kit.Pads[0].SamplePath != expected {
		t.Errorf("SamplePath = %q, want %q", res.Kit.Pads[0].SamplePath, expected)
	}
	if len(res.Notes) > 0 {
		t.Errorf("unexpected notes: %v", res.Notes)
	}
}

func TestRelinkKit_HashFallback(t *testing.T) {
	oldRoot := t.TempDir()
	newRoot := t.TempDir()

	// Create the file in newRoot with some content.
	content := []byte("RIFFWAV")
	newPath := filepath.Join(newRoot, "subdir", "kick_renamed.wav")
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	// Compute hash for the content.
	h := hashFile(newPath)
	if h == "" {
		t.Fatal("hashFile returned empty")
	}

	// Kit references the old path (doesn't exist in newRoot).
	k := DefaultKit()
	k.Pads[0].SamplePath = filepath.Join(oldRoot, "kick.wav")

	padHashes := map[int]string{0: h}
	res := RelinkKit(k, newRoot, padHashes)

	if res.Kit.Pads[0].SamplePath != newPath {
		t.Errorf("hash-relinked path = %q, want %q", res.Kit.Pads[0].SamplePath, newPath)
	}
	// Should have a note about hash relink
	found := false
	for _, n := range res.Notes {
		if strings.Contains(n, "relinked by content hash") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected hash-relink note, got %v", res.Notes)
	}
}

func TestRelinkKit_CannotRelink(t *testing.T) {
	newRoot := t.TempDir()
	// File simply doesn't exist anywhere.
	k := DefaultKit()
	k.Pads[0].SamplePath = "nonexistent.wav"

	res := RelinkKit(k, newRoot, nil)
	// Path stays as-is; note emitted.
	if res.Kit.Pads[0].SamplePath != "nonexistent.wav" {
		t.Errorf("SamplePath = %q, want nonexistent.wav", res.Kit.Pads[0].SamplePath)
	}
	found := false
	for _, n := range res.Notes {
		if strings.Contains(n, "could not relink") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected could-not-relink note, got %v", res.Notes)
	}
}

func TestRelinkKit_EmptyPath_Skipped(t *testing.T) {
	newRoot := t.TempDir()
	k := DefaultKit()
	// Pads 0..15 all have empty SamplePath — nothing to relink.
	res := RelinkKit(k, newRoot, nil)
	if len(res.Notes) != 0 {
		t.Errorf("expected no notes for empty paths, got %v", res.Notes)
	}
}

// ── round-trip ────────────────────────────────────────────────────────────────

func TestRoundTripRelativiseRelink(t *testing.T) {
	root1 := t.TempDir()
	root2 := t.TempDir()

	// Create identical content in root2 (simulating "moved library").
	content := []byte("RIFFDATA")
	p1 := filepath.Join(root1, "snare.wav")
	p2 := filepath.Join(root2, "snare.wav")
	if err := os.WriteFile(p1, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p2, content, 0o644); err != nil {
		t.Fatal(err)
	}

	k := buildPadWithSound(p1)

	rel, hashes := RelativiseKitPaths(k, root1)
	if rel.Kit.Pads[0].SamplePath != "snare.wav" {
		t.Fatalf("expected relative path snare.wav, got %q", rel.Kit.Pads[0].SamplePath)
	}

	// Now relink to root2.
	relinked := RelinkKit(rel.Kit, root2, hashes)
	got := relinked.Kit.Pads[0].SamplePath
	if got != p2 {
		t.Errorf("relinked path = %q, want %q", got, p2)
	}
}
