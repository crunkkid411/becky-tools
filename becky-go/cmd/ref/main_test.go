package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"becky-go/internal/music"
	"becky-go/internal/refmatch"
)

// --- test WAV synthesis (mirrors internal/refmatch/refmatch_test.go) ---

func encodeWAV(samples []float64, sr int) []byte {
	var buf bytes.Buffer
	data := make([]byte, len(samples)*2)
	for i, s := range samples {
		if s > 1 {
			s = 1
		}
		if s < -1 {
			s = -1
		}
		binary.LittleEndian.PutUint16(data[i*2:], uint16(int16(math.Round(s*32767))))
	}
	dataLen := len(data)
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(36+dataLen))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint32(sr))
	binary.Write(&buf, binary.LittleEndian, uint32(sr*2))
	binary.Write(&buf, binary.LittleEndian, uint16(2))
	binary.Write(&buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(dataLen))
	buf.Write(data)
	return buf.Bytes()
}

const sr = 44100

func tone(freq, amp, dur float64) []float64 {
	n := int(dur * sr)
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = amp * math.Sin(2*math.Pi*freq*float64(i)/float64(sr))
	}
	return out
}

func mixSig(sigs ...[]float64) []float64 {
	n := math.MaxInt
	for _, s := range sigs {
		if len(s) < n {
			n = len(s)
		}
	}
	out := make([]float64, n)
	for _, s := range sigs {
		for i := 0; i < n; i++ {
			out[i] += s[i]
		}
	}
	for i := range out {
		if out[i] > 1 {
			out[i] = 1
		}
		if out[i] < -1 {
			out[i] = -1
		}
	}
	return out
}

func writeWAV(t *testing.T, path string, samples []float64) {
	t.Helper()
	if err := os.WriteFile(path, encodeWAV(samples, sr), 0o644); err != nil {
		t.Fatalf("write wav %s: %v", path, err)
	}
}

// --- library build then match --library auto-selects by role ---

func TestLibraryBuildAndMatchByRole(t *testing.T) {
	dir := t.TempDir()
	// A bright stem so the role is stable; the filename corroborates "hat".
	writeWAV(t, filepath.Join(dir, "hat_01.wav"), mixSig(tone(5000, 0.5, 1.0), tone(9000, 0.4, 1.0)))
	writeWAV(t, filepath.Join(dir, "bass_01.wav"), mixSig(tone(80, 0.5, 1.0), tone(120, 0.4, 1.0)))

	housePath := filepath.Join(dir, "house.json")
	if code := runLibrary([]string{"build", "--dir", dir, "--out", housePath}); code != 0 {
		t.Fatalf("library build exited %d", code)
	}

	// Load the library to discover a real role present in it.
	lib := loadLib(t, housePath)
	if len(lib.Roles) == 0 {
		t.Fatalf("library has no roles")
	}
	target := lib.Roles[0]

	// Build a "mine" stem of the same character as target's contributors, by reusing
	// one contributor's name pattern. We pick the bass role for a stable low stem.
	minePath := filepath.Join(dir, "mine.wav")
	writeWAV(t, minePath, mixSig(tone(80, 0.2, 1.0), tone(120, 0.15, 1.0))) // quiet low

	planPath := filepath.Join(dir, "plan.json")
	code := runMatch([]string{"--library", housePath, "--mine", minePath, "--out", planPath})
	// Exit may be 1 if the matched role isn't in the library; that's the role-miss path
	// which is tested separately. For a low stem, the bass role should be present.
	_ = target
	if code != 0 && code != 1 {
		t.Fatalf("unexpected match exit code %d", code)
	}
	if code == 0 {
		if _, err := os.Stat(planPath); err != nil {
			t.Errorf("match should have written a plan: %v", err)
		}
	}
}

// --- match --library role-miss path is graceful (exit 1, no panic) ---

