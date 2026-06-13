// location.go — location identification via perceptual hashing (aHash).
//
// Samples the video at ~1 fps into 8x8 grayscale frames (one ffmpeg call, same as
// becky-events), aHashes each (REUSING internal/osintexport — not reimplemented),
// then for each enrolled location computes the minimum Hamming distance between
// any sampled frame and any enrolled reference (frame-hash or precomputed hash).
// A best distance <= --location-threshold names the location. Reference frame
// images are hashed once at startup via osintexport.AHashFromImage.
package main

import (
	"bufio"
	"fmt"
	"image"
	_ "image/jpeg" // register JPEG decoder for reference frames
	_ "image/png"  // register PNG decoder for reference frames
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/mediainfo"
	"becky-go/internal/osintexport"
)

// gridBytes is one downscaled keyframe = 8x8 grayscale bytes from ffmpeg.
const gridBytes = osintexport.AHashSize * osintexport.AHashSize

// identifyLocations samples the clip and matches enrolled locations by aHash.
func identifyLocations(cfg config.Config, info mediainfo.Info, input string, kb Knowledge, threshold int, dev string, verbose bool) ([]Identification, error) {
	sampled, err := sampleHashes(cfg.FFmpeg, input, dev, verbose)
	if err != nil {
		return nil, err
	}
	beckyio.Logf(verbose, "sampled %d keyframes at 1 fps for location matching", len(sampled))
	if len(sampled) == 0 {
		return nil, nil
	}

	var ids []Identification
	for _, loc := range kb.Locations {
		refHashes := referenceHashes(loc, verbose)
		if len(refHashes) == 0 {
			beckyio.Logf(verbose, "  location %q: no usable reference hashes, skipping", loc.Name)
			continue
		}
		best := minHamming(sampled, refHashes)
		if best > threshold {
			continue
		}
		ham := best
		ids = append(ids, Identification{
			Type:       "location",
			Name:       loc.Name,
			Confidence: round4(locationConfidence(best)),
			Match:      "perceptual-hash",
			Hamming:    &ham,
		})
		beckyio.Logf(verbose, "  location %q matched (hamming=%d <= %d)", loc.Name, best, threshold)
	}
	return ids, nil
}

// referenceHashes returns the aHashes for an enrolled location: precomputed ones
// from sidecar JSON plus runtime hashes of any reference frame images.
func referenceHashes(loc LocationPrint, verbose bool) []uint64 {
	hashes := append([]uint64{}, loc.Hashes...)
	for _, frame := range loc.Frames {
		h, err := hashImageFile(frame)
		if err != nil {
			beckyio.Logf(verbose, "  warning: hash %s: %v", frame, err)
			continue
		}
		hashes = append(hashes, h)
	}
	return hashes
}

// hashImageFile decodes an image file and aHashes it via the shared helper.
func hashImageFile(path string) (uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return 0, fmt.Errorf("decode %s: %w", path, err)
	}
	return osintexport.AHashFromImage(img), nil
}

// minHamming returns the smallest Hamming distance between any sampled frame and
// any enrolled reference hash.
func minHamming(sampled, refs []uint64) int {
	best := 64 // a 64-bit hash can differ by at most 64 bits
	for _, s := range sampled {
		for _, r := range refs {
			if d := osintexport.HammingDistance(s, r); d < best {
				best = d
			}
		}
	}
	return best
}

// locationConfidence maps a Hamming distance to a 0..1 score: distance 0 -> 1.0,
// distance 64 (max) -> 0.0. Linear and deterministic.
func locationConfidence(hamming int) float64 {
	c := 1.0 - float64(hamming)/64.0
	if c < 0 {
		return 0
	}
	return c
}

// sampleHashes runs ffmpeg once to produce a 1 fps, 8x8 grayscale raw stream and
// computes an aHash per 64-byte frame. CUDA decode is best-effort; on failure we
// retry on the CPU so sampling still runs (same pattern as becky-events).
func sampleHashes(ffmpeg, input, dev string, verbose bool) ([]uint64, error) {
	tryCUDA := strings.EqualFold(dev, "cuda")
	hashes, err := runSample(ffmpeg, input, tryCUDA)
	if err != nil && tryCUDA {
		beckyio.Logf(verbose, "cuda decode failed (%v); retrying on cpu", err)
		hashes, err = runSample(ffmpeg, input, false)
	}
	return hashes, err
}

func runSample(ffmpeg, input string, hwaccel bool) ([]uint64, error) {
	args := []string{"-y"}
	if hwaccel {
		args = append(args, "-hwaccel", "cuda")
	}
	args = append(args,
		"-i", input,
		"-vf", fmt.Sprintf("fps=1,scale=%d:%d,format=gray", osintexport.AHashSize, osintexport.AHashSize),
		"-f", "rawvideo", "-pix_fmt", "gray",
		"-loglevel", "error", "-")

	cmd := exec.Command(ffmpeg, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	hashes, readErr := readGrayFrames(stdout)
	waitErr := cmd.Wait()
	if waitErr != nil {
		return nil, fmt.Errorf("ffmpeg sample: %v: %s", waitErr, tail(errBuf.String()))
	}
	if readErr != nil {
		return nil, readErr
	}
	return hashes, nil
}

// readGrayFrames consumes the raw gray stream 64 bytes at a time, hashing each
// frame. A trailing partial frame (shorter than 64 bytes) is ignored.
func readGrayFrames(r io.Reader) ([]uint64, error) {
	br := bufio.NewReader(r)
	buf := make([]byte, gridBytes)
	var hashes []uint64
	for {
		_, err := io.ReadFull(br, buf)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read gray frame: %w", err)
		}
		h, herr := osintexport.AHashFromGray64(buf)
		if herr != nil {
			return nil, herr
		}
		hashes = append(hashes, h)
	}
	return hashes, nil
}

// parseHash accepts a 16-char hex digest (osintexport.HashHex form) or a decimal
// uint64 string and returns the parsed hash. Empty / unparseable -> (0, false).
func parseHash(s string) (uint64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	s = strings.TrimPrefix(strings.ToLower(s), "0x")
	// A 16-char hex digest is the canonical osintexport.HashHex form.
	if len(s) == 16 {
		if v, err := strconv.ParseUint(s, 16, 64); err == nil {
			return v, true
		}
	}
	// Otherwise try decimal (a plain uint64 string).
	if v, err := strconv.ParseUint(s, 10, 64); err == nil {
		return v, true
	}
	// Last resort: any-length hex.
	if v, err := strconv.ParseUint(s, 16, 64); err == nil {
		return v, true
	}
	return 0, false
}
