package main

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// series turns "first frame + values" into a motion map.
func series(first int, v ...float64) map[int]float64 {
	m := map[int]float64{}
	for i, x := range v {
		m[first+i] = x
	}
	return m
}

// Motion measured on Jordan's real claps (2026-10-12). Contact frames were
// checked by eye, frame by frame.
func TestContactFrameOnJordansClaps(t *testing.T) {
	cases := []struct {
		name  string
		m     map[int]float64
		spike int
		want  int
	}{
		// goodbye-youtube-update: 164 is the blurred swing, 165 hands together
		{"goodbye-youtube-update", series(140, 6.35, 6.16, 5.82, 5.68, 5.64, 5.56, 5.59, 5.75, 5.96, 6.71, 7.81, 8.77,
			9.13, 9.29, 7.13, 7.07, 7.28, 7.25, 7.24, 7.80, 8.56, 9.53, 11.33, 15.58, 22.26, 18.15, 5.52, 5.58, 4.76,
			4.00, 4.44, 4.75, 5.69, 6.71, 7.34, 7.05, 7.63, 8.49, 8.26, 8.16, 7.84, 5.48, 3.67, 2.67, 2.59, 2.48, 2.58,
			2.76), 161, 165},
		// kid 05-02-41: OBS repeats frames (~0 motion), and a hand-on-face fidget
		// before the clap (64-65) must not win over the clap swing (85-87)
		{"kid repeated frames + fidget", series(53, 24.95, 11.10, 9.29, 8.78, 8.66, 8.96, 9.51, 0.10, 17.01, 13.64,
			0.05, 27.10, 30.83, 0.05, 13.41, 17.90, 18.84, 0.13, 0.12, 10.85, 8.57, 7.12, 18.46, 0.14, 9.43, 0.05, 8.94,
			8.44, 10.97, 0.02, 13.55, 0.10, 30.15, 25.41, 16.01, 7.42, 0.31, 10.39, 4.42, 3.57, 3.76, 3.27, 3.22, 3.93,
			3.52, 3.33, 3.13, 0.12), 74, 87},
		{"kid 07-52-14", series(99, 15.22, 13.87, 14.23, 13.17, 11.80, 11.51, 12.33, 12.42, 12.11, 10.84, 9.28, 7.13,
			5.74, 6.27, 6.13, 5.61, 4.23, 3.00, 2.22, 2.78, 4.36, 5.30, 5.87, 6.28, 6.24, 6.73, 7.80, 8.46, 9.24, 9.60,
			10.05, 11.31, 14.20, 21.27, 30.18, 14.69, 8.41, 8.70, 5.46, 4.66, 3.97, 5.49, 6.73, 8.03, 8.94, 10.55, 11.28,
			12.26), 120, 134},
		// police report: picture-in-picture layout, repeated frames
		{"police report", series(516, 10.24, 8.38, 9.07, 12.10, 17.78, 0.03, 27.52, 0.21, 15.39, 22.54, 11.93, 0.04,
			11.58, 15.66, 0.05, 9.35, 14.16, 0.03, 11.08, 14.43, 35.01, 16.86, 6.32, 0.04, 6.21, 7.73, 3.33, 0.07, 4.72,
			1.84, 1.28, 1.32, 1.58, 2.24, 2.62, 2.71, 2.96, 2.84, 2.35, 2.36, 2.40, 2.74, 3.66, 4.99, 6.14, 8.61, 10.85,
			13.59), 537, 537},
	}
	for _, c := range cases {
		got, ok, why := contactFrame(c.m, c.spike)
		if !ok || got != c.want {
			t.Errorf("%s: got frame %d ok=%v (%s), want %d", c.name, got, ok, why, c.want)
		}
	}
}

// A keyboard click with no hand swing in the picture is not a clap.
func TestContactFrameRefusesNoSwing(t *testing.T) {
	// police report 10-15-42: its loudest spike is a keyboard click (40.7 s)
	m := series(1201, 1.96, 1.82, 1.41, 1.27, 2.00, 2.50, 2.20, 2.29, 2.16, 1.29, 1.21, 1.66, 1.99, 1.72, 2.14, 1.76,
		0.93, 1.48, 1.28, 1.49, 1.55, 1.31, 1.33, 0.99, 0.85, 0.81, 0.96, 0.71, 0.93, 1.40, 1.83, 3.48, 3.34, 3.42,
		2.57, 3.58, 3.23, 2.81, 3.34, 3.35, 3.16, 2.34, 2.54, 3.75, 3.88, 2.99, 3.79, 3.63)
	if f, ok, _ := contactFrame(m, 1222); ok {
		t.Fatalf("found a clap at frame %d in footage with no hand swing", f)
	}
}

func TestProjectName(t *testing.T) {
	for in, want := range map[string]string{
		"12_goodbye-youtube-update": "goodbye-youtube-update",
		"8_goodbye_youtube":         "goodbye_youtube",
		"no-day-prefix":             "no-day-prefix",
	} {
		if got := projectName(in); got != want {
			t.Errorf("projectName(%q) = %q, want %q", in, got, want)
		}
	}
}

// findSpikes takes a sharp clap out of quiet over a louder but gradual sound,
// and reports its onset to the sample.
func TestFindSpikesOnset(t *testing.T) {
	x := make([]float32, sampleRate*4)
	for i := range x { // a loud slow swell at 1 s (speech-like, no sharp attack)
		tt := float64(i)/sampleRate - 1.0
		x[i] = float32(0.6 * math.Exp(-tt*tt*20) * math.Sin(float64(i)*0.05))
	}
	on := 3*sampleRate + 123 // the clap
	for i := 0; i < 2000; i++ {
		x[on+i] += float32(0.9 * math.Exp(-float64(i)/300) * math.Cos(float64(i)*0.9))
	}
	sp := findSpikes(x)
	if len(sp) == 0 {
		t.Fatal("no spike found")
	}
	if got := sp[0].Onset; math.Abs(got-float64(on)/sampleRate) > 2.0/sampleRate {
		t.Fatalf("onset %.6f s, want %.6f s", got, float64(on)/sampleRate)
	}
}

func TestSpikeInWav(t *testing.T) {
	const rate, at = 48000, 30000
	pcm := make([]byte, 2*rate)
	for i := 0; i < 600; i++ {
		v := int16(30000 * math.Exp(-float64(i)/150) * math.Cos(float64(i)*0.7))
		binary.LittleEndian.PutUint16(pcm[(at+i)*2:], uint16(v))
	}
	var b []byte
	le := func(n uint32, size int) {
		tmp := make([]byte, 4)
		binary.LittleEndian.PutUint32(tmp, n)
		b = append(b, tmp[:size]...)
	}
	b = append(b, "RIFF"...)
	le(uint32(36+len(pcm)), 4)
	b = append(b, "WAVEfmt "...)
	le(16, 4)
	le(1, 2)
	le(1, 2)
	le(rate, 4)
	le(rate*2, 4)
	le(2, 2)
	le(16, 2)
	b = append(b, "data"...)
	le(uint32(len(pcm)), 4)
	b = append(b, pcm...)
	path := filepath.Join(t.TempDir(), "p.wav")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := spikeInWav(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := float64(at) / rate; math.Abs(got-want) > 1.0/rate {
		t.Fatalf("spike at %.6f s, want %.6f s", got, want)
	}
}
