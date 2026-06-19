package samplelib

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// writeWAV writes a minimal valid 16-bit PCM mono WAV of the given duration (in
// seconds) at sampleRate to path, creating parent dirs. The data is zero-filled
// (we only ever read the header).
func writeWAV(t *testing.T, path string, durSec float64, sampleRate int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const bits = 16
	const ch = 1
	byteRate := sampleRate * ch * bits / 8
	dataLen := int(durSec * float64(byteRate))
	if dataLen < 0 {
		dataLen = 0
	}

	var b []byte
	put4 := func(s string) { b = append(b, s...) }
	put32 := func(v uint32) {
		var x [4]byte
		binary.LittleEndian.PutUint32(x[:], v)
		b = append(b, x[:]...)
	}
	put16 := func(v uint16) {
		var x [2]byte
		binary.LittleEndian.PutUint16(x[:], v)
		b = append(b, x[:]...)
	}

	put4("RIFF")
	put32(uint32(4 + 8 + 16 + 8 + dataLen)) // chunk size
	put4("WAVE")
	put4("fmt ")
	put32(16)
	put16(1) // PCM
	put16(ch)
	put32(uint32(sampleRate))
	put32(uint32(byteRate))
	put16(ch * bits / 8) // block align
	put16(bits)
	put4("data")
	put32(uint32(dataLen))
	b = append(b, make([]byte, dataLen)...)

	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write wav: %v", err)
	}
}

func writeRaw(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write raw: %v", err)
	}
}

func TestGuessRole(t *testing.T) {
	tests := []struct {
		name, folder string
		wantRole     string
		wantConf     string
	}{
		{"kick_01.wav", "Kicks", RoleKick, ConfHigh},          // both agree
		{"BD_punchy.wav", "808 Kicks", RoleKick, ConfHigh},    // kik/bd + kick folder
		{"snare_hit.wav", "Drums", RoleSnare, ConfLow},        // filename only
		{"loop_120.wav", "Hats", RoleHat, ConfLow},            // folder only
		{"weird-thing.wav", "Misc", RoleUnknown, ConfUnknown}, // neither
		{"clap.wav", "Claps", RoleClap, ConfHigh},
		{"808_sub.wav", "Bass", RoleBass, ConfHigh},
		{"snare.wav", "Kicks", RoleSnare, ConfLow}, // disagreement -> filename, low
		{"vox_chop.wav", "Vocals", RoleVocal, ConfHigh},
		{"bird.wav", "Animals", RoleUnknown, ConfUnknown}, // "bd" must NOT match "bird"
	}
	for _, tc := range tests {
		role, conf := guessRole(tc.name, tc.folder)
		if role != tc.wantRole || conf != tc.wantConf {
			t.Errorf("guessRole(%q,%q) = (%s,%s); want (%s,%s)",
				tc.name, tc.folder, role, conf, tc.wantRole, tc.wantConf)
		}
	}
}

func TestBPMFromName(t *testing.T) {
	tests := []struct {
		name string
		want float64
	}{
		{"groove_90bpm.wav", 90},
		{"loop 128 bpm.wav", 128},
		{"break_174BPM.wav", 174},
		{"kick_01.wav", 0},
		{"track_5bpm.wav", 0},    // too few digits
		{"weird_9000bpm.wav", 0}, // out of range (4 digits won't match \d{2,3})
		{"x_400bpm.wav", 0},      // matches regex but out of sane range
	}
	for _, tc := range tests {
		if got := bpmFromName(tc.name); got != tc.want {
			t.Errorf("bpmFromName(%q) = %v; want %v", tc.name, got, tc.want)
		}
	}
}

func TestKeyFromName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"melody_Cmin.wav", "Cmin"},
		{"lead_F#maj.wav", "F#maj"},
		{"bass_Am.wav", "Am"},
		{"pad_Gb.wav", "Gb"},
		{"kick_01.wav", ""},  // no key
		{"a_loop.wav", ""},   // bare single letter ignored
		{"the_beat.wav", ""}, // no note token
	}
	for _, tc := range tests {
		if got := keyFromName(tc.name); got != tc.want {
			t.Errorf("keyFromName(%q) = %q; want %q", tc.name, got, tc.want)
		}
	}
}

