// Package tts is the deterministic Go front for becky-tts — becky's local spoken
// voice (NeuTTS Air, GGUF). The CLI parsing, file-safety, WAV writing/validation,
// the --selftest offline proof, and the degrade-never-crash path all live here and
// are fully testable WITHOUT a GPU, an audio device, or any model. The single AI
// step (the neural synthesis + NeuCodec decode) is the local-wiring boundary: when
// the runtime binary + model GGUF are absent the synth returns a typed DegradeError
// and the CLI prints the text instead — NEVER a Microsoft voice (ACCESSIBILITY.md).
package tts

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

// WAV format constants for the canonical mono PCM16 output becky-tts writes.
const (
	// formatPCM is WAVE_FORMAT_PCM (the only format becky-tts emits/accepts).
	formatPCM = 1
	// BitsPerSample is the fixed sample width (16-bit signed little-endian).
	BitsPerSample = 16
	// Channels is mono (1) — becky's voice is a single speaker.
	Channels = 1
	// DefaultSampleRate is the fixed rate the --selftest fixture is rendered at.
	// (The real NeuTTS path reads the rate from the helper; this is only the
	// deterministic offline-proof rate.)
	DefaultSampleRate = 24000
)

// WriteWAVPCM16 serialises mono 16-bit PCM samples into a complete, valid
// canonical RIFF/WAVE byte stream (header + fmt chunk + data chunk). It is
// deterministic for a given (samples, sampleRate) and is the ONLY place becky-tts
// constructs a WAV, so every output (selftest or real) shares one validated writer.
func WriteWAVPCM16(samples []int16, sampleRate int) ([]byte, error) {
	if sampleRate <= 0 {
		return nil, fmt.Errorf("invalid sample rate %d", sampleRate)
	}
	dataLen := len(samples) * 2 // 2 bytes per 16-bit sample
	byteRate := sampleRate * Channels * (BitsPerSample / 8)
	blockAlign := Channels * (BitsPerSample / 8)
	// RIFF chunk size = 4 ("WAVE") + (8 + 16) fmt + (8 + dataLen) data.
	riffSize := 4 + (8 + 16) + (8 + dataLen)

	buf := make([]byte, 0, 44+dataLen)
	buf = append(buf, "RIFF"...)
	buf = le32(buf, uint32(riffSize))
	buf = append(buf, "WAVE"...)

	// fmt subchunk (16 bytes, PCM).
	buf = append(buf, "fmt "...)
	buf = le32(buf, 16)
	buf = le16(buf, formatPCM)
	buf = le16(buf, Channels)
	buf = le32(buf, uint32(sampleRate))
	buf = le32(buf, uint32(byteRate))
	buf = le16(buf, uint16(blockAlign))
	buf = le16(buf, BitsPerSample)

	// data subchunk.
	buf = append(buf, "data"...)
	buf = le32(buf, uint32(dataLen))
	for _, s := range samples {
		buf = le16(buf, uint16(s))
	}
	return buf, nil
}

func le16(b []byte, v uint16) []byte {
	var tmp [2]byte
	binary.LittleEndian.PutUint16(tmp[:], v)
	return append(b, tmp[:]...)
}

func le32(b []byte, v uint32) []byte {
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], v)
	return append(b, tmp[:]...)
}

// WAVInfo is the parsed, validated shape of a canonical PCM WAV — enough for tests
// and the CLI to confirm becky produced a REAL audio file (not a stub/text blob).
type WAVInfo struct {
	SampleRate    int
	Channels      int
	BitsPerSample int
	AudioFormat   int
	DataBytes     int // length of the data chunk payload (>0 means real audio)
}

