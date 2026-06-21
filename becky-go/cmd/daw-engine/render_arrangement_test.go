package main

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"becky-go/internal/dawmodel"
	"becky-go/internal/music"
)

// writePCM16WAV writes a minimal mono 16-bit PCM WAV of a short decaying tone, so the
// render test has a real sample to play (no external assets).
func writePCM16WAV(t *testing.T, path string) {
	t.Helper()
	const sr = 48000
	n := sr / 8 // 125ms
	pcm := make([]byte, n*2)
	for i := 0; i < n; i++ {
		// a fixed loud-ish value with a simple decay → non-silent, decoder-friendly.
		v := int16(12000 * (1.0 - float64(i)/float64(n)))
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(v))
	}
	var b []byte
	put := func(s string) { b = append(b, s...) }
	put32 := func(v uint32) { var x [4]byte; binary.LittleEndian.PutUint32(x[:], v); b = append(b, x[:]...) }
	put16 := func(v uint16) { var x [2]byte; binary.LittleEndian.PutUint16(x[:], v); b = append(b, x[:]...) }
	put("RIFF")
	put32(uint32(36 + len(pcm)))
	put("WAVE")
	put("fmt ")
	put32(16)
	put16(1) // PCM
	put16(1) // mono
	put32(sr)
	put32(sr * 2)
	put16(2)
	put16(16)
	put("data")
	put32(uint32(len(pcm)))
	b = append(b, pcm...)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRenderArrangement_producesAudio proves the canvas drum chain
// (MachineFromArrangement + WithDefaultKitSamples → sampler render) stays wired: a
// kick-only arrangement rendered against a synthesized kit yields a non-trivial WAV.
func TestRenderArrangement_producesAudio(t *testing.T) {
	dir := t.TempDir()
	kit := filepath.Join(dir, "kit")
	if err := os.MkdirAll(kit, 0o755); err != nil {
		t.Fatal(err)
	}
	writePCM16WAV(t, filepath.Join(kit, "kick.wav"))

	// A 1-bar arrangement with four kicks on the floor.
	a := dawmodel.New()
	a.BPM = 120
	a = a.AddTrack("drums", dawmodel.KindMIDI)
	a.Tracks[0].Clips = append(a.Tracks[0].Clips, dawmodel.Clip{Name: "beat", Channel: 9, Program: -1})
	for _, s := range []int{0, 4, 8, 12} {
		a, _, _ = a.AddNote("drums", "beat", dawmodel.Note{Start: s * music.StepTicks, Dur: music.StepTicks, Pitch: 36, Vel: 110, Ch: 9})
	}
	proj := filepath.Join(dir, "beat.json")
	body, _ := json.MarshalIndent(a, "", "  ")
	if err := os.WriteFile(proj, body, 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "out.wav")
	if code := runRenderArrangement(proj, kit, out, 42); code != 0 {
		t.Fatalf("runRenderArrangement exit code %d", code)
	}
	fi, err := os.Stat(out)
	if err != nil {
		t.Fatalf("no WAV produced: %v", err)
	}
	// A 1-bar 120bpm loop @48k ≈ 2s mono float32 ≈ ~384KB; anything tiny means silence/skip.
	if fi.Size() < 50_000 {
		t.Errorf("rendered WAV is suspiciously small (%d bytes) — the chain likely produced silence", fi.Size())
	}
}

func TestRenderArrangement_degradesOnBadInput(t *testing.T) {
	if code := runRenderArrangement(filepath.Join(t.TempDir(), "nope.json"), "", "", 42); code == 0 {
		t.Error("missing project should return a non-zero code, not succeed")
	}
}
