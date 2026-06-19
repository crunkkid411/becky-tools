package sampledecode

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// wavBuilder assembles a valid RIFF/WAVE byte stream for tests. Chunks are appended in
// order and the RIFF size is fixed up by bytes(). Bodies are word-aligned with a pad
// byte, matching the real format.
type wavBuilder struct {
	chunks []byte
}

func (w *wavBuilder) chunk(id string, body []byte) {
	if len(id) != 4 {
		panic("chunk id must be 4 bytes")
	}
	var hdr [8]byte
	copy(hdr[0:4], id)
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(body)))
	w.chunks = append(w.chunks, hdr[:]...)
	w.chunks = append(w.chunks, body...)
	if len(body)%2 == 1 {
		w.chunks = append(w.chunks, 0) // pad byte
	}
}

// fmtBody builds a "fmt " chunk body. For EXTENSIBLE, pass tag=0xFFFE and subTag = the
// real PCM/float tag.
func fmtBody(tag, channels, bits uint16, sampleRate uint32, extensible bool, subTag uint16) []byte {
	var b []byte
	put16 := func(v uint16) { var t [2]byte; binary.LittleEndian.PutUint16(t[:], v); b = append(b, t[:]...) }
	put32 := func(v uint32) { var t [4]byte; binary.LittleEndian.PutUint32(t[:], v); b = append(b, t[:]...) }
	blockAlign := channels * bits / 8
	put16(tag)
	put16(channels)
	put32(sampleRate)
	put32(sampleRate * uint32(blockAlign)) // avg bytes/sec
	put16(blockAlign)
	put16(bits)
	if extensible {
		put16(22)     // cbSize
		put16(bits)   // valid bits
		put32(0)      // channel mask
		put16(subTag) // SubFormat GUID leading code = real tag
		// remaining 14 bytes of the GUID (fixed KSDATAFORMAT suffix; value irrelevant here)
		b = append(b, []byte{0x00, 0x00, 0x00, 0x00, 0x10, 0x00, 0x80, 0x00, 0x00, 0xAA, 0x00, 0x38, 0x9B, 0x71}...)
	}
	return b
}

func (w *wavBuilder) bytes() []byte {
	out := make([]byte, 0, 12+len(w.chunks))
	out = append(out, "RIFF"...)
	var sz [4]byte
	binary.LittleEndian.PutUint32(sz[:], uint32(4+len(w.chunks))) // "WAVE" + chunks
	out = append(out, sz[:]...)
	out = append(out, "WAVE"...)
	out = append(out, w.chunks...)
	return out
}

// --- data encoders (interleaved) ---

func encPCM16(samples []int16) []byte {
	b := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(s))
	}
	return b
}

func encPCM24(samples []int32) []byte {
	b := make([]byte, len(samples)*3)
	for i, s := range samples {
		u := uint32(s)
		b[i*3+0] = byte(u)
		b[i*3+1] = byte(u >> 8)
		b[i*3+2] = byte(u >> 16)
	}
	return b
}

func encPCM32(samples []int32) []byte {
	b := make([]byte, len(samples)*4)
	for i, s := range samples {
		binary.LittleEndian.PutUint32(b[i*4:], uint32(s))
	}
	return b
}

func encFloat32(samples []float32) []byte {
	b := make([]byte, len(samples)*4)
	for i, s := range samples {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(s))
	}
	return b
}

func approx(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-6 }

// --- 8-bit UNSIGNED PCM (P2-2: lo-fi/vintage kits) ---

func TestDecodePCM8Bit(t *testing.T) {
	// 8-bit WAV is UNSIGNED: 0x80 (128) = zero, 0x00 = -1.0, 0xFF = ~+1.0
	data := []byte{128, 0, 255, 192}
	w := &wavBuilder{}
	w.chunk("fmt ", fmtBody(wfPCM, 1, 8, 44100, false, 0))
	w.chunk("data", data)
	a, err := DecodeWAV(bytes.NewReader(w.bytes()))
	if err != nil {
		t.Fatalf("DecodeWAV 8-bit: %v", err)
	}
	if a.Format != FormatPCM {
		t.Errorf("Format = %v, want pcm", a.Format)
	}
	if a.Bits != 8 || a.Channels != 1 || a.SampleRate != 44100 {
		t.Errorf("header = bits %d ch %d rate %d", a.Bits, a.Channels, a.SampleRate)
	}
	if a.Frames != 4 {
		t.Fatalf("Frames = %d, want 4", a.Frames)
	}
	// 128 -> 0.0, 0 -> -1.0, 255 -> 127/128 (+near-full), 192 -> 64/128 (+0.5)
	want := []float32{0.0, -1.0, float32(127) / 128.0, float32(64) / 128.0}
	for i, wv := range want {
		if !approx(a.Samples[i], wv) {
			t.Errorf("sample[%d] = %v, want %v", i, a.Samples[i], wv)
		}
	}
	// The key unsigned invariant: 0x80 (128) must be exactly zero.
	if a.Samples[0] != 0 {
		t.Errorf("8-bit 0x80 should decode to exactly 0.0, got %v", a.Samples[0])
	}
}

