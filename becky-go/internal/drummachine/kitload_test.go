package drummachine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// touchWAV creates a minimal stub file so kitimport doesn't mark samples
// as missing. 4 bytes is enough for fileExists() to return true.
func touchWAV(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("RIFF"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func writeSFZ(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// ── LoadKitFromSFZ ────────────────────────────────────────────────────────────

func TestLoadKitFromSFZ_Simple(t *testing.T) {
	dir := t.TempDir()
	touchWAV(t, dir, "kick.wav")
	touchWAV(t, dir, "snare.wav")
	sfz := `
<region> sample=kick.wav  key=36 loop_mode=one_shot
<region> sample=snare.wav key=38 loop_mode=one_shot
`
	path := writeSFZ(t, dir, "test.sfz", sfz)
	res, err := LoadKitFromSFZ(path)
	if err != nil {
		t.Fatalf("LoadKitFromSFZ: %v", err)
	}
	if len(res.Kit.Pads) != PadCount {
		t.Fatalf("expected %d pads, got %d", PadCount, len(res.Kit.Pads))
	}
	// pad 0 should be wired to the first sound
	pad0 := res.Kit.Pads[0]
	if pad0.Sound == nil {
		t.Fatal("pad 0 Sound is nil")
	}
	if pad0.SamplePath == "" {
		t.Error("pad 0 SamplePath is empty")
	}
	if !pad0.Sound.OneShot {
		t.Error("pad 0 Sound.OneShot should be true (loop_mode=one_shot)")
	}
}

func TestLoadKitFromSFZ_ChokeGroupMirrored(t *testing.T) {
	dir := t.TempDir()
	touchWAV(t, dir, "hat_c.wav")
	touchWAV(t, dir, "hat_o.wav")
	sfz := `
<region> sample=hat_c.wav key=42 group=1 off_by=2 loop_mode=one_shot
<region> sample=hat_o.wav key=46 group=2 loop_mode=one_shot
`
	path := writeSFZ(t, dir, "choke.sfz", sfz)
	res, err := LoadKitFromSFZ(path)
	if err != nil {
		t.Fatalf("LoadKitFromSFZ: %v", err)
	}
	// Pad 0 → hat_c: choke group 1
	pad0 := res.Kit.Pads[0]
	if pad0.Sound == nil {
		t.Fatal("pad 0 Sound nil")
	}
	if pad0.Sound.ChokeGroup != 1 {
		t.Errorf("Sound.ChokeGroup = %d, want 1", pad0.Sound.ChokeGroup)
	}
	if pad0.ChokeGroup != 1 {
		t.Errorf("Pad.ChokeGroup = %d, want 1 (backward-compat mirror)", pad0.ChokeGroup)
	}
}

func TestLoadKitFromSFZ_MissingSampleNotFatal(t *testing.T) {
	dir := t.TempDir()
	// no WAV files on disk
	sfz := `<region> sample=ghost.wav key=36 loop_mode=one_shot`
	path := writeSFZ(t, dir, "ghost.sfz", sfz)
	res, err := LoadKitFromSFZ(path)
	// must not return a fatal error
	if err != nil {
		t.Fatalf("expected no error for missing sample, got: %v", err)
	}
	// sound must be wired with Missing=true
	if res.Kit.Pads[0].Sound == nil {
		t.Fatal("pad 0 Sound nil even for missing sample")
	}
	if !res.Kit.Pads[0].Sound.Layers[0].RoundRobin[0].Missing {
		t.Error("expected Variant.Missing=true for ghost.wav")
	}
}

func TestLoadKitFromSFZ_OverflowCapped(t *testing.T) {
	dir := t.TempDir()
	// Create 20 samples — more than PadCount (16)
	sfz := ""
	for i := 0; i < 20; i++ {
		fname := "s" + indexToName(i) + ".wav"
		if err := os.WriteFile(filepath.Join(dir, fname), []byte("RIFF"), 0o644); err != nil {
			t.Fatal(err)
		}
		sfz += "<region> sample=" + fname + " key=" + intToString(36+i) + "\n"
	}
	path := writeSFZ(t, dir, "big.sfz", sfz)
	res, err := LoadKitFromSFZ(path)
	if err != nil {
		t.Fatalf("LoadKitFromSFZ: %v", err)
	}
	// Notes must mention the overflow
	found := false
	for _, n := range res.Notes {
		if strings.Contains(n, "only first") || strings.Contains(n, "sounds in file") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected overflow note, got %v", res.Notes)
	}
}

func TestLoadKitFromSFZ_KitName(t *testing.T) {
	dir := t.TempDir()
	touchWAV(t, dir, "s.wav")
	sfz := `<region> sample=s.wav key=36`
	path := writeSFZ(t, dir, "MyDrumKit.sfz", sfz)
	res, err := LoadKitFromSFZ(path)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kit.Name != "MyDrumKit" {
		t.Errorf("Kit.Name = %q, want MyDrumKit", res.Kit.Name)
	}
}

// ── LoadKitFromFolder ─────────────────────────────────────────────────────────

func TestLoadKitFromFolder_RoleMapping(t *testing.T) {
	dir := t.TempDir()
	touchWAV(t, dir, "kick_01.wav")
	touchWAV(t, dir, "snare_01.wav")
	touchWAV(t, dir, "hat_01.wav")

	res, err := LoadKitFromFolder(dir)
	if err != nil {
		t.Fatalf("LoadKitFromFolder: %v", err)
	}
	// Kick → pad 0
	if res.Kit.Pads[0].Sound == nil {
		t.Error("pad 0 (kick) Sound nil")
	}
	// Snare → pad 1
	if res.Kit.Pads[1].Sound == nil {
		t.Error("pad 1 (snare) Sound nil")
	}
	// Hat → pad 2
	if res.Kit.Pads[2].Sound == nil {
		t.Error("pad 2 (hat) Sound nil")
	}
}

func TestLoadKitFromFolder_Deterministic(t *testing.T) {
	dir := t.TempDir()
	touchWAV(t, dir, "kick_a.wav")
	touchWAV(t, dir, "kick_b.wav")

	res1, err1 := LoadKitFromFolder(dir)
	res2, err2 := LoadKitFromFolder(dir)
	if err1 != nil || err2 != nil {
		t.Fatalf("errors: %v / %v", err1, err2)
	}
	p1 := res1.Kit.Pads[0].SamplePath
	p2 := res2.Kit.Pads[0].SamplePath
	if p1 != p2 {
		t.Errorf("non-deterministic: first run %q, second run %q", p1, p2)
	}
}

func TestLoadKitFromFolder_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	res, _ := LoadKitFromFolder(dir)
	// Should not crash; kit should be the default size
	if len(res.Kit.Pads) != PadCount {
		t.Errorf("expected %d pads, got %d", PadCount, len(res.Kit.Pads))
	}
	found := false
	for _, n := range res.Notes {
		if strings.Contains(n, "no audio samples") || strings.Contains(n, "scanned 0") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected empty-dir note, got %v", res.Notes)
	}
}

func TestLoadKitFromFolder_OpenHat(t *testing.T) {
	dir := t.TempDir()
	touchWAV(t, dir, "hat_closed.wav")
	touchWAV(t, dir, "hat_open.wav")

	res, err := LoadKitFromFolder(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Pad 2 = closed hat, pad 3 = open hat
	if res.Kit.Pads[2].Sound == nil {
		t.Error("pad 2 (closed hat) Sound nil")
	}
	if res.Kit.Pads[3].Sound == nil {
		t.Error("pad 3 (open hat) Sound nil — second hat not assigned")
	}
}

// ── tiny helpers (avoid importing strconv to stay lightweight) ────────────────

func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := make([]byte, 0, 4)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}

// indexToName maps 0..25 to "a".."z" for generating distinct file names.
func indexToName(i int) string {
	if i < 26 {
		return string(rune('a' + i))
	}
	return intToString(i)
}
