// phash_roi_test.go — value-asserting tests for the ROI-restricted aHash, the
// core fix for talking-head room matching. These build synthetic frames whose
// upper band ("ceiling/trim") and centered block ("the subject") are controlled
// independently, then assert the ROI hash keys on the band, not the body.
package osintexport

import (
	"image"
	"image/color"
	"testing"
)

// drawRect fills a rectangle of img with c.
func drawRect(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

// ceilingPattern paints a fixed, non-uniform "ceiling/trim" pattern into the top
// band (y in [0, bandH)) of a w×h image: a checker of two grays so the band's
// aHash is structured (not all-equal). The rest of the frame is a flat mid gray.
func ceilingPattern(w, h, bandH int, bg color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	drawRect(img, 0, 0, w, h, bg)
	light := color.RGBA{210, 210, 210, 255}
	dark := color.RGBA{40, 40, 40, 255}
	cell := w / 8
	if cell < 1 {
		cell = 1
	}
	for x := 0; x < w; x++ {
		c := light
		if (x/cell)%2 == 0 {
			c = dark
		}
		for y := 0; y < bandH; y++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// TestROIHashIgnoresCenteredSubject — FALSE-NEGATIVE regression. Two frames with
// an IDENTICAL ceiling band but a DIFFERENT centered subject block must produce
// an ROI Hamming of 0 (same room) while the whole-frame hash diverges past the
// threshold (the body changed). This is the bug: the body dominated the hash.
func TestROIHashIgnoresCenteredSubject(t *testing.T) {
	const w, h, bandH = 160, 160, 56 // bandH ≈ 0.35*h
	mid := color.RGBA{128, 128, 128, 255}

	a := ceilingPattern(w, h, bandH, mid)
	b := ceilingPattern(w, h, bandH, mid)
	// Different centered "subjects": A has a dark block, B has a light block,
	// both well below the ceiling band.
	drawRect(a, w/4, h/2, 3*w/4, h-8, color.RGBA{20, 20, 20, 255})
	drawRect(b, w/4, h/2, 3*w/4, h-8, color.RGBA{235, 235, 235, 255})

	band := ROI{TopFrac: 0, LeftFrac: 0, WidthFrac: 1, HeightFrac: 0.35}
	roiA := AHashFromImageROI(a, band)
	roiB := AHashFromImageROI(b, band)
	roiHam := HammingDistance(roiA, roiB)

	wholeA := AHashFromImage(a)
	wholeB := AHashFromImage(b)
	wholeHam := HammingDistance(wholeA, wholeB)

	const roiThreshold = 8
	if roiHam != 0 {
		t.Errorf("ROI Hamming = %d, want 0 (identical ceiling band, same room)", roiHam)
	}
	if wholeHam <= roiThreshold {
		t.Errorf("whole-frame Hamming = %d, want > %d (subject change should swamp the whole-frame hash)", wholeHam, roiThreshold)
	}
	t.Logf("ROI Hamming=%d  whole-frame Hamming=%d (proves ROI keys on background, not body)", roiHam, wholeHam)
}

// TestROIHashDistinguishesDifferentDecor — FALSE-POSITIVE regression. Two frames
// with the SAME global tone and the SAME centered subject but a DIFFERENT ceiling
// band must produce a large ROI Hamming (different room) even though the
// whole-frame hash stays small.
func TestROIHashDistinguishesDifferentDecor(t *testing.T) {
	const w, h, bandH = 160, 160, 56
	mid := color.RGBA{128, 128, 128, 255}

	a := ceilingPattern(w, h, bandH, mid)
	// b: same background tone + same subject, but a DIFFERENT (inverted) ceiling.
	b := image.NewRGBA(image.Rect(0, 0, w, h))
	drawRect(b, 0, 0, w, h, mid)
	light := color.RGBA{210, 210, 210, 255}
	dark := color.RGBA{40, 40, 40, 255}
	cell := w / 8
	for x := 0; x < w; x++ {
		c := dark // inverted phase vs ceilingPattern
		if (x/cell)%2 == 0 {
			c = light
		}
		for y := 0; y < bandH; y++ {
			b.SetRGBA(x, y, c)
		}
	}
	// Identical centered subject on both.
	subj := color.RGBA{20, 20, 20, 255}
	drawRect(a, w/4, h/2, 3*w/4, h-8, subj)
	drawRect(b, w/4, h/2, 3*w/4, h-8, subj)

	band := ROI{TopFrac: 0, LeftFrac: 0, WidthFrac: 1, HeightFrac: 0.35}
	roiHam := HammingDistance(AHashFromImageROI(a, band), AHashFromImageROI(b, band))
	wholeHam := HammingDistance(AHashFromImage(a), AHashFromImage(b))

	const roiThreshold = 8
	if roiHam <= roiThreshold {
		t.Errorf("ROI Hamming = %d, want > %d (different ceiling = different room)", roiHam, roiThreshold)
	}
	t.Logf("ROI Hamming=%d  whole-frame Hamming=%d (decor differs even when global tone matches)", roiHam, wholeHam)
}

// TestROIFullEqualsLegacy — back-compat. AHashFromImageROI(img, FullROI) must be
// byte-identical to the legacy AHashFromImage(img).
func TestROIFullEqualsLegacy(t *testing.T) {
	img := ceilingPattern(123, 97, 30, color.RGBA{90, 110, 130, 255})
	drawRect(img, 30, 40, 80, 90, color.RGBA{200, 30, 30, 255})
	want := AHashFromImage(img)
	got := AHashFromImageROI(img, FullROI)
	if got != want {
		t.Errorf("AHashFromImageROI(FullROI) = %016x, want legacy %016x", got, want)
	}
}

// TestROIGeometryClampAndValidate — out-of-range fractions clamp to a valid
// in-bounds rectangle and never panic or produce an empty rect.
func TestROIGeometryClampAndValidate(t *testing.T) {
	cases := []struct {
		name string
		in   ROI
	}{
		{"negative edges", ROI{TopFrac: -0.5, LeftFrac: -1, WidthFrac: 0.5, HeightFrac: 0.5}},
		{"oversize", ROI{TopFrac: 0.9, LeftFrac: 0.9, WidthFrac: 0.5, HeightFrac: 0.5}},
		{"zero size", ROI{TopFrac: 0.2, LeftFrac: 0.2, WidthFrac: 0, HeightFrac: 0}},
		{"edge at 1", ROI{TopFrac: 1, LeftFrac: 1, WidthFrac: 1, HeightFrac: 1}},
	}
	img := ceilingPattern(64, 64, 20, color.RGBA{100, 100, 100, 255})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clamped := c.in.Clamp()
			if clamped.TopFrac < 0 || clamped.TopFrac > 1 || clamped.LeftFrac < 0 || clamped.LeftFrac > 1 {
				t.Errorf("edges not clamped: %+v", clamped)
			}
			if clamped.WidthFrac <= 0 || clamped.HeightFrac <= 0 {
				t.Errorf("size not positive: %+v", clamped)
			}
			if clamped.LeftFrac+clamped.WidthFrac > 1.0001 || clamped.TopFrac+clamped.HeightFrac > 1.0001 {
				t.Errorf("ROI exceeds unit square: %+v", clamped)
			}
			r := clamped.rect(img.Bounds())
			if r.Dx() < 1 || r.Dy() < 1 {
				t.Errorf("resolved rect empty: %v", r)
			}
			// Must not panic.
			_ = AHashFromImageROI(img, c.in)
		})
	}
}