// --- integer PCM round-trip ---

func TestDecodePCMRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		bits uint16
		data []byte
		// want is the expected normalized samples.
		want []float32
	}{
		{
			name: "16-bit full-scale + zero",
			bits: 16,
			data: encPCM16([]int16{32767, -32768, 0, 16384}),
			want: []float32{32767.0 / 32768.0, -1.0, 0, 0.5},
		},
		{
			name: "24-bit full-scale + zero",
			bits: 24,
			data: encPCM24([]int32{8388607, -8388608, 0, 4194304}),
			want: []float32{8388607.0 / 8388608.0, -1.0, 0, 0.5},
		},
		{
			name: "32-bit full-scale + zero",
			bits: 32,
			data: encPCM32([]int32{2147483647, -2147483648, 0, 1073741824}),
			want: []float32{float32(2147483647.0 / 2147483648.0), -1.0, 0, 0.5},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := &wavBuilder{}
			w.chunk("fmt ", fmtBody(wfPCM, 1, c.bits, 44100, false, 0))
			w.chunk("data", c.data)
			a, err := DecodeWAV(bytes.NewReader(w.bytes()))
			if err != nil {
				t.Fatalf("DecodeWAV: %v", err)
			}
			if a.Format != FormatPCM {
				t.Errorf("Format = %v, want pcm", a.Format)
			}
			if a.Bits != int(c.bits) || a.Channels != 1 || a.SampleRate != 44100 {
				t.Errorf("header = bits %d ch %d rate %d", a.Bits, a.Channels, a.SampleRate)
			}
			if a.Frames != len(c.want) {
				t.Fatalf("Frames = %d, want %d", a.Frames, len(c.want))
			}
			for i, want := range c.want {
				if !approx(a.Samples[i], want) {
					t.Errorf("sample[%d] = %v, want %v", i, a.Samples[i], want)
				}
			}
		})
	}
}

// --- the float-correctness test: proves no int-cast bug (go-audio/wav #18) ---

func TestDecodeFloat32Exact(t *testing.T) {
	// These exact values would be destroyed by an int cast (e.g. 0.1 -> 0).
	want := []float32{0.1, -0.1, 0.123456, 0.9999999, -1.0, 1.0, 0, 0.5, -0.333333}
	w := &wavBuilder{}
	w.chunk("fmt ", fmtBody(wfFloat, 1, 32, 48000, false, 0))
	w.chunk("data", encFloat32(want))
	a, err := DecodeWAV(bytes.NewReader(w.bytes()))
	if err != nil {
		t.Fatalf("DecodeWAV: %v", err)
	}
	if a.Format != FormatFloat {
		t.Fatalf("Format = %v, want float", a.Format)
	}
	if a.Frames != len(want) {
		t.Fatalf("Frames = %d, want %d", a.Frames, len(want))
	}
	for i, w := range want {
		// EXACT equality: math.Float32frombits must reproduce the written bits bit-for-bit.
		if a.Samples[i] != w {
			t.Errorf("float sample[%d] = %v, want %v (int-cast regression?)", i, a.Samples[i], w)
		}
	}
	// Sanity: a naive int cast of 0.1 would yield 0; assert we did NOT do that.
	if a.Samples[0] == 0 {
		t.Fatal("float 0.1 decoded to 0 — int-cast bug present")
	}
}

// --- EXTENSIBLE wrapping PCM and float ---

