package dsp

import (
	"math"
	"testing"
)

// pcOf returns the pitch class (0=C..11=B) of a MIDI number.
func pcOf(midi int) int { return ((midi % 12) + 12) % 12 }

func TestAnalyze_ChromaPureTone(t *testing.T) {
	// A 440 Hz tone is A4 (MIDI 69, pitch class 9). The dominant chroma bin must be A.
	sr := 16000
	samples := sineSamples(440, sr, sr) // 1 second
	feat := Analyze(samples, sr)
	if feat.DurationSec < 0.99 {
		t.Fatalf("duration = %v, want ~1", feat.DurationSec)
	}
	want := pcOf(69) // 9 == A
	if got := argMax12(feat.Chroma); got != want {
		t.Errorf("dominant chroma bin = %d, want %d (A)", got, want)
	}
}

func TestAnalyze_ChromaMiddleC(t *testing.T) {
	// 261.63 Hz is middle C (MIDI 60, pitch class 0).
	sr := 16000
	samples := sineSamples(261.63, sr, sr)
	feat := Analyze(samples, sr)
	if got := argMax12(feat.Chroma); got != 0 {
		t.Errorf("dominant chroma bin = %d, want 0 (C)", got)
	}
}

func TestAnalyze_TooShortDegrades(t *testing.T) {
	feat := Analyze([]float64{0, 0, 0}, 16000)
	if len(feat.OnsetEnv) != 0 || feat.DurationSec != 0 {
		t.Errorf("too-short audio should yield zeroed features, got %+v", feat)
	}
}

func TestPitchContour_RecoversTone(t *testing.T) {
	// The dominant-peak contour of a 220 Hz tone should sit near MIDI 57 (A3).
	sr := 16000
	frames := PitchContour(sineSamples(220, sr, sr), sr)
	if len(frames) == 0 {
		t.Fatal("no contour frames")
	}
	voiced := 0
	for _, f := range frames {
		if f.F0 <= 0 {
			continue
		}
		voiced++
		midi := 69 + 12*math.Log2(f.F0/440.0)
		if math.Abs(midi-57) > 1 { // within a semitone of A3
			t.Errorf("frame f0 %.1f Hz -> MIDI %.2f, want ~57", f.F0, midi)
		}
	}
	if voiced == 0 {
		t.Error("contour found no voiced frames for a steady tone")
	}
}

func TestOnsetTimes_RegularSpacing(t *testing.T) {
	// Build an onset envelope with peaks every `period` frames; OnsetTimes should
	// recover that spacing (the substrate becky-hum's tempo stage autocorrelates).
	fps := 31.25 // 16000/512, the real frame rate
	period := 16 // frames between beats => 0.512 s => ~117 BPM
	env := make([]float64, 200)
	for i := period; i < len(env); i += period {
		env[i] = 1.0
	}
	onsets := OnsetTimes(env, fps)
	if len(onsets) < 5 {
		t.Fatalf("expected several onsets, got %d", len(onsets))
	}
	wantGap := float64(period) / fps
	for i := 1; i < len(onsets); i++ {
		if gap := onsets[i] - onsets[i-1]; math.Abs(gap-wantGap) > 1e-6 {
			t.Errorf("onset gap = %v, want %v", gap, wantGap)
		}
	}
}

func TestAnalyze_Deterministic(t *testing.T) {
	sr := 16000
	s := sineSamples(330, sr, sr/2)
	a := Analyze(s, sr)
	b := Analyze(s, sr)
	if a.Chroma != b.Chroma || len(a.OnsetEnv) != len(b.OnsetEnv) {
		t.Fatal("Analyze not deterministic")
	}
}

// argMax12 returns the index of the largest of 12 chroma bins.
func argMax12(c [12]float64) int {
	best, bi := -1.0, 0
	for i, v := range c {
		if v > best {
			best, bi = v, i
		}
	}
	return bi
}
