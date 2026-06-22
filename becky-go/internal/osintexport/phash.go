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

// ROI is a fractional sub-rectangle of an image, expressed as fractions of the
// image's width and height so it is resolution-independent (portrait and
// landscape behave the same). All four fields are in [0,1]; Width and Height
// must be > 0. The default same-room band is {Top:0, Left:0, Width:1, Height:0.35}
// — the top 35% of the frame (ceiling / upper wall), away from a centered subject.
type ROI struct {
	TopFrac    float64 // top edge as a fraction of height [0,1]
	LeftFrac   float64 // left edge as a fraction of width  [0,1]
	WidthFrac  float64 // width as a fraction of width      (0,1]
	HeightFrac float64 // height as a fraction of height    (0,1]
}

// FullROI is the whole-frame region; AHashFromImageROI(img, FullROI) is
// byte-identical to AHashFromImage(img). It is the back-compat / "--roi full" path.
var FullROI = ROI{TopFrac: 0, LeftFrac: 0, WidthFrac: 1, HeightFrac: 1}

// Clamp returns a copy of the ROI with every fraction clamped into a valid
// range: edges into [0,1], sizes into (0,1], and each edge+size capped so the
// rectangle stays inside the unit square. Width/Height that are <= 0 become the
// remaining space from the edge (so a zero size never yields an empty rect).
func (r ROI) Clamp() ROI {
	clamp01 := func(v float64) float64 {
		if v < 0 {
			return 0
		}
		if v > 1 {
			return 1
		}
		return v
	}
	out := ROI{
		TopFrac:    clamp01(r.TopFrac),
		LeftFrac:   clamp01(r.LeftFrac),
		WidthFrac:  r.WidthFrac,
		HeightFrac: r.HeightFrac,
	}
	if out.WidthFrac <= 0 {
		out.WidthFrac = 1 - out.LeftFrac
	}
	if out.HeightFrac <= 0 {
		out.HeightFrac = 1 - out.TopFrac
	}
	out.WidthFrac = clamp01(out.WidthFrac)
	out.HeightFrac = clamp01(out.HeightFrac)
	if out.LeftFrac+out.WidthFrac > 1 {
		out.WidthFrac = 1 - out.LeftFrac
	}
	if out.TopFrac+out.HeightFrac > 1 {
		out.HeightFrac = 1 - out.TopFrac
	}
	// Guard against a degenerate zero size after capping (e.g. LeftFrac==1).
	if out.WidthFrac <= 0 {
		out.WidthFrac = 1.0 / float64(AHashSize)
		if out.LeftFrac+out.WidthFrac > 1 {
			out.LeftFrac = 1 - out.WidthFrac
		}
	}
	if out.HeightFrac <= 0 {
		out.HeightFrac = 1.0 / float64(AHashSize)
		if out.TopFrac+out.HeightFrac > 1 {
			out.TopFrac = 1 - out.HeightFrac
		}
	}
	return out
}

// rect resolves the fractional ROI to an integer pixel rectangle inside b.
func (r ROI) rect(b image.Rectangle) image.Rectangle {
	w, h := b.Dx(), b.Dy()
	c := r.Clamp()
	x0 := b.Min.X + int(c.LeftFrac*float64(w))
	y0 := b.Min.Y + int(c.TopFrac*float64(h))
	rw := int(c.WidthFrac * float64(w))
	rh := int(c.HeightFrac * float64(h))
	if rw < 1 {
		rw = 1
	}
	if rh < 1 {
		rh = 1
	}
	x1 := x0 + rw
	y1 := y0 + rh
	if x1 > b.Max.X {
		x1 = b.Max.X
	}
	if y1 > b.Max.Y {
		y1 = b.Max.Y
	}
	return image.Rect(x0, y0, x1, y1)
}

// AHashFromImageROI computes an 8x8 aHash restricted to a fractional region of
// the image (the ceiling/upper-wall band, by default). It reuses the exact
// center-of-cell sampling math of AHashFromImage with the bounds set to the ROI
// rectangle, so AHashFromImageROI(img, FullROI) == AHashFromImage(img) bit for
// bit. This is the core "stop hashing the subject" fix for talking-head footage.
func AHashFromImageROI(img image.Image, roi ROI) uint64 {
	b := roi.rect(img.Bounds())
	w, h := b.Dx(), b.Dy()
	gray := make([]byte, AHashSize*AHashSize)
	if w == 0 || h == 0 {
		v, _ := AHashFromGray64(gray)
		return v
	}
	for gy := 0; gy < AHashSize; gy++ {
		for gx := 0; gx < AHashSize; gx++ {
			// Sample the center of each cell to avoid edge bias (identical to
			// AHashFromImage, but over the ROI rectangle).
			sx := b.Min.X + (gx*w+w/2)/AHashSize
			sy := b.Min.Y + (gy*h+h/2)/AHashSize
			r, g, bl, _ := img.At(sx, sy).RGBA()
			luma := (299*int(r>>8) + 587*int(g>>8) + 114*int(bl>>8)) / 1000
			gray[gy*AHashSize+gx] = byte(luma)
		}
	}
	v, _ := AHashFromGray64(gray)
	return v
}

// GrayROI extracts the 8x8 grayscale cell values (row-major) sampled over a
// fractional region of the image, using the same center-of-cell math as
// AHashFromImageROI. It is exposed so a corroborating signal (e.g. the pure-Go
// decor matcher) can reuse the exact ROI sampling without recomputing geometry.
func GrayROI(img image.Image, roi ROI) []byte {
	b := roi.rect(img.Bounds())
	w, h := b.Dx(), b.Dy()
	gray := make([]byte, AHashSize*AHashSize)
	if w == 0 || h == 0 {
		return gray
	}
	for gy := 0; gy < AHashSize; gy++ {
		for gx := 0; gx < AHashSize; gx++ {
			sx := b.Min.X + (gx*w+w/2)/AHashSize
			sy := b.Min.Y + (gy*h+h/2)/AHashSize
			r, g, bl, _ := img.At(sx, sy).RGBA()
			luma := (299*int(r>>8) + 587*int(g>>8) + 114*int(bl>>8)) / 1000
			gray[gy*AHashSize+gx] = byte(luma)
		}
	}
	return gray
}

// HammingDistance counts the differing bits between two 64-bit hashes.
func HammingDistance(a, b uint64) int {
	return bits.OnesCount64(a ^ b)
}

// HashHex renders a 64-bit hash as a 16-char hex string for the provenance sidecar.
func HashHex(h uint64) string {
	return fmt.Sprintf("%016x", h)
}