func TestDecodeExtensible(t *testing.T) {
	t.Run("PCM", func(t *testing.T) {
		data := encPCM16([]int16{16384, -16384})
		w := &wavBuilder{}
		w.chunk("fmt ", fmtBody(wfExtensible, 1, 16, 44100, true, wfPCM))
		w.chunk("data", data)
		a, err := DecodeWAV(bytes.NewReader(w.bytes()))
		if err != nil {
			t.Fatalf("DecodeWAV: %v", err)
		}
		if a.Format != FormatPCM {
			t.Fatalf("Format = %v, want pcm", a.Format)
		}
		if !approx(a.Samples[0], 0.5) || !approx(a.Samples[1], -0.5) {
			t.Errorf("samples = %v", a.Samples)
		}
	})
	t.Run("float", func(t *testing.T) {
		data := encFloat32([]float32{0.25, -0.75})
		w := &wavBuilder{}
		w.chunk("fmt ", fmtBody(wfExtensible, 1, 32, 44100, true, wfFloat))
		w.chunk("data", data)
		a, err := DecodeWAV(bytes.NewReader(w.bytes()))
		if err != nil {
			t.Fatalf("DecodeWAV: %v", err)
		}
		if a.Format != FormatFloat {
			t.Fatalf("Format = %v, want float", a.Format)
		}
		if a.Samples[0] != 0.25 || a.Samples[1] != -0.75 {
			t.Errorf("samples = %v", a.Samples)
		}
	})
}

// --- multi-channel interleave ---

func TestDecodeMultiChannelInterleave(t *testing.T) {
	// Stereo: L=0.5,R=-0.5 then L=1.0(full),R=0.
	data := encPCM16([]int16{16384, -16384, 32767, 0})
	w := &wavBuilder{}
	w.chunk("fmt ", fmtBody(wfPCM, 2, 16, 44100, false, 0))
	w.chunk("data", data)
	a, err := DecodeWAV(bytes.NewReader(w.bytes()))
	if err != nil {
		t.Fatalf("DecodeWAV: %v", err)
	}
	if a.Channels != 2 || a.Frames != 2 {
		t.Fatalf("channels %d frames %d", a.Channels, a.Frames)
	}
	if len(a.Samples) != 4 {
		t.Fatalf("len(Samples) = %d, want 4", len(a.Samples))
	}
	if !approx(a.Samples[0], 0.5) || !approx(a.Samples[1], -0.5) ||
		!approx(a.Samples[2], 32767.0/32768.0) || !approx(a.Samples[3], 0) {
		t.Errorf("interleaved samples = %v", a.Samples)
	}
}

// --- smpl loop-point parse ---

func smplBody(unityNote uint32, loops [][3]uint32) []byte {
	b := make([]byte, 36)
	binary.LittleEndian.PutUint32(b[12:16], unityNote)
	binary.LittleEndian.PutUint32(b[28:32], uint32(len(loops)))
	for _, lp := range loops {
		var rec [24]byte
		binary.LittleEndian.PutUint32(rec[4:8], lp[2])   // type
		binary.LittleEndian.PutUint32(rec[8:12], lp[0])  // start
		binary.LittleEndian.PutUint32(rec[12:16], lp[1]) // end
		b = append(b, rec[:]...)
	}
	return b
}

func TestDecodeSmpl(t *testing.T) {
	data := encPCM16([]int16{0, 0, 0, 0})
	w := &wavBuilder{}
	w.chunk("fmt ", fmtBody(wfPCM, 1, 16, 44100, false, 0))
	w.chunk("smpl", smplBody(62, [][3]uint32{{100, 200, 0}, {300, 400, 1}}))
	w.chunk("data", data)
	a, err := DecodeWAV(bytes.NewReader(w.bytes()))
	if err != nil {
		t.Fatalf("DecodeWAV: %v", err)
	}
	if a.Smpl == nil {
		t.Fatal("Smpl is nil")
	}
	if a.Smpl.UnityNote != 62 {
		t.Errorf("UnityNote = %d, want 62", a.Smpl.UnityNote)
	}
	if len(a.Smpl.Loops) != 2 {
		t.Fatalf("loops = %d, want 2", len(a.Smpl.Loops))
	}
	if a.Smpl.Loops[0] != (Loop{Start: 100, End: 200, Type: 0}) {
		t.Errorf("loop0 = %+v", a.Smpl.Loops[0])
	}
	if a.Smpl.Loops[1] != (Loop{Start: 300, End: 400, Type: 1}) {
		t.Errorf("loop1 = %+v", a.Smpl.Loops[1])
	}
}