func TestGuessKind(t *testing.T) {
	tests := []struct {
		bpm, dur float64
		want     string
	}{
		{90, 4.0, KindLoop},   // bpm + multi-second
		{0, 0.3, KindOneShot}, // short, no bpm
		{0, 5.0, KindUnknown}, // long but no bpm token -> unknown
		{120, 0, KindLoop},    // bpm token, unknown duration -> loop
		{0, 0, KindUnknown},   // nothing known
	}
	for _, tc := range tests {
		if got := guessKind(tc.bpm, tc.dur); got != tc.want {
			t.Errorf("guessKind(%v,%v) = %s; want %s", tc.bpm, tc.dur, got, tc.want)
		}
	}
}

func TestWavDurationSec(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.wav")
	writeWAV(t, p, 2.0, 44100)
	d, err := wavDurationSec(p)
	if err != nil {
		t.Fatalf("wavDurationSec: %v", err)
	}
	if d < 1.99 || d > 2.01 {
		t.Errorf("duration = %v; want ~2.0", d)
	}

	// Garbage file -> error (not panic).
	bad := filepath.Join(dir, "bad.wav")
	writeRaw(t, bad, []byte("not a wav at all"))
	if _, err := wavDurationSec(bad); err == nil {
		t.Errorf("expected error for non-WAV, got nil")
	}
}

// buildTree lays out a synthetic library exercising every heuristic and returns root.
func buildTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// role agreement (high) + short one-shot
	writeWAV(t, filepath.Join(root, "Kicks", "kick_01.wav"), 0.2, 44100)
	// loop: bpm token + multi-second, role from folder only (low)
	writeWAV(t, filepath.Join(root, "Loops", "groove_90bpm.wav"), 4.0, 44100)
	// snare filename only (low)
	writeWAV(t, filepath.Join(root, "Drums", "snare_hit.wav"), 0.3, 44100)
	// key token + bass agreement
	writeWAV(t, filepath.Join(root, "Bass", "808_sub_Cmin.wav"), 0.8, 44100)
	// unknown role
	writeWAV(t, filepath.Join(root, "Misc", "weird-thing.wav"), 0.5, 44100)
	// non-audio garbage -> skipped
	writeRaw(t, filepath.Join(root, "readme.txt"), []byte("hello"))
	// empty file with audio ext -> skipped (empty)
	writeRaw(t, filepath.Join(root, "Kicks", "empty.wav"), []byte{})
	return root
}

func TestScanClassification(t *testing.T) {
	root := buildTree(t)
	idx, err := Scan(root, ScanOptions{Recursive: true})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(idx.Samples) != 5 {
		t.Fatalf("got %d samples; want 5 (%+v)", len(idx.Samples), idx.Samples)
	}

	byName := map[string]Sample{}
	for _, s := range idx.Samples {
		byName[s.Name] = s
	}

	if s := byName["kick_01.wav"]; s.Role != RoleKick || s.RoleConfidence != ConfHigh || s.Kind != KindOneShot {
		t.Errorf("kick_01: %+v", s)
	}
	if s := byName["groove_90bpm.wav"]; s.BPM != 90 || s.Kind != KindLoop {
		t.Errorf("groove loop: %+v", s)
	}
	if s := byName["snare_hit.wav"]; s.Role != RoleSnare || s.RoleConfidence != ConfLow {
		t.Errorf("snare: %+v", s)
	}
	if s := byName["808_sub_Cmin.wav"]; s.Role != RoleBass || s.Key != "Cmin" {
		t.Errorf("bass key: %+v", s)
	}
	if s := byName["weird-thing.wav"]; s.Role != RoleUnknown || s.RoleConfidence != ConfUnknown {
		t.Errorf("unknown: %+v", s)
	}

	// readme.txt + empty.wav should be skipped, with reasons.
	skipReasons := map[string]string{}
	for _, sk := range idx.Skipped {
		skipReasons[Base(sk.Path)] = sk.Reason
	}
	if _, ok := skipReasons["readme.txt"]; !ok {
		t.Errorf("readme.txt not skipped; skips=%+v", idx.Skipped)
	}
	if _, ok := skipReasons["empty.wav"]; !ok {
		t.Errorf("empty.wav not skipped; skips=%+v", idx.Skipped)
	}
}

