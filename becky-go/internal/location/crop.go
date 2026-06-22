// crop.go — the decor-band crop-mask math (pure function over frame dims). This
// is the cloud half of the seam: computing the rectangle to fingerprint from
// (w,h) + the chosen mask. The LOCAL half applies it to a real decoded frame.
package location

import (
	"image"
	"strconv"
	"strings"
)

// CropRect computes the pixel rectangle to fingerprint for a frame of size w×h
// under the given mask. It is a deterministic pure function: same input → exact
// same rectangle (a clustering invariant).
//
// The mask drops Top/Left/Right/Bottom fractions from each edge; when KeepTopBand
// > 0 the kept region is further restricted to the top KeepTopBand fraction of
// the FULL height (the talking-head/top presets). The result is always clamped to
// the frame bounds and is never empty for a non-degenerate frame — if a mask
// would collapse the region, CropRect falls back to the full frame so
// fingerprinting still happens (degrade-never-crash).
func CropRect(w, h int, mask CropMask) image.Rectangle {
	full := image.Rect(0, 0, w, h)
	if w <= 0 || h <= 0 {
		return full
	}

	left := clampFrac(mask.Left)
	right := clampFrac(mask.Right)
	top := clampFrac(mask.Top)
	bottom := clampFrac(mask.Bottom)

	x0 := int(float64(w) * left)
	x1 := w - int(float64(w)*right)
	y0 := int(float64(h) * top)
	y1 := h - int(float64(h)*bottom)

	// KeepTopBand restricts the kept region to the top fraction of the height.
	if mask.KeepTopBand > 0 {
		band := int(float64(h) * clampFrac(mask.KeepTopBand))
		if band > 0 && band < y1 {
			y1 = band
		}
		if y0 >= y1 {
			y0 = 0
		}
	}

	// Clamp and guard against an empty/inverted rectangle.
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > w {
		x1 = w
	}
	if y1 > h {
		y1 = h
	}
	if x0 >= x1 || y0 >= y1 {
		return full // mask collapsed the region — fall back to the full frame.
	}
	return image.Rect(x0, y0, x1, y1)
}

func clampFrac(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

// ParseCrop resolves a --crop value to a CropMask. Accepts the preset names
// (talking-head/top/full) or an explicit "T,L,R,B" of percentages (0..100) to
// DROP from each edge. A malformed explicit spec falls back to the talking-head
// preset (degrade-never-crash) and reports ok=false so the caller can note it.
func ParseCrop(spec string) (CropMask, bool) {
	s := strings.TrimSpace(spec)
	switch s {
	case "", "talking-head", "top", "full":
		return MaskPreset(s), true
	}
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return MaskPreset("talking-head"), false
	}
	vals := make([]float64, 4)
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || v < 0 || v > 100 {
			return MaskPreset("talking-head"), false
		}
		vals[i] = v / 100.0
	}
	return CropMask{
		Name:   "custom",
		Top:    vals[0],
		Left:   vals[1],
		Right:  vals[2],
		Bottom: vals[3],
	}, true
}