// --- acid chunk parse (tempo + oneshot + beats) ---

func acidBody(oneShot bool, root int16, beats int32, meterDenom, meterNum int16, tempo float32) []byte {
	b := make([]byte, 32)
	var typ uint32
	if oneShot {
		typ |= 0x01
	}
	binary.LittleEndian.PutUint32(b[8:12], typ)
	binary.LittleEndian.PutUint16(b[12:14], uint16(root))
	binary.LittleEndian.PutUint32(b[20:24], uint32(beats))
	binary.LittleEndian.PutUint16(b[24:26], uint16(meterDenom))
	binary.LittleEndian.PutUint16(b[26:28], uint16(meterNum))
	binary.LittleEndian.PutUint32(b[28:32], math.Float32bits(tempo))
	return b
}

func TestDecodeAcid(t *testing.T) {
	data := encPCM16([]int16{0, 0})
	w := &wavBuilder{}
	w.chunk("fmt ", fmtBody(wfPCM, 1, 16, 44100, false, 0))
	w.chunk("acid", acidBody(true, 60, 8, 4, 4, 174.0))
	w.chunk("data", data)
	a, err := DecodeWAV(bytes.NewReader(w.bytes()))
	if err != nil {
		t.Fatalf("DecodeWAV: %v", err)
	}
	if a.Acid == nil {
		t.Fatal("Acid is nil")
	}
	if !a.Acid.OneShot {
		t.Error("OneShot = false, want true")
	}
	if a.Acid.RootNote != 60 || a.Acid.Beats != 8 || a.Acid.MeterNum != 4 || a.Acid.MeterDenom != 4 {
		t.Errorf("acid = %+v", a.Acid)
	}
	if !approx(a.Acid.TempoBPM, 174.0) {
		t.Errorf("TempoBPM = %v, want 174", a.Acid.TempoBPM)
	}
}

// --- cue point parse ---

func TestDecodeCue(t *testing.T) {
	data := encPCM16([]int16{0, 0})
	// cue: 1 point, id=7, sampleOffset(at +20)=1234.
	body := make([]byte, 4+24)
	binary.LittleEndian.PutUint32(body[0:4], 1)
	binary.LittleEndian.PutUint32(body[4:8], 7)          // id
	binary.LittleEndian.PutUint32(body[4+20:4+24], 1234) // sample offset
	w := &wavBuilder{}
	w.chunk("fmt ", fmtBody(wfPCM, 1, 16, 44100, false, 0))
	w.chunk("cue ", body)
	w.chunk("data", data)
	a, err := DecodeWAV(bytes.NewReader(w.bytes()))
	if err != nil {
		t.Fatalf("DecodeWAV: %v", err)
	}
	if len(a.Cues) != 1 || a.Cues[0].ID != 7 || a.Cues[0].Position != 1234 {
		t.Fatalf("cues = %+v", a.Cues)
	}
}

// --- unknown chunk skip ---

func TestDecodeUnknownChunkSkip(t *testing.T) {
	data := encPCM16([]int16{16384})
	w := &wavBuilder{}
	w.chunk("fmt ", fmtBody(wfPCM, 1, 16, 44100, false, 0))
	w.chunk("JUNK", []byte{1, 2, 3, 4, 5}) // odd-length unknown chunk (tests padding)
	w.chunk("LIST", []byte("INFOIART....."))
	w.chunk("data", data)
	a, err := DecodeWAV(bytes.NewReader(w.bytes()))
	if err != nil {
		t.Fatalf("DecodeWAV: %v", err)
	}
	if a.Frames != 1 || !approx(a.Samples[0], 0.5) {
		t.Errorf("frames %d samples %v", a.Frames, a.Samples)
	}
}

// --- truncated / malformed: error, no panic ---