// Base is a tiny local helper to avoid importing pathx in the test.
func Base(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}

func TestScanDeterministicOrder(t *testing.T) {
	root := buildTree(t)
	a, _ := Scan(root, ScanOptions{Recursive: true})
	b, _ := Scan(root, ScanOptions{Recursive: true})
	if len(a.Samples) != len(b.Samples) {
		t.Fatalf("len mismatch")
	}
	for i := range a.Samples {
		if a.Samples[i].Path != b.Samples[i].Path {
			t.Fatalf("order differs at %d: %q vs %q", i, a.Samples[i].Path, b.Samples[i].Path)
		}
		if i > 0 && a.Samples[i-1].Path > a.Samples[i].Path {
			t.Fatalf("not sorted by path at %d", i)
		}
	}
}

func TestScanNonRecursive(t *testing.T) {
	root := buildTree(t)
	idx, err := Scan(root, ScanOptions{Recursive: false})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	// Top level has only readme.txt (skipped) — no audio files at root.
	if len(idx.Samples) != 0 {
		t.Errorf("non-recursive got %d samples; want 0: %+v", len(idx.Samples), idx.Samples)
	}
}

func TestSearchAndByRole(t *testing.T) {
	root := buildTree(t)
	idx, _ := Scan(root, ScanOptions{Recursive: true})

	if got := idx.ByRole(RoleKick); len(got) != 1 || got[0].Name != "kick_01.wav" {
		t.Errorf("ByRole(kick) = %+v", got)
	}
	if got := idx.ByRole("KICK"); len(got) != 1 {
		t.Errorf("ByRole case-insensitive failed: %+v", got)
	}
	if got := idx.Search("snare"); len(got) != 1 || got[0].Name != "snare_hit.wav" {
		t.Errorf("Search(snare) = %+v", got)
	}
	if got := idx.Search("cmin"); len(got) != 1 || got[0].Name != "808_sub_Cmin.wav" {
		t.Errorf("Search(cmin key) = %+v", got)
	}
	if got := idx.Search(""); len(got) != len(idx.Samples) {
		t.Errorf("Search(empty) should return all")
	}
	if c := idx.Count(RoleBass); c != 1 {
		t.Errorf("Count(bass) = %d; want 1", c)
	}
	// Deterministic ordering of Search results (sorted by path via index order).
	res := idx.Search(".wav")
	for i := 1; i < len(res); i++ {
		if res[i-1].Path > res[i].Path {
			t.Errorf("Search results not sorted by path")
		}
	}
}

func TestMaxFilesCap(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 10; i++ {
		writeWAV(t, filepath.Join(root, "Kicks", "kick_"+itoa(i)+".wav"), 0.2, 44100)
	}
	idx, err := Scan(root, ScanOptions{Recursive: true, MaxFiles: 3})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(idx.Samples) != 3 {
		t.Errorf("cap not enforced: got %d samples; want 3", len(idx.Samples))
	}
	// The cap should be recorded in Skipped.
	found := false
	for _, sk := range idx.Skipped {
		if contains(sk.Reason, "max files cap") {
			found = true
		}
	}
	if !found {
		t.Errorf("cap reason not recorded in Skipped: %+v", idx.Skipped)
	}
}

func TestScanErrors(t *testing.T) {
	if _, err := Scan("", ScanOptions{}); err == nil {
		t.Errorf("empty root should error")
	}
	if _, err := Scan(filepath.Join(t.TempDir(), "does-not-exist"), ScanOptions{}); err == nil {
		t.Errorf("missing root should error")
	}
	// A file (not a dir) as root.
	f := filepath.Join(t.TempDir(), "afile.wav")
	writeWAV(t, f, 0.1, 44100)
	if _, err := Scan(f, ScanOptions{}); err == nil {
		t.Errorf("file root should error")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
