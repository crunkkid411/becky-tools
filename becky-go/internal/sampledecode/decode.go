package sampledecode

import (
	"encoding/binary"
	"fmt"
	"math"
)

// parseFmt reads the "fmt " chunk and resolves WAVE_FORMAT_EXTENSIBLE to its real
// underlying tag. The canonical fmt body is at least 16 bytes:
//
//	0  uint16 wFormatTag
//	2  uint16 nChannels
//	4  uint32 nSamplesPerSec
//	8  uint32 nAvgBytesPerSec
//	12 uint16 nBlockAlign
//	14 uint16 wBitsPerSample
//
// EXTENSIBLE adds (from offset 16): cbSize, wValidBitsPerSample, dwChannelMask, then
// a 16-byte SubFormat GUID whose leading 2 bytes are the real format tag (offset 24).
func parseFmt(c []byte) (fmtChunk, error) {
	if len(c) < 16 {
		return fmtChunk{}, fmt.Errorf("sampledecode: fmt chunk too short (%d bytes)", len(c))
	}
	f := fmtChunk{
		tag:        binary.LittleEndian.Uint16(c[0:2]),
		channels:   binary.LittleEndian.Uint16(c[2:4]),
		sampleRate: binary.LittleEndian.Uint32(c[4:8]),
		bits:       binary.LittleEndian.Uint16(c[14:16]),
	}
	if f.tag == wfExtensible {
		if len(c) < 26 {
			return fmtChunk{}, fmt.Errorf("sampledecode: EXTENSIBLE fmt chunk too short (%d bytes)", len(c))
		}
		f.tag = binary.LittleEndian.Uint16(c[24:26]) // SubFormat GUID leading code
	}
	if f.channels == 0 {
		return fmtChunk{}, fmt.Errorf("sampledecode: zero channels")
	}
	if f.sampleRate == 0 {
		return fmtChunk{}, fmt.Errorf("sampledecode: zero sample rate")
	}
	if f.tag != wfPCM && f.tag != wfFloat {
		return fmtChunk{}, fmt.Errorf("sampledecode: unsupported format tag 0x%04X", f.tag)
	}
	if f.bits == 0 || f.bits%8 != 0 {
		return fmtChunk{}, fmt.Errorf("sampledecode: unsupported bit depth %d", f.bits)
	}
	return f, nil
}

// formatFromTag maps an already-resolved fmt tag to the public Format enum.
func formatFromTag(tag uint16) Format {
	switch tag {
	case wfPCM:
		return FormatPCM
	case wfFloat:
		return FormatFloat
	default:
		return FormatUnknown
	}
}

// decodeSamples converts the raw data bytes into interleaved float32 in [-1, 1] and
// returns the frame count. Each frame is channels*bytesPerSample bytes; any trailing
// partial frame is ignored (degrade-never-crash). Supported: PCM 16/24/32-bit int and
// IEEE float 32-bit. The float path uses math.Float32frombits — NOT an int cast — so
// it cannot reproduce the go-audio/wav float bug.
func decodeSamples(data []byte, f fmtChunk) ([]float32, int, error) {
	bytesPerSmp := int(f.bits) / 8
	ch := int(f.channels)
	frameBytes := bytesPerSmp * ch
	if frameBytes == 0 {
		return nil, 0, fmt.Errorf("sampledecode: zero frame size")
	}

	// Confirm we actually support this (tag, bits) pair before allocating.
	switch {
	case f.tag == wfPCM && (f.bits == 16 || f.bits == 24 || f.bits == 32):
	case f.tag == wfFloat && f.bits == 32:
	default:
		return nil, 0, fmt.Errorf("sampledecode: unsupported format tag 0x%04X / %d-bit", f.tag, f.bits)
	}

	frames := len(data) / frameBytes
	out := make([]float32, frames*ch)
	for i := 0; i < frames*ch; i++ {
		p := data[i*bytesPerSmp:]
		switch {
		case f.tag == wfPCM && f.bits == 16:
			out[i] = float32(int16(binary.LittleEndian.Uint16(p))) / 32768.0
		case f.tag == wfPCM && f.bits == 24:
			out[i] = float32(int24(p)) / 8388608.0
		case f.tag == wfPCM && f.bits == 32:
			out[i] = float32(float64(int32(binary.LittleEndian.Uint32(p))) / 2147483648.0)
		case f.tag == wfFloat && f.bits == 32:
			out[i] = math.Float32frombits(binary.LittleEndian.Uint32(p))
		}
	}
	return out, frames, nil
}