func TestDecodeTruncatedNoPanic(t *testing.T) {
	full := func() []byte {
		w := &wavBuilder{}
		w.chunk("fmt ", fmtBody(wfPCM, 1, 16, 44100, false, 0))
		w.chunk("data", encPCM16([]int16{1, 2, 3, 4}))
		return w.bytes()
	}()

	cases := map[string][]byte{
		"empty":                      {},
		"too short for RIFF":         {'R', 'I', 'F', 'F'},
		"not WAVE":                   append([]byte("RIFF"), append(make([]byte, 4), []byte("XXXX")...)...),
		"truncated mid-chunk-header": full[:14],
		"chunk size overruns file":   full[:len(full)-4],
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on %q: %v", name, r)
				}
			}()
			if _, err := DecodeWAV(bytes.NewReader(b)); err == nil {
				t.Errorf("expected error for %q, got nil", name)
			}
		})
	}
}

func TestDecodeMissingChunks(t *testing.T) {
	t.Run("no fmt", func(t *testing.T) {
		w := &wavBuilder{}
		w.chunk("data", encPCM16([]int16{1}))
		if _, err := DecodeWAV(bytes.NewReader(w.bytes())); err == nil {
			t.Error("expected error for missing fmt")
		}
	})
	t.Run("no data", func(t *testing.T) {
		w := &wavBuilder{}
		w.chunk("fmt ", fmtBody(wfPCM, 1, 16, 44100, false, 0))
		if _, err := DecodeWAV(bytes.NewReader(w.bytes())); err == nil {
			t.Error("expected error for missing data")
		}
	})
	t.Run("unsupported tag", func(t *testing.T) {
		w := &wavBuilder{}
		w.chunk("fmt ", fmtBody(6 /* A-law */, 1, 8, 44100, false, 0))
		w.chunk("data", []byte{0, 0})
		if _, err := DecodeWAV(bytes.NewReader(w.bytes())); err == nil {
			t.Error("expected error for unsupported tag")
		}
	})
}

// --- ProbeWAV matches DecodeWAV header ---

func TestProbeWAVMatchesDecode(t *testing.T) {
	cases := []struct {
		name       string
		tag, bits  uint16
		channels   uint16
		extensible bool
		subTag     uint16
		data       []byte
	}{
		{"pcm16 mono", wfPCM, 16, 1, false, 0, encPCM16([]int16{1, 2, 3, 4})},
		{"pcm24 stereo", wfPCM, 24, 2, false, 0, encPCM24(make([]int32, 8))},
		{"float32 mono", wfFloat, 32, 1, false, 0, encFloat32([]float32{0.1, 0.2, 0.3})},
		{"ext pcm stereo", wfExtensible, 16, 2, true, wfPCM, encPCM16(make([]int16, 6))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := &wavBuilder{}
			w.chunk("fmt ", fmtBody(c.tag, c.channels, c.bits, 22050, c.extensible, c.subTag))
			w.chunk("JUNK", []byte{9, 9}) // ensure probe skips before data
			w.chunk("data", c.data)
			raw := w.bytes()

			dir := t.TempDir()
			path := filepath.Join(dir, "probe.wav")
			if err := os.WriteFile(path, raw, 0o644); err != nil {
				t.Fatal(err)
			}

			pr, pc, pf, pb, pfmt, err := ProbeWAV(path)
			if err != nil {
				t.Fatalf("ProbeWAV: %v", err)
			}
			a, err := DecodeWAVFile(path)
			if err != nil {
				t.Fatalf("DecodeWAVFile: %v", err)
			}
			if pr != a.SampleRate || pc != a.Channels || pf != a.Frames || pb != a.Bits || pfmt != a.Format.String() {
				t.Errorf("probe(%d,%d,%d,%d,%s) != decode(%d,%d,%d,%d,%s)",
					pr, pc, pf, pb, pfmt, a.SampleRate, a.Channels, a.Frames, a.Bits, a.Format)
			}
		})
	}
}

func TestProbeWAVErrors(t *testing.T) {
	if _, _, _, _, _, err := ProbeWAV(filepath.Join(t.TempDir(), "nope.wav")); err == nil {
		t.Error("expected error for missing file")
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.wav")
	if err := os.WriteFile(bad, []byte("not a wav"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, _, err := ProbeWAV(bad); err == nil {
		t.Error("expected error for non-WAV file")
	}
}

func TestDurationSec(t *testing.T) {
	a := &Audio{SampleRate: 1000, Frames: 500}
	if got := a.DurationSec(); got != 0.5 {
		t.Errorf("DurationSec = %v, want 0.5", got)
	}
	if (&Audio{}).DurationSec() != 0 {
		t.Error("zero-rate DurationSec should be 0")
	}
}
