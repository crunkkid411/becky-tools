// phash.go — stdlib-only 64-bit average perceptual hash (aHash) helpers, shared
// by becky-events (location-change detection) and becky-osint (frame provenance).
//
// aHash recipe: downscale a frame to 8x8 grayscale, take the mean of the 64
// gray values, then set bit i to 1 when pixel i >= mean. Two hashes are compared
// by Hamming distance (popcount of the XOR); a large distance means the frames
// look different (e.g. a location/background change). No third-party deps.
package osintexport

import (
	"fmt"
	"image"
	"math/bits"
)

// AHashSize is the side length of the downscaled grid; 8x8 = 64 bits.
const AHashSize = 8

// AHashFromGray64 computes a 64-bit aHash from exactly 64 grayscale byte values
// (row-major 8x8). It returns an error if the slice is not 64 bytes long.
func AHashFromGray64(gray []byte) (uint64, error) {
	if len(gray) != AHashSize*AHashSize {
		return 0, fmt.Errorf("aHash needs %d gray bytes, got %d", AHashSize*AHashSize, len(gray))
	}
	var sum int
	for _, v := range gray {
		sum += int(v)
	}
	mean := sum / len(gray)
	var hash uint64
	for i, v := range gray {
		if int(v) >= mean {
			hash |= 1 << uint(i)
		}
	}
	return hash, nil
}

// AHashFromImage downscales an arbitrary image to 8x8 grayscale (nearest-neighbor,
// stdlib only) and computes its aHash. Used when a frame is decoded in-process
// rather than reduced by ffmpeg.
func AHashFromImage(img image.Image) uint64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	gray := make([]byte, AHashSize*AHashSize)
	if w == 0 || h == 0 {
		v, _ := AHashFromGray64(gray)
		return v
	}
	for gy := 0; gy < AHashSize; gy++ {
		for gx := 0; gx < AHashSize; gx++ {
			// Sample the center of each cell to avoid edge bias.
			sx := b.Min.X + (gx*w+w/2)/AHashSize
			sy := b.Min.Y + (gy*h+h/2)/AHashSize
			r, g, bl, _ := img.At(sx, sy).RGBA()
			// Rec. 601 luma; RGBA() returns 16-bit values, shift to 8-bit.
			luma := (299*int(r>>8) + 587*int(g>>8) + 114*int(bl>>8)) / 1000
			gray[gy*AHashSize+gx] = byte(luma)
		}
	}
	v, _ := AHashFromGray64(gray)
	return v
}

// HammingDistance counts the differing bits between two 64-bit hashes.
func HammingDistance(a, b uint64) int {
	return bits.OnesCount64(a ^ b)
}

// HashHex renders a 64-bit hash as a 16-char hex string for the provenance sidecar.
func HashHex(h uint64) string {
	return fmt.Sprintf("%016x", h)
}
