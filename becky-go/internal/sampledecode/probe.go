package sampledecode

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// ProbeWAV reads only the header chunks of the WAV file at path and reports its
// sample rate, channel count, frame count, bit depth, and resolved format ("pcm" /
// "float"). It is for fast library scanning: it parses "fmt " (and skips other chunk
// bodies via io.Seek where possible) and computes the frame count from the "data"
// chunk's declared size WITHOUT reading the sample bytes.
//
// Degrade-never-crash: malformed/truncated/unsupported headers return an error, never
// a panic.
func ProbeWAV(path string) (sampleRate, channels, frames, bits int, format string, err error) {
	f, oerr := os.Open(path)
	if oerr != nil {
		return 0, 0, 0, 0, "", fmt.Errorf("sampledecode: probe open %q: %w", path, oerr)
	}
	defer f.Close()

	var hdr [12]byte
	if _, rerr := io.ReadFull(f, hdr[:]); rerr != nil {
		return 0, 0, 0, 0, "", fmt.Errorf("sampledecode: probe %q: read RIFF header: %w", path, rerr)
	}
	if string(hdr[0:4]) != "RIFF" || string(hdr[8:12]) != "WAVE" {
		return 0, 0, 0, 0, "", fmt.Errorf("sampledecode: probe %q: not a RIFF/WAVE file", path)
	}

	var (
		ft       fmtChunk
		haveFmt  bool
		dataSize int
		haveData bool
	)

	for !(haveFmt && haveData) {
		var ch [8]byte
		if _, rerr := io.ReadFull(f, ch[:]); rerr != nil {
			if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
				break // clean end of chunk list
			}
			return 0, 0, 0, 0, "", fmt.Errorf("sampledecode: probe %q: read chunk header: %w", path, rerr)
		}
		id := string(ch[0:4])
		size := int64(binary.LittleEndian.Uint32(ch[4:8]))
		if size < 0 {
			return 0, 0, 0, 0, "", fmt.Errorf("sampledecode: probe %q: chunk %q bad size", path, id)
		}
		padded := size + size%2 // word-aligned body length
		switch id {
		case "fmt ":
			body := make([]byte, size)
			if _, rerr := io.ReadFull(f, body); rerr != nil {
				return 0, 0, 0, 0, "", fmt.Errorf("sampledecode: probe %q: read fmt chunk: %w", path, rerr)
			}
			parsed, perr := parseFmt(body)
			if perr != nil {
				return 0, 0, 0, 0, "", fmt.Errorf("sampledecode: probe %q: %w", path, perr)
			}
			ft, haveFmt = parsed, true
			if size%2 == 1 { // skip the fmt pad byte
				if _, serr := f.Seek(1, io.SeekCurrent); serr != nil {
					return 0, 0, 0, 0, "", fmt.Errorf("sampledecode: probe %q: seek: %w", path, serr)
				}
			}
		case "data":
			// Record the size and skip the sample bytes (never read them). If fmt is
			// still pending we keep scanning after the skip.
			dataSize, haveData = int(size), true
			if !haveFmt {
				if _, serr := f.Seek(padded, io.SeekCurrent); serr != nil {
					return 0, 0, 0, 0, "", fmt.Errorf("sampledecode: probe %q: seek: %w", path, serr)
				}
			}
		default:
			// Skip this chunk body (word-aligned) without reading it.
			if _, serr := f.Seek(padded, io.SeekCurrent); serr != nil {
				return 0, 0, 0, 0, "", fmt.Errorf("sampledecode: probe %q: seek: %w", path, serr)
			}
		}
	}

	if !haveFmt {
		return 0, 0, 0, 0, "", fmt.Errorf("sampledecode: probe %q: missing fmt chunk", path)
	}
	if !haveData {
		return 0, 0, 0, 0, "", fmt.Errorf("sampledecode: probe %q: missing data chunk", path)
	}
	frameBytes := int(ft.bits) / 8 * int(ft.channels)
	if frameBytes == 0 {
		return 0, 0, 0, 0, "", fmt.Errorf("sampledecode: probe %q: zero frame size", path)
	}
	return int(ft.sampleRate),
		int(ft.channels),
		dataSize / frameBytes,
		int(ft.bits),
		formatFromTag(ft.tag).String(),
		nil
}