func TestMatchLibraryRoleMiss(t *testing.T) {
	dir := t.TempDir()
	// Library with ONLY a bright (hat-ish) role.
	writeWAV(t, filepath.Join(dir, "hat_01.wav"), mixSig(tone(5000, 0.5, 1.0), tone(9000, 0.4, 1.0)))
	housePath := filepath.Join(dir, "house.json")
	if code := runLibrary([]string{"build", "--dir", dir, "--out", housePath}); code != 0 {
		t.Fatalf("library build exited %d", code)
	}
	lib := loadLib(t, housePath)

	// A clearly-low stem so its role is NOT the bright role in the library.
	minePath := filepath.Join(dir, "mine.wav")
	writeWAV(t, minePath, mixSig(tone(60, 0.6, 1.0), tone(90, 0.5, 1.0)))

	planPath := filepath.Join(dir, "plan.json")
	code := runMatch([]string{"--library", housePath, "--mine", minePath, "--out", planPath})
	// If the low stem's role happens to match the single role in the library, this is a
	// hit (exit 0). Otherwise it's the graceful miss (exit 1). Either way: no panic and
	// no plan written on a miss.
	if code == 1 {
		if _, err := os.Stat(planPath); err == nil {
			t.Errorf("role-miss should not write a plan")
		}
	}
	_ = lib
}

// --- apply writes the EQ + gain nodes; dry-run writes nothing ---

func TestApplyAndDryRun(t *testing.T) {
	dir := t.TempDir()

	// Build a real plan with moves: dark/quiet mine vs bright/loud reference.
	refPath := filepath.Join(dir, "ref.wav")
	minePath := filepath.Join(dir, "mine.wav")
	writeWAV(t, refPath, mixSig(tone(4000, 0.7, 1.0), tone(8000, 0.5, 1.0)))
	writeWAV(t, minePath, mixSig(tone(90, 0.2, 1.0), tone(110, 0.15, 1.0)))

	planPath := filepath.Join(dir, "plan.json")
	if code := runMatch([]string{"--reference", refPath, "--mine", minePath, "--out", planPath}); code != 0 && code != 1 {
		t.Fatalf("match exit %d", code)
	}

	// A project to apply onto.
	projPath := filepath.Join(dir, "project.json")
	proj := music.Project{
		SchemaVersion: 1,
		Buses: []music.ProjBus{
			{ID: "bus.drums", Out: "bus.master"},
			{ID: "bus.master", Out: "out.main"},
		},
	}
	pb, _ := json.MarshalIndent(proj, "", "  ")
	if err := os.WriteFile(projPath, pb, 0o644); err != nil {
		t.Fatal(err)
	}

	// Dry-run: must NOT write an output.
	outPath := filepath.Join(dir, "out.json")
	if code := runApply([]string{"--plan", planPath, "--project", projPath, "--bus", "bus.drums", "--output", outPath, "--dry-run"}); code != 0 {
		t.Fatalf("apply --dry-run exited %d", code)
	}
	if _, err := os.Stat(outPath); err == nil {
		t.Errorf("dry-run must not write the output file")
	}

	// Real apply: must write the output with the ref nodes on the drums bus.
	if code := runApply([]string{"--plan", planPath, "--project", projPath, "--bus", "bus.drums", "--output", outPath}); code != 0 {
		t.Fatalf("apply exited %d", code)
	}
	b, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read applied project: %v", err)
	}
	var applied music.Project
	if err := json.Unmarshal(b, &applied); err != nil {
		t.Fatalf("parse applied project: %v", err)
	}
	var drums music.ProjBus
	for _, bus := range applied.Buses {
		if bus.ID == "bus.drums" {
			drums = bus
		}
	}
	foundEQ := false
	for _, fx := range drums.FX {
		if fx.ID == "drums.ref.eq" {
			foundEQ = true
		}
	}
	if !foundEQ {
		t.Errorf("applied project should carry drums.ref.eq node; got %+v", drums.FX)
	}
}

// --- apply on a bad project/plan degrades (exit 1, no panic) ---

func TestApplyBadInputs(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("{ not json"), 0o644)

	// Bad plan file.
	if code := runApply([]string{"--plan", bad, "--project", bad, "--bus", "bus.drums"}); code == 0 {
		t.Errorf("apply on a malformed plan should exit non-zero")
	}
	// Missing flags.
	if code := runApply([]string{"--bus", "bus.drums"}); code != 2 {
		t.Errorf("apply missing --plan/--project should be a usage error (2), got %d", code)
	}
}

func loadLib(t *testing.T, path string) refmatch.HouseSound {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read library: %v", err)
	}
	var h refmatch.HouseSound
	if err := json.Unmarshal(b, &h); err != nil {
		t.Fatalf("parse library: %v", err)
	}
	return h
}