// int24 reads a little-endian signed 24-bit integer from p[0:3], sign-extended into
// an int32 (24-bit = 3 LE bytes, two's complement).
func int24(p []byte) int32 {
	v := int32(p[0]) | int32(p[1])<<8 | int32(p[2])<<16
	if v&0x800000 != 0 {
		v |= ^0xFFFFFF // sign-extend the top byte
	}
	return v
}

// parseSmpl parses the "smpl" chunk. Layout (RecordingBlogs "Sample chunk"):
//
//	0  uint32 Manufacturer
//	4  uint32 Product
//	8  uint32 SamplePeriod
//	12 uint32 MIDIUnityNote
//	16 uint32 MIDIPitchFraction
//	20 uint32 SMPTEFormat
//	24 uint32 SMPTEOffset
//	28 uint32 NumSampleLoops
//	32 uint32 SamplerData
//	36 [NumSampleLoops] loop records, each 24 bytes:
//	     +0  uint32 CuePointID
//	     +4  uint32 Type     (0 forward, 1 alternating, 2 backward)
//	     +8  uint32 Start
//	     +12 uint32 End
//	     +16 uint32 Fraction
//	     +20 uint32 PlayCount
//
// Returns nil if the chunk is too short to hold the fixed header. Loop records beyond
// the chunk bounds are skipped (degrade-never-crash).
func parseSmpl(c []byte) *SmplChunk {
	if len(c) < 36 {
		return nil
	}
	s := &SmplChunk{
		UnityNote: int(binary.LittleEndian.Uint32(c[12:16])),
	}
	n := int(binary.LittleEndian.Uint32(c[28:32]))
	for i := 0; i < n; i++ {
		off := 36 + i*24
		if off+24 > len(c) {
			break
		}
		s.Loops = append(s.Loops, Loop{
			Type:  int(binary.LittleEndian.Uint32(c[off+4 : off+8])),
			Start: int(binary.LittleEndian.Uint32(c[off+8 : off+12])),
			End:   int(binary.LittleEndian.Uint32(c[off+12 : off+16])),
		})
	}
	return s
}

// parseAcid parses the "acid" chunk (KVR / libsndfile layout). From the body start:
//
//	8  uint32  type bitmask  (bit 0 / 0x01 = OneShot)
//	12 int16   root note
//	20 int32   number of beats
//	24 int16   meter denominator
//	26 int16   meter numerator
//	28 float32 tempo (BPM)
//
// The full acid chunk is 24 bytes through the meter fields; tempo needs >= 32. Returns
// nil if too short for the fixed fields (degrade-never-crash); tempo stays 0 if the
// chunk ends before offset 32.
func parseAcid(c []byte) *AcidChunk {
	if len(c) < 28 {
		return nil
	}
	a := &AcidChunk{
		OneShot:    binary.LittleEndian.Uint32(c[8:12])&0x01 != 0,
		RootNote:   int(int16(binary.LittleEndian.Uint16(c[12:14]))),
		Beats:      int(int32(binary.LittleEndian.Uint32(c[20:24]))),
		MeterDenom: int(int16(binary.LittleEndian.Uint16(c[24:26]))),
		MeterNum:   int(int16(binary.LittleEndian.Uint16(c[26:28]))),
	}
	if len(c) >= 32 {
		a.TempoBPM = math.Float32frombits(binary.LittleEndian.Uint32(c[28:32]))
	}
	return a
}

// parseCue parses the "cue " chunk. Layout:
//
//	0 uint32 NumCuePoints
//	4 [NumCuePoints] 24-byte records:
//	    +0  uint32 dwIdentifier
//	    +4  uint32 dwPosition
//	    +8  fourcc fccChunk
//	    +12 uint32 dwChunkStart
//	    +16 uint32 dwBlockStart
//	    +20 uint32 dwSampleOffset  (frame index into the data)
//
// Returns nil if too short. Records beyond the chunk bounds are skipped.
func parseCue(c []byte) []CuePoint {
	if len(c) < 4 {
		return nil
	}
	n := int(binary.LittleEndian.Uint32(c[0:4]))
	var cues []CuePoint
	for i := 0; i < n; i++ {
		off := 4 + i*24
		if off+24 > len(c) {
			break
		}
		cues = append(cues, CuePoint{
			ID:       int(binary.LittleEndian.Uint32(c[off : off+4])),
			Position: int(binary.LittleEndian.Uint32(c[off+20 : off+24])),
		})
	}
	return cues
}
