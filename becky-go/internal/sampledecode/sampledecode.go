// Package sampledecode is a pure-Go, dependency-free RIFF/WAVE decoder for the
// becky drum machine's sample loader (SPEC-BECKY-DRUM.md §2). It exists because the
// archived go-audio/wav silently mis-decodes 32-bit IEEE float via an int cast
// (go-audio/audio issue #18); this decoder uses math.Float32frombits on the float
// path so that bug cannot recur. There is a deliberate float-correctness test that
// asserts exact decoded sample values.
//
// Scope: stdlib only (encoding/binary, math, io, os). No cgo, no build tags, no new
// go.mod deps. Degrade-never-crash: truncated/odd/unsupported inputs return a wrapped
// error and never panic; unknown chunks are skipped.
//
// Byte-layout references in comments:
//   - WAVE/RIFF canonical chunk layout: McGill WAVE spec
//     (http://www-mmsp.ece.mcgill.ca/Documents/AudioFormats/WAVE/WAVE.html).
//   - acid chunk byte layout: KVR / libsndfile (the OneShot bit, root note, beats,
//     meter and float BPM offsets used below).
//   - smpl chunk byte layout: RecordingBlogs "Sample chunk (of a Wave file)".
package sampledecode

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// Format names the resolved sample encoding of the data chunk.
type Format int

const (
	// FormatUnknown is the zero value (no data decoded).
	FormatUnknown Format = iota
	// FormatPCM is integer linear PCM (WAVE tag 1, or EXTENSIBLE wrapping it).
	FormatPCM
	// FormatFloat is IEEE 754 floating point (WAVE tag 3, or EXTENSIBLE wrapping it).
	FormatFloat
)

// String renders the format for ProbeWAV / diagnostics.
func (f Format) String() string {
	switch f {
	case FormatPCM:
		return "pcm"
	case FormatFloat:
		return "float"
	default:
		return "unknown"
	}
}

// WAVE format tags. EXTENSIBLE (0xFFFE) stores the real tag in the first 2 bytes of
// its 16-byte SubFormat GUID.
const (
	wfPCM        = 0x0001
	wfFloat      = 0x0003
	wfExtensible = 0xFFFE
)

// Loop is one smpl-chunk sustain loop.
type Loop struct {
	// Start and End are sample-frame indices into the data (loop start/end points).
	Start int
	End   int
	// Type is the loop type: 0 = forward, 1 = alternating (ping-pong), 2 = backward.
	Type int
}

// SmplChunk is the parsed "smpl" chunk: the root/unity note plus any loop points.
// (RecordingBlogs smpl layout: MIDIUnityNote at offset 12, NumSampleLoops at 28,
// then NumSampleLoops 24-byte loop records starting at offset 36.)
type SmplChunk struct {
	// UnityNote is the MIDI note number the sample plays at unpitched (root note).
	UnityNote int
	Loops     []Loop
}

// AcidChunk is the parsed "acid" chunk (loop/tempo metadata written by Acidized
// loops; read by libsndfile). KVR/libsndfile byte layout, from the chunk body start:
//
//	offset 8  uint32 type bitmask  (bit 0 / 0x01 = OneShot, i.e. not a tempo loop)
//	offset 12 int16  root note     (MIDI)
//	offset 20 int32  number of beats
//	offset 24 int16  meter denominator
//	offset 26 int16  meter numerator
//	offset 28 float32 tempo (BPM)
type AcidChunk struct {
	OneShot    bool
	RootNote   int
	Beats      int
	MeterNum   int
	MeterDenom int
	TempoBPM   float32
}

// CuePoint is one entry of the "cue " chunk (a marker position in sample frames).
type CuePoint struct {
	// ID is the cue point's dwIdentifier.
	ID int
	// Position is the SampleOffset (frame index into the play order / data).
	Position int
}

