// roi.go — region-of-interest hashing config + the static-decor (keypoint)
// corroboration interface, the second independent signal behind the room call.
//
// The core fix for talking-head footage: hash the ceiling / upper-wall BAND
// (away from the centered subject) instead of the whole frame. The whole-frame
// hash is kept as a weak/provenance signal. A same-room CONCLUSION needs >=2
// independent signals agreeing (corroborate-then-conclude); a lone weak signal
// can NEVER reach a conclusion. See SPEC-FRAMEMATCH-HARDENING.md.
package main

import (
	"fmt"
	"image"

	"becky-go/internal/osintexport"
)

// roiConfig holds the resolved ROI mode + geometry + corroboration settings for
// one framematch run. It is built from the CLI flags and validated once.
type roiConfig struct {
	mode         string            // "band" | "corners" | "full"
	roi          osintexport.ROI   // resolved fractional rectangle (for band/full)
	corners      []osintexport.ROI // the two upper-corner ROIs (corners mode only)
	roiThreshold int               // max ROI-aHash Hamming for an "agree"
	keypoints    bool              // static-decor keypoint corroboration enabled
	minInliers   int               // keypoint inliers required for an "agree"
	matcher      DecorMatcher      // pluggable decor matcher (pure-Go default)
}

// spec renders the exact region hashed, for the manifest (re-runnability).
func (c roiConfig) spec() string {
	switch c.mode {
	case "corners":
		return "corners (upper-left + upper-right)"
	case "full":
		return "full frame (legacy whole-frame aHash)"
	default:
		return fmt.Sprintf("band top=%.2f h=%.2f left=%.2f w=%.2f",
			c.roi.TopFrac, c.roi.HeightFrac, c.roi.LeftFrac, c.roi.WidthFrac)
	}
}

// roiHashHex computes the ROI hash hex for an image under this config. For
// "corners" it concatenates the two upper-corner hashes (the comparison uses the
// dedicated cornersHamming so this is provenance only); for band/full it is the
// single ROI hash.
func (c roiConfig) roiHashHex(img image.Image) string {
	switch c.mode {
	case "corners":
		l := osintexport.AHashFromImageROI(img, c.corners[0])
		r := osintexport.AHashFromImageROI(img, c.corners[1])
		return osintexport.HashHex(l) + osintexport.HashHex(r)
	default:
		return osintexport.HashHex(osintexport.AHashFromImageROI(img, c.roi))
	}
}

// roiFeaturedHex reports featured-ness from a stored ROI hash hex. A uniform band
// hashes to all-zero or all-one bits (every cell equals the mean) — which is the
// featureless signature we cannot use to judge a room. This lets the pairing
// stage decide featured-ness from the stored hash without re-decoding the frame.
func (c roiConfig) roiFeaturedHex(hexStr string) bool {
	if c.mode == "corners" {
		if len(hexStr) != 32 {
			return false
		}
		return featuredHash16(hexStr[:16]) || featuredHash16(hexStr[16:])
	}
	return featuredHash16(hexStr)
}

// featuredHash16 reports whether a 16-char hex aHash shows structure: an all-zero
// or all-F hash means every cell sat on one side of the mean (a flat band).
func featuredHash16(hexStr string) bool {
	v, bad := parseHash(hexStr)
	if bad {
		return false
	}
	ones := 0
	for x := v; x != 0; x &= x - 1 {
		ones++
	}
	// Flat bands give 0 ones (all below mean) or near-64; require a real mix.
	return ones >= 4 && ones <= 60
}

// keypointCount detects static-decor keypoints in the ROI of an image (0 when
// keypoints are disabled or the matcher reports none). It feeds the per-frame
// Keypoints field.
func (c roiConfig) keypointCount(img image.Image) int {
	if !c.keypoints || c.matcher == nil {
		return 0
	}
	return c.matcher.Keypoints(c.cropROI(img))
}

// cropROI returns the ROI sub-image for the matcher (band/corners→band for
// keypoints; full→whole). It uses the primary band rect (corners reuse the band).
func (c roiConfig) cropROI(img image.Image) image.Image {
	roi := c.roi
	if c.mode == "corners" {
		// For keypoints, use the whole top band rather than two tiny corners.
		roi = osintexport.ROI{TopFrac: 0, LeftFrac: 0, WidthFrac: 1, HeightFrac: 0.35}
	}
	r := resolveRect(img, roi)
	return subImage(img, r)
}

