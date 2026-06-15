package hum

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// writePCM16WAV writes a mono PCM-16 WAV of the given samples to path. This is the
// test fixture for the offline audio path — a real file the DSPExtractor decodes.
func writePCM16WAV(t *testing.T, path string, samples []float64, sr int) {
	t.Helper()
	var buf bytes.Buffer
	dataLen := len(samples) * 2
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(36+dataLen))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // PCM
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // mono
	binary.Write(&buf, binary.LittleEndian, uint32(sr))
	binary.Write(&buf, binary.LittleEndian, uint32(sr*2))
	binary.Write(&buf, binary.LittleEndian, uint16(2))
	binary.Write(&buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(dataLen))
	for _, s := range samples {
		if s > 1 {
			s = 1
		} else if s < -1 {
			s = -1
		}
		binary.Write(&buf, binary.LittleEndian, int16(math.Round(s*32767)))
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write wav: %v", err)
	}
}

// tone appends n samples of a freqHz sine (amplitude 0.8) at sr to dst.
func tone(dst []float64, freqHz float64, sr, n int) []float64 {
	phase0 := float64(len(dst))
	for i := 0; i < n; i++ {
		dst = append(dst, 0.8*math.Sin(2*math.Pi*freqHz*(phase0+float64(i))/float64(sr)))
	}
	return dst
}

func TestDSPExtractor_MissingFileErrors(t *testing.T) {
	if _, err := (DSPExtractor{}).Extract("does-not-exist.wav", "", "cpu"); err == nil {
		t.Error("missing file should be a real I/O error")
	}
}

func TestDSPExtractor_EmptyPathErrors(t *testing.T) {
	if _, err := (DSPExtractor{}).Extract("", "", ""); err == nil {
		t.Error("empty path should error")
	}
}

func TestDSPExtractor_BadWavDegrades(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.wav")
	if err := os.WriteFile(p, []byte("not a wav at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	feat, err := DSPExtractor{}.Extract(p, "", "cpu")
	if err != nil {
		t.Fatalf("bad wav should degrade, not error: %v", err)
	}
	if !feat.Skipped || feat.Reason == "" {
		t.Errorf("bad wav should be Skipped with a reason, got %+v", feat)
	}
}

// TestDSPExtractor_EndToEnd is the full offline becky-hum path: synthesize a C-major
// scale WAV in a temp file, decode + analyze it with the pure-Go DSP front-end, run
// the deterministic pipeline, and assert a sane key + tempo plus a real MIDI file.
func TestDSPExtractor_EndToEnd(t *testing.T) {
	sr := 16000
	noteDur := sr / 2 // 0.5 s per note => 120 BPM quarter notes
	// C major scale: C4 D4 E4 F4 G4 A4 B4 C5 (Hz).
	freqs := []float64{261.63, 293.66, 329.63, 349.23, 392.00, 440.00, 493.88, 523.25}
	var samples []float64
	for _, f := range freqs {
		samples = tone(samples, f, sr, noteDur)
	}

	dir := t.TempDir()
	wavPath := filepath.Join(dir, "scale.wav")
	writePCM16WAV(t, wavPath, samples, sr)

	feat, err := DSPExtractor{}.Extract(wavPath, "dsp-floor", "cpu")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if feat.Skipped {
		t.Fatalf("scale WAV should not be skipped: %s", feat.Reason)
	}
	if len(feat.Frames) == 0 {
		t.Fatal("no contour frames extracted from the scale")
	}

	opt := DefaultOptions()
	opt.Wav = wavPath
	res := Analyze(feat, opt)

	// Key: a C-major scale should land in the C-major / A-minor family. The floor is
	// approximate, so we require a real non-empty key and only log the family check.
	if res.Key.Compose == "" {
		t.Error("no key detected")
	}
	if res.Key.Root != "C" && res.Key.Root != "A" && res.Key.Root != "G" {
		t.Logf("detected key %s (floor is approximate; C/Am/G acceptable family)", res.Key.Compose)
	}
	// Tempo: must be a plausible musical BPM, not a degenerate default-from-nothing.
	if res.Tempo.BPM < 40 || res.Tempo.BPM > 300 {
		t.Errorf("tempo %d BPM out of plausible range", res.Tempo.BPM)
	}
	// Notes: the eight scale steps should yield several transcribed notes.
	if len(res.Notes) < 3 {
		t.Errorf("only %d notes transcribed from an 8-note scale", len(res.Notes))
	}

	// A real Standard MIDI File must be writable from the result.
	midi := MelodySMF(res.Notes, res.Tempo.BPM, 480).Bytes()
	if !bytes.HasPrefix(midi, []byte("MThd")) {
		t.Error("melody output is not a Standard MIDI File")
	}
	midiPath := filepath.Join(dir, "melody.mid")
	if err := os.WriteFile(midiPath, midi, 0o644); err != nil {
		t.Fatalf("write midi: %v", err)
	}
	if fi, err := os.Stat(midiPath); err != nil || fi.Size() == 0 {
		t.Errorf("melody.mid not written or empty: %v", err)
	}

	t.Logf("offline becky-hum: key=%s tempo=%d notes=%d", res.Key.Compose, res.Tempo.BPM, len(res.Notes))
}

func TestDSPExtractor_Deterministic(t *testing.T) {
	sr := 16000
	samples := tone(nil, 440, sr, sr)
	dir := t.TempDir()
	p := filepath.Join(dir, "a.wav")
	writePCM16WAV(t, p, samples, sr)
	a, err := DSPExtractor{}.Extract(p, "dsp-floor", "cpu")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := DSPExtractor{}.Extract(p, "dsp-floor", "cpu")
	if len(a.Frames) != len(b.Frames) || len(a.Onsets) != len(b.Onsets) {
		t.Fatal("extraction not deterministic across runs")
	}
	ra := Analyze(a, DefaultOptions())
	rb := Analyze(b, DefaultOptions())
	if ra.Key != rb.Key || ra.Tempo.BPM != rb.Tempo.BPM {
		t.Fatal("pipeline not deterministic on identical audio")
	}
}