// Audio is a fully decoded WAV file. Samples is interleaved (frame 0 ch 0, frame 0
// ch 1, frame 1 ch 0, ...) and normalized to float32 in [-1, 1] for every integer
// and float source format. Metadata chunk fields are nil when the chunk is absent.
type Audio struct {
	SampleRate int
	Channels   int
	Frames     int
	Samples    []float32
	Bits       int
	Format     Format

	Smpl *SmplChunk
	Acid *AcidChunk
	Cues []CuePoint
}

// DurationSec is the audio length in seconds (0 if no/invalid rate).
func (a *Audio) DurationSec() float64 {
	if a == nil || a.SampleRate <= 0 {
		return 0
	}
	return float64(a.Frames) / float64(a.SampleRate)
}

// fmtChunk holds the fields read from a parsed "fmt " chunk.
type fmtChunk struct {
	tag        uint16 // resolved real tag (EXTENSIBLE already unwrapped)
	channels   uint16
	sampleRate uint32
	bits       uint16
}

// DecodeWAVFile decodes the WAV file at path. It reads the whole file into memory
// (drum samples are short) and delegates to DecodeWAV.
func DecodeWAVFile(path string) (*Audio, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("sampledecode: open %q: %w", path, err)
	}
	defer f.Close()
	a, err := DecodeWAV(f)
	if err != nil {
		return nil, fmt.Errorf("sampledecode: %q: %w", path, err)
	}
	return a, nil
}

// DecodeWAV reads an entire WAV stream and returns the decoded Audio. Any
// malformed/truncated/unsupported input yields a wrapped error (never a panic).
func DecodeWAV(r io.Reader) (*Audio, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("sampledecode: read: %w", err)
	}
	return decodeBytes(b)
}

// decodeBytes is the core decoder over an in-memory RIFF/WAVE buffer.
func decodeBytes(b []byte) (*Audio, error) {
	// RIFF header: "RIFF" <uint32 size> "WAVE".
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, fmt.Errorf("sampledecode: not a RIFF/WAVE file")
	}

	var (
		ft        fmtChunk
		haveFmt   bool
		data      []byte
		haveData  bool
		smplBytes []byte
		acidBytes []byte
		cueBytes  []byte
	)

	// Walk the chunk list after the 12-byte RIFF/WAVE header. Each chunk is an
	// 8-byte header (4-byte id, 4-byte LE size) followed by size bytes, padded to an
	// even (word-aligned) length. Unknown chunks are skipped.
	for off := 12; off+8 <= len(b); {
		id := string(b[off : off+4])
		size := int(binary.LittleEndian.Uint32(b[off+4 : off+8]))
		start := off + 8
		if size < 0 || start+size > len(b) {
			return nil, fmt.Errorf("sampledecode: chunk %q overruns file (size %d, %d remaining)", id, size, len(b)-start)
		}
		body := b[start : start+size]
		switch id {
		case "fmt ":
			parsed, err := parseFmt(body)
			if err != nil {
				return nil, err
			}
			ft, haveFmt = parsed, true
		case "data":
			data, haveData = body, true
		case "smpl":
			smplBytes = body
		case "acid":
			acidBytes = body
		case "cue ":
			cueBytes = body
		}
		off = start + size
		if size%2 == 1 {
			off++ // pad byte
		}
	}

	if !haveFmt {
		return nil, fmt.Errorf("sampledecode: missing fmt chunk")
	}
	if !haveData {
		return nil, fmt.Errorf("sampledecode: missing data chunk")
	}

	samples, frames, err := decodeSamples(data, ft)
	if err != nil {
		return nil, err
	}

	a := &Audio{
		SampleRate: int(ft.sampleRate),
		Channels:   int(ft.channels),
		Frames:     frames,
		Samples:    samples,
		Bits:       int(ft.bits),
		Format:     formatFromTag(ft.tag),
	}
	if smplBytes != nil {
		a.Smpl = parseSmpl(smplBytes)
	}
	if acidBytes != nil {
		a.Acid = parseAcid(acidBytes)
	}
	if cueBytes != nil {
		a.Cues = parseCue(cueBytes)
	}
	return a, nil
}