// ValidateWAV parses a byte stream as a canonical RIFF/WAVE PCM file and returns
// its parameters, erroring on anything that is not a structurally-valid PCM WAV
// with a non-empty data chunk. This is the guard that proves an output (whether
// from --selftest or the real helper) is genuinely audio before becky claims it.
func ValidateWAV(b []byte) (WAVInfo, error) {
	var info WAVInfo
	if len(b) < 12 {
		return info, errors.New("not a WAV: shorter than RIFF header")
	}
	if string(b[0:4]) != "RIFF" {
		return info, fmt.Errorf("not a WAV: missing RIFF tag (got %q)", safeTag(b[0:4]))
	}
	if string(b[8:12]) != "WAVE" {
		return info, fmt.Errorf("not a WAV: missing WAVE tag (got %q)", safeTag(b[8:12]))
	}

	var (
		sawFmt  bool
		sawData bool
	)
	pos := 12
	for pos+8 <= len(b) {
		id := string(b[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(b[pos+4 : pos+8]))
		body := pos + 8
		if size < 0 || body+size > len(b) {
			// Tolerate a data chunk whose declared size overruns the buffer only
			// by clamping for inspection; but a fmt overrun is fatal.
			if id == "data" {
				size = len(b) - body
			} else {
				return info, fmt.Errorf("chunk %q declares size %d past end of file", safeTag(b[pos:pos+4]), size)
			}
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return info, fmt.Errorf("fmt chunk too small (%d bytes)", size)
			}
			info.AudioFormat = int(binary.LittleEndian.Uint16(b[body : body+2]))
			info.Channels = int(binary.LittleEndian.Uint16(b[body+2 : body+4]))
			info.SampleRate = int(binary.LittleEndian.Uint32(b[body+4 : body+8]))
			info.BitsPerSample = int(binary.LittleEndian.Uint16(b[body+14 : body+16]))
			sawFmt = true
		case "data":
			info.DataBytes = size
			sawData = true
		}
		// Chunks are word-aligned; advance past the (padded) body.
		adv := size
		if adv%2 == 1 {
			adv++
		}
		pos = body + adv
	}

	if !sawFmt {
		return info, errors.New("not a valid WAV: no fmt chunk")
	}
	if !sawData {
		return info, errors.New("not a valid WAV: no data chunk")
	}
	if info.AudioFormat != formatPCM {
		return info, fmt.Errorf("not PCM audio (format code %d)", info.AudioFormat)
	}
	if info.DataBytes <= 0 {
		return info, errors.New("WAV has an empty data chunk (no audio)")
	}
	return info, nil
}

func safeTag(b []byte) string {
	out := make([]byte, len(b))
	for i, c := range b {
		if c >= 0x20 && c < 0x7f {
			out[i] = c
		} else {
			out[i] = '.'
		}
	}
	return string(out)
}

// ReadAll is a tiny helper so the CLI can validate a file via an io.Reader without
// importing io everywhere. It reads the whole stream then validates it.
func ReadAll(r io.Reader) ([]byte, error) { return io.ReadAll(r) }

// seededTone deterministically renders a short, gentle sine tone as PCM16 samples.
// It is the FIXED fixture behind --selftest: same seed → byte-identical samples,
// no model, no randomness from the clock. This proves the text→WAV plumbing end to
// end (writer + validator) without any neural step.
func seededTone(seed int64, sampleRate int, durationMs int) []int16 {
	if sampleRate <= 0 {
		sampleRate = DefaultSampleRate
	}
	if durationMs <= 0 {
		durationMs = 600
	}
	n := sampleRate * durationMs / 1000
	if n <= 0 {
		n = 1
	}
	// Map the seed to a stable pitch in a pleasant range (A3..A5), deterministically.
	base := 220.0 + float64(((seed%12)+12)%12)*36.0 // 220..616 Hz
	amp := 0.25 * math.MaxInt16                     // -12 dBFS-ish, never clipping
	samples := make([]int16, n)
	twoPiFOverSR := 2 * math.Pi * base / float64(sampleRate)
	// Short linear fade in/out to avoid clicks (still fully deterministic).
	fade := sampleRate / 100 // ~10ms
	if fade < 1 {
		fade = 1
	}
	for i := 0; i < n; i++ {
		v := amp * math.Sin(twoPiFOverSR*float64(i))
		switch {
		case i < fade:
			v *= float64(i) / float64(fade)
		case i >= n-fade:
			v *= float64(n-1-i) / float64(fade)
		}
		samples[i] = int16(math.Round(v))
	}
	return samples
}
