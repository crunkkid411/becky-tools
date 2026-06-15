package dsp

import (
	"encoding/binary"
	"fmt"
	"math"
)

// WAV decoding. dawbase loads WAVs through dr_wav (a public-domain C library); on
// the Go side we write a small pure-Go RIFF/WAVE decoder instead — no cgo, no deps.
// It parses the canonical chunk layout (RIFF -> "WAVE" -> "fmt " -> "data",
// skipping any chunks in between) and supports the formats becky actually meets:
// PCM 16/24/32-bit integer and 32-bit IEEE float, mono or stereo (downmixed to
// mono). Anything malformed or unsupported returns a wrapped error — never a panic
// or an out-of-range index (degrade-never-crash).

// formatPCM, formatFloat, formatExtensible are the WAVE format-tag codes we accept.
const (
	formatPCM        = 1
	formatFloat      = 3
	formatExtensible = 0xFFFE // real tag lives in the SubFormat GUID's first 2 bytes
)

// Audio is decoded mono float PCM in [-1, 1] plus its sample rate, mirroring
// dawbase's CaptureResult (samples + sample_rate + duration_sec).
type Audio struct {
	Samples    []float64
	SampleRate int
}

// DurationSec is the audio length in seconds (0 if no samples / bad rate).
func (a Audio) DurationSec() float64 {
	if a.SampleRate <= 0 {
		return 0
	}
	return float64(len(a.Samples)) / float64(a.SampleRate)
}

// wavFormat holds the parsed "fmt " chunk fields we need.
type wavFormat struct {
	tag        uint16
	channels   uint16
	sampleRate uint32
	bitsPerSmp uint16
}

// DecodeWAV parses a WAV byte buffer into mono float samples. A stereo (or
// N-channel) file is averaged down to mono. Returns a wrapped error for any
// malformed/truncated/unsupported input.
func DecodeWAV(b []byte) (Audio, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return Audio{}, fmt.Errorf("decode wav: not a RIFF/WAVE file")
	}
	fmtChunk, dataChunk, err := findChunks(b[12:])
	if err != nil {
		return Audio{}, err
	}
	f, err := parseFormat(fmtChunk)
	if err != nil {
		return Audio{}, err
	}
	samples, err := decodeSamples(dataChunk, f)
	if err != nil {
		return Audio{}, err
	}
	return Audio{Samples: samples, SampleRate: int(f.sampleRate)}, nil
}

// findChunks walks the chunk list after the WAVE id and returns the "fmt " and
// "data" chunk bodies. Each chunk is an 8-byte header (4-byte id, 4-byte LE size)
// followed by size bytes (padded to even length). Truncation => error.
func findChunks(b []byte) (fmtChunk, dataChunk []byte, err error) {
	for off := 0; off+8 <= len(b); {
		id := string(b[off : off+4])
		size := int(binary.LittleEndian.Uint32(b[off+4 : off+8]))
		start := off + 8
		if size < 0 || start+size > len(b) {
			return nil, nil, fmt.Errorf("decode wav: chunk %q overruns file (size %d)", id, size)
		}
		switch id {
		case "fmt ":
			fmtChunk = b[start : start+size]
		case "data":
			dataChunk = b[start : start+size]
		}
		off = start + size
		if size%2 == 1 {
			off++ // chunks are word-aligned
		}
	}
	if fmtChunk == nil {
		return nil, nil, fmt.Errorf("decode wav: missing fmt chunk")
	}
	if dataChunk == nil {
		return nil, nil, fmt.Errorf("decode wav: missing data chunk")
	}
	return fmtChunk, dataChunk, nil
}

// parseFormat reads the fields we need from a "fmt " chunk, resolving WAVE_FORMAT_
// EXTENSIBLE to its underlying PCM/float tag.
func parseFormat(c []byte) (wavFormat, error) {
	if len(c) < 16 {
		return wavFormat{}, fmt.Errorf("decode wav: fmt chunk too short (%d bytes)", len(c))
	}
	f := wavFormat{
		tag:        binary.LittleEndian.Uint16(c[0:2]),
		channels:   binary.LittleEndian.Uint16(c[2:4]),
		sampleRate: binary.LittleEndian.Uint32(c[4:8]),
		bitsPerSmp: binary.LittleEndian.Uint16(c[14:16]),
	}
	if f.tag == formatExtensible && len(c) >= 26 {
		f.tag = binary.LittleEndian.Uint16(c[24:26]) // SubFormat GUID's leading code
	}
	if f.channels == 0 {
		return wavFormat{}, fmt.Errorf("decode wav: zero channels")
	}
	if f.sampleRate == 0 {
		return wavFormat{}, fmt.Errorf("decode wav: zero sample rate")
	}
	return f, nil
}

// decodeSamples converts the raw data bytes into mono float samples per the format.
func decodeSamples(data []byte, f wavFormat) ([]float64, error) {
	bytesPerSmp := int(f.bitsPerSmp) / 8
	if bytesPerSmp == 0 {
		return nil, fmt.Errorf("decode wav: zero bits-per-sample")
	}
	ch := int(f.channels)
	frameBytes := bytesPerSmp * ch
	frames := len(data) / frameBytes
	out := make([]float64, frames)
	for i := 0; i < frames; i++ {
		var sum float64
		base := i * frameBytes
		for c := 0; c < ch; c++ {
			v, err := sampleAt(data[base+c*bytesPerSmp:], f)
			if err != nil {
				return nil, err
			}
			sum += v
		}
		out[i] = sum / float64(ch)
	}
	return out, nil
}

// sampleAt decodes one channel sample (starting at p[0]) to a float in [-1, 1].
func sampleAt(p []byte, f wavFormat) (float64, error) {
	switch {
	case f.tag == formatPCM && f.bitsPerSmp == 16:
		return float64(int16(binary.LittleEndian.Uint16(p))) / 32768.0, nil
	case f.tag == formatPCM && f.bitsPerSmp == 24:
		return float64(int24(p)) / 8388608.0, nil
	case f.tag == formatPCM && f.bitsPerSmp == 32:
		return float64(int32(binary.LittleEndian.Uint32(p))) / 2147483648.0, nil
	case f.tag == formatFloat && f.bitsPerSmp == 32:
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(p))), nil
	default:
		return 0, fmt.Errorf("decode wav: unsupported format tag %d / %d-bit", f.tag, f.bitsPerSmp)
	}
}

// int24 reads a little-endian signed 24-bit integer from p[0:3] (sign-extended).
func int24(p []byte) int32 {
	v := int32(p[0]) | int32(p[1])<<8 | int32(p[2])<<16
	if v&0x800000 != 0 {
		v |= ^0xFFFFFF // sign-extend the top byte
	}
	return v
}