// resolveRect resolves a fractional ROI to a pixel rectangle (mirrors the
// unexported osintexport geometry; kept local so the matcher can crop).
func resolveRect(img image.Image, roi osintexport.ROI) image.Rectangle {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	c := roi.Clamp()
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
	x1, y1 := x0+rw, y0+rh
	if x1 > b.Max.X {
		x1 = b.Max.X
	}
	if y1 > b.Max.Y {
		y1 = b.Max.Y
	}
	return image.Rect(x0, y0, x1, y1)
}

// subImage returns the sub-image for rect r if the image supports SubImage,
// otherwise the image unchanged (degrade-never-crash).
func subImage(img image.Image, r image.Rectangle) image.Image {
	type subImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	if si, ok := img.(subImager); ok {
		return si.SubImage(r)
	}
	return img
}

// buildROIConfig validates the flags and returns the resolved config. Validation
// mirrors the existing --threshold / --enhance-side fatal checks in main.go.
func buildROIConfig(mode string, top, height, left, width float64,
	roiThreshold int, keypoints bool, minInliers int) (roiConfig, error) {

	switch mode {
	case "band", "corners", "full":
	default:
		return roiConfig{}, fmt.Errorf("--roi must be band, corners, or full, got %q", mode)
	}
	if roiThreshold < 0 || roiThreshold > 64 {
		return roiConfig{}, fmt.Errorf("--roi-threshold must be 0-64 (Hamming bits), got %d", roiThreshold)
	}
	if minInliers < 0 {
		return roiConfig{}, fmt.Errorf("--min-inliers must be >= 0, got %d", minInliers)
	}
	for _, f := range []struct {
		name string
		v    float64
	}{{"--roi-top", top}, {"--roi-height", height}, {"--roi-left", left}, {"--roi-width", width}} {
		if f.v < 0 || f.v > 1 {
			return roiConfig{}, fmt.Errorf("%s must be a fraction in [0,1], got %v", f.name, f.v)
		}
	}
	if mode == "band" && (width <= 0 || height <= 0) {
		return roiConfig{}, fmt.Errorf("--roi-width and --roi-height must be > 0 for band mode")
	}

	cfg := roiConfig{
		mode:         mode,
		roiThreshold: roiThreshold,
		keypoints:    keypoints,
		minInliers:   minInliers,
	}
	switch mode {
	case "full":
		cfg.roi = osintexport.FullROI
	case "corners":
		cfg.roi = osintexport.ROI{TopFrac: 0, LeftFrac: 0, WidthFrac: 1, HeightFrac: height}.Clamp()
		// Two upper corners: each is the outer third of the top band.
		ch := height
		if ch <= 0 {
			ch = 0.35
		}
		cfg.corners = []osintexport.ROI{
			{TopFrac: 0, LeftFrac: 0, WidthFrac: 0.33, HeightFrac: ch},
			{TopFrac: 0, LeftFrac: 0.67, WidthFrac: 0.33, HeightFrac: ch},
		}
	default: // band
		cfg.roi = osintexport.ROI{TopFrac: top, LeftFrac: left, WidthFrac: width, HeightFrac: height}.Clamp()
	}
	if keypoints {
		cfg.matcher = PureGoDecorMatcher{}
	}
	return cfg, nil
}

// cornersHamming compares two concatenated corner-hash hex strings (two 16-char
// hashes each) by summing the per-corner Hamming. Returns (dist, ok); ok is
// false on a malformed hex pair.
func cornersHamming(aHex, bHex string) (int, bool) {
	if len(aHex) != 32 || len(bHex) != 32 {
		return 0, false
	}
	al, badAL := parseHash(aHex[:16])
	ar, badAR := parseHash(aHex[16:])
	bl, badBL := parseHash(bHex[:16])
	br, badBR := parseHash(bHex[16:])
	if badAL || badAR || badBL || badBR {
		return 0, false
	}
	return hamming64(al, bl) + hamming64(ar, br), true
}

// roiHammingOf computes the ROI-hash Hamming between two frames' stored ROI
// hashes under this config. Returns (dist, ok); ok is false if either hash is
// malformed (caller treats it as "unknown").
func (c roiConfig) roiHammingOf(aHex, bHex string) (int, bool) {
	if c.mode == "corners" {
		return cornersHamming(aHex, bHex)
	}
	a, badA := parseHash(aHex)
	b, badB := parseHash(bHex)
	if badA || badB {
		return 0, false
	}
	return hamming64(a, b), true
}
