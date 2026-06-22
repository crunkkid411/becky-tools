package location

import (
	"image"
	"image/color"
	"testing"
)

// solidImage returns a w×h image filled with one color (a trivial synthetic frame).
func solidImage(w, h int, c color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

// bandedImage: top band one color, lower region another — so the talking-head
// crop (top band only) keys on the TOP color, proving the mask works.
func bandedImage(w, h int, top, bottom color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	cut := h * 30 / 100
	for y := 0; y < h; y++ {
		c := bottom
		if y < cut {
			c = top
		}
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func TestPhashFingerprinter_Deterministic(t *testing.T) {
	fp := NewPhashFingerprinter()
	img := solidImage(200, 200, color.RGBA{120, 130, 140, 255})
	mask := MaskPreset("talking-head")
	a, _ := fp.Print(img, mask)
	b, _ := fp.Print(img, mask)
	if a.DecorHash != b.DecorHash {
		t.Fatalf("decor hash not deterministic: %x vs %x", a.DecorHash, b.DecorHash)
	}
	if colorChi2(a.ColorHist, b.ColorHist) != 0 {
		t.Fatalf("color histogram not deterministic")
	}
	if !a.HasColor() {
		t.Fatalf("phash fingerprint should carry a color histogram")
	}
	if a.HasFeatures() {
		t.Fatalf("pure-Go phash must NOT claim features")
	}
}

// The talking-head crop keys on the TOP band: two frames whose TOP bands share a
// color but whose LOWER (subject) regions differ should produce the SAME color
// histogram — the README portrait-footage fix, in a unit test.
func TestPhashFingerprinter_MaskIgnoresLowerRegion(t *testing.T) {
	fp := NewPhashFingerprinter()
	mask := MaskPreset("talking-head")
	topColor := color.RGBA{200, 50, 50, 255}
	// Same top band; different "subject" in the lower region.
	imgA := bandedImage(300, 300, topColor, color.RGBA{10, 10, 10, 255})
	imgB := bandedImage(300, 300, topColor, color.RGBA{240, 240, 240, 255})
	a, _ := fp.Print(imgA, mask)
	b, _ := fp.Print(imgB, mask)
	if c := colorChi2(a.ColorHist, b.ColorHist); c > 0.01 {
		t.Fatalf("masked color histograms should match (top band identical), chi2=%v", c)
	}

	// With the FULL mask, the differing lower regions make them disagree.
	full := MaskPreset("full")
	af, _ := fp.Print(imgA, full)
	bf, _ := fp.Print(imgB, full)
	if c := colorChi2(af.ColorHist, bf.ColorHist); c < 0.1 {
		t.Fatalf("full-frame histograms should DIFFER (lower regions differ), chi2=%v", c)
	}
}

func TestPhashFingerprinter_NilImage(t *testing.T) {
	fp := NewPhashFingerprinter()
	got, err := fp.Print(nil, MaskPreset("talking-head"))
	if err != nil {
		t.Fatalf("nil image should degrade, not error: %v", err)
	}
	if got.HasColor() || got.HasFeatures() {
		t.Fatalf("nil image should produce an empty fingerprint")
	}
}
