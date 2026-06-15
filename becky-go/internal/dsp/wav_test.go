package dsp

import (
	"encoding/binary"
	"math"
	"testing"
)

// synthWAV builds an in-memory PCM-16 WAV from mono float samples [-1,1]. This is the
// test fixture: no file on disk, just the RIFF/WAVE byte layout DecodeWAV parses.
func synthWAV(samples []float64, sr int) []byte {
	const bits = 16
	dataLen := len(samples) * 2
	buf := make([]byte, 0, 44+dataLen)
	put := func(b ...byte) { buf = append(buf, b...) }
	putU32 := func(v uint32) { var t [4]byte; binary.LittleEndian.PutUint32(t[:], v); buf = append(buf, t[:]...) }
	putU16 := func(v uint16) { var t [2]byte; binary.LittleEndian.PutUint16(t[:], v); buf = append(buf, t[:]...) }

	put('R', 'I', 'F', 'F')
	putU32(uint32(36 + dataLen))
	put('W', 'A', 'V', 'E')
	put('f', 'm', 't', ' ')
	putU32(16)
	putU16(formatPCM)
	putU16(1) // mono
	putU32(uint32(sr))
	putU32(uint32(sr * 2)) // byte rate
	putU16(2)              // block align
	putU16(bits)
	put('d', 'a', 't', 'a')
	putU32(uint32(dataLen))
	for _, s := range samples {
		v := int16(math.Round(clampUnit(s) * 32767))
		putU16(uint16(v))
	}
	return buf
}

func clampUnit(x float64) float64 {
	if x > 1 {
		return 1
	}
	if x < -1 {
		return -1
	}
	return x
}

// sineSamples generates `n` samples of a freqHz sine at sr.
func sineSamples(freqHz float64, sr, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = 0.8 * math.Sin(2*math.Pi*freqHz*float64(i)/float64(sr))
	}
	return out
}

func TestDecodeWAV_RoundTripPCM16(t *testing.T) {
	sr := 8000
	in := sineSamples(440, sr, 800)
	audio, err := DecodeWAV(synthWAV(in, sr))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if audio.SampleRate != sr {
		t.Errorf("sample rate = %d, want %d", audio.SampleRate, sr)
	}
	if len(audio.Samples) != len(in) {
		t.Fatalf("samples = %d, want %d", len(audio.Samples), len(in))
	}
	// Round-trip tolerance: we encode with *32767 and decode with /32768, so the
	// reconstructed value differs from the original by up to ~2 quantization steps.
	const quantTol = 2.0/32767 + 1e-6
	for i := range in {
		if math.Abs(audio.Samples[i]-in[i]) > quantTol {
			t.Fatalf("sample %d = %v, want ~%v", i, audio.Samples[i], in[i])
		}
	}
	if d := audio.DurationSec(); math.Abs(d-0.1) > 1e-6 {
		t.Errorf("duration = %v, want 0.1", d)
	}
}

func TestDecodeWAV_StereoDownmix(t *testing.T) {
	// Two channels averaged: left = +0.5, right = -0.5 => mono 0.
	sr := 8000
	buf := stereoWAV([]float64{0.5, 0.5}, []float64{-0.5, -0.5}, sr)
	audio, err := DecodeWAV(buf)
	if err != nil {
		t.Fatalf("decode stereo: %v", err)
	}
	for i, s := range audio.Samples {
		if math.Abs(s) > 1.0/32767+1e-6 {
			t.Errorf("downmixed sample %d = %v, want ~0", i, s)
		}
	}
}

func TestDecodeWAV_Malformed(t *testing.T) {
	cases := []struct {
		name string
		buf  []byte
	}{
		{"too short", []byte{'R', 'I'}},
		{"not riff", append([]byte("XXXXxxxxWAVE"), make([]byte, 20)...)},
		{"truncated header", synthWAV(sineSamples(440, 8000, 100), 8000)[:30]},
		{"empty", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := DecodeWAV(c.buf); err == nil {
				t.Errorf("expected error for %s, got nil (must degrade, not crash)", c.name)
			}
		})
	}
}

// stereoWAV builds a 2-channel PCM-16 WAV from equal-length L/R sample slices.
func stereoWAV(left, right []float64, sr int) []byte {
	const bits = 16
	dataLen := len(left) * 2 * 2
	buf := make([]byte, 0, 44+dataLen)
	put := func(b ...byte) { buf = append(buf, b...) }
	putU32 := func(v uint32) { var t [4]byte; binary.LittleEndian.PutUint32(t[:], v); buf = append(buf, t[:]...) }
	putU16 := func(v uint16) { var t [2]byte; binary.LittleEndian.PutUint16(t[:], v); buf = append(buf, t[:]...) }
	putSample := func(s float64) { putU16(uint16(int16(math.Round(clampUnit(s) * 32767)))) }

	put('R', 'I', 'F', 'F')
	putU32(uint32(36 + dataLen))
	put('W', 'A', 'V', 'E')
	put('f', 'm', 't', ' ')
	putU32(16)
	putU16(formatPCM)
	putU16(2) // stereo
	putU32(uint32(sr))
	putU32(uint32(sr * 4))
	putU16(4)
	putU16(bits)
	put('d', 'a', 't', 'a')
	putU32(uint32(dataLen))
	for i := range left {
		putSample(left[i])
		putSample(right[i])
	}
	return buf
}
