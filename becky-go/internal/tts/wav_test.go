package tts

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestWriteWAVPCM16_HeaderBytes(t *testing.T) {
	samples := []int16{0, 100, -100, 32767, -32768}
	rate := 24000
	b, err := WriteWAVPCM16(samples, rate)
	if err != nil {
		t.Fatalf("WriteWAVPCM16: %v", err)
	}
	// Exact canonical 44-byte header + 5*2 data bytes.
	if got, want := len(b), 44+len(samples)*2; got != want {
		t.Fatalf("len = %d, want %d", got, want)
	}
	if string(b[0:4]) != "RIFF" {
		t.Errorf("bytes[0:4] = %q, want RIFF", b[0:4])
	}
	if string(b[8:12]) != "WAVE" {
		t.Errorf("bytes[8:12] = %q, want WAVE", b[8:12])
	}
	if string(b[12:16]) != "fmt " {
		t.Errorf("bytes[12:16] = %q, want 'fmt '", b[12:16])
	}
	// fmt chunk size must be 16 (PCM).
	if v := binary.LittleEndian.Uint32(b[16:20]); v != 16 {
		t.Errorf("fmt size = %d, want 16", v)
	}
	// audio format must be 1 (PCM).
	if v := binary.LittleEndian.Uint16(b[20:22]); v != formatPCM {
		t.Errorf("audio format = %d, want %d", v, formatPCM)
	}
	if v := binary.LittleEndian.Uint16(b[22:24]); v != Channels {
		t.Errorf("channels = %d, want %d", v, Channels)
	}
	if v := binary.LittleEndian.Uint32(b[24:28]); int(v) != rate {
		t.Errorf("sample rate = %d, want %d", v, rate)
	}
	// byte rate = rate * channels * bytes/sample
	wantByteRate := uint32(rate * Channels * (BitsPerSample / 8))
	if v := binary.LittleEndian.Uint32(b[28:32]); v != wantByteRate {
		t.Errorf("byte rate = %d, want %d", v, wantByteRate)
	}
	if v := binary.LittleEndian.Uint16(b[32:34]); int(v) != Channels*(BitsPerSample/8) {
		t.Errorf("block align = %d, want %d", v, Channels*(BitsPerSample/8))
	}
	if v := binary.LittleEndian.Uint16(b[34:36]); v != BitsPerSample {
		t.Errorf("bits/sample = %d, want %d", v, BitsPerSample)
	}
	if string(b[36:40]) != "data" {
		t.Errorf("bytes[36:40] = %q, want data", b[36:40])
	}
	if v := binary.LittleEndian.Uint32(b[40:44]); int(v) != len(samples)*2 {
		t.Errorf("data size = %d, want %d", v, len(samples)*2)
	}
	// RIFF size = total - 8
	if v := binary.LittleEndian.Uint32(b[4:8]); int(v) != len(b)-8 {
		t.Errorf("RIFF size = %d, want %d", v, len(b)-8)
	}
}

func TestWriteWAVPCM16_SamplesRoundTrip(t *testing.T) {
	samples := []int16{1, -1, 12345, -12345}
	b, err := WriteWAVPCM16(samples, 8000)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range samples {
		off := 44 + i*2
		got := int16(binary.LittleEndian.Uint16(b[off : off+2]))
		if got != s {
			t.Errorf("sample %d = %d, want %d", i, got, s)
		}
	}
}

func TestWriteWAVPCM16_BadRate(t *testing.T) {
	if _, err := WriteWAVPCM16([]int16{0}, 0); err == nil {
		t.Fatal("expected error for zero sample rate")
	}
}

func TestValidateWAV_Accepts(t *testing.T) {
	b, _ := WriteWAVPCM16([]int16{0, 1, 2, 3}, 16000)
	info, err := ValidateWAV(b)
	if err != nil {
		t.Fatalf("ValidateWAV rejected a valid WAV: %v", err)
	}
	if info.SampleRate != 16000 {
		t.Errorf("rate = %d, want 16000", info.SampleRate)
	}
	if info.Channels != 1 {
		t.Errorf("channels = %d, want 1", info.Channels)
	}
	if info.BitsPerSample != 16 {
		t.Errorf("bits = %d, want 16", info.BitsPerSample)
	}
	if info.AudioFormat != formatPCM {
		t.Errorf("format = %d, want %d", info.AudioFormat, formatPCM)
	}
	if info.DataBytes != 8 {
		t.Errorf("data bytes = %d, want 8", info.DataBytes)
	}
}

func TestValidateWAV_Rejects(t *testing.T) {
	cases := map[string][]byte{
		"empty":      {},
		"too short":  []byte("RI"),
		"no RIFF":    append([]byte("XXXX\x00\x00\x00\x00WAVE"), make([]byte, 8)...),
		"no WAVE":    append([]byte("RIFF\x00\x00\x00\x00XXXX"), make([]byte, 8)...),
		"plain text": []byte("this is not audio, it is the printed text"),
		"riff no data": func() []byte {
			// RIFF/WAVE with a fmt chunk but no data chunk.
			var buf bytes.Buffer
			buf.WriteString("RIFF")
			binary.Write(&buf, binary.LittleEndian, uint32(4+8+16))
			buf.WriteString("WAVE")
			buf.WriteString("fmt ")
			binary.Write(&buf, binary.LittleEndian, uint32(16))
			binary.Write(&buf, binary.LittleEndian, uint16(1))     // PCM
			binary.Write(&buf, binary.LittleEndian, uint16(1))     // mono
			binary.Write(&buf, binary.LittleEndian, uint32(16000)) // rate
			binary.Write(&buf, binary.LittleEndian, uint32(32000)) // byte rate
			binary.Write(&buf, binary.LittleEndian, uint16(2))     // block align
			binary.Write(&buf, binary.LittleEndian, uint16(16))    // bits
			return buf.Bytes()
		}(),
	}
	for name, b := range cases {
		if _, err := ValidateWAV(b); err == nil {
			t.Errorf("%s: expected ValidateWAV to reject, got nil", name)
		}
	}
}

func TestValidateWAV_RejectsEmptyData(t *testing.T) {
	b, _ := WriteWAVPCM16(nil, 16000) // zero samples => empty data chunk
	if _, err := ValidateWAV(b); err == nil {
		t.Fatal("expected rejection of an empty data chunk")
	}
}

func TestSeededTone_Deterministic(t *testing.T) {
	a := seededTone(42, 24000, 200)
	b := seededTone(42, 24000, 200)
	if len(a) == 0 {
		t.Fatal("seededTone produced no samples")
	}
	if !bytes.Equal(int16ToBytes(a), int16ToBytes(b)) {
		t.Fatal("seededTone is not deterministic for the same seed")
	}
	c := seededTone(7, 24000, 200)
	if bytes.Equal(int16ToBytes(a), int16ToBytes(c)) {
		t.Fatal("different seeds produced identical samples")
	}
	// Never clip.
	for i, s := range a {
		if s == 32767 || s == -32768 {
			t.Fatalf("sample %d clipped at %d", i, s)
		}
	}
}

func int16ToBytes(s []int16) []byte {
	out := make([]byte, len(s)*2)
	for i, v := range s {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(v))
	}
	return out
}
