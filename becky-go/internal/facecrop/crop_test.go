package facecrop

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

// gradient builds a w×h RGBA where each pixel encodes its own coordinates:
// R = x mod 256, G = y mod 256, so a cropped pixel's value reveals exactly which
// source coordinate it came from (lets us assert the crop is the right sub-rect).
func gradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	return img
}

func TestCropImageBoundsAndPixels(t *testing.T) {
	src := gradient(200, 150)
	r := image.Rect(40, 30, 120, 110) // 80x80
	got := CropImage(src, r)

	if w, h := got.Bounds().Dx(), got.Bounds().Dy(); w != 80 || h != 80 {
		t.Fatalf("crop dims = %dx%d, want 80x80", w, h)
	}
	if got.Bounds().Min != (image.Point{}) {
		t.Errorf("crop origin = %v, want (0,0)", got.Bounds().Min)
	}

	// Corner pixels of the crop must equal the SOURCE pixels at the mapped coords.
	check := func(cx, cy, sx, sy int) {
		t.Helper()
		wantR, wantG, wantB, wantA := src.At(sx, sy).RGBA()
		gotR, gotG, gotB, gotA := got.At(cx, cy).RGBA()
		if gotR != wantR || gotG != wantG || gotB != wantB || gotA != wantA {
			t.Errorf("crop pixel (%d,%d) = (%d,%d,%d,%d), want source (%d,%d) = (%d,%d,%d,%d)",
				cx, cy, gotR, gotG, gotB, gotA, sx, sy, wantR, wantG, wantB, wantA)
		}
	}
	check(0, 0, 40, 30)     // top-left of crop -> source (40,30)
	check(79, 0, 119, 30)   // top-right
	check(0, 79, 40, 109)   // bottom-left
	check(79, 79, 119, 109) // bottom-right
	check(10, 20, 50, 50)   // interior
}

func TestCropImageEmptyRect(t *testing.T) {
	src := gradient(100, 100)
	got := CropImage(src, image.Rectangle{})
	if got.Bounds().Dx() != 0 || got.Bounds().Dy() != 0 {
		t.Errorf("empty-rect crop = %v, want 0x0", got.Bounds())
	}
}

func TestSaveCropRoundTrip(t *testing.T) {
	src := gradient(120, 90)
	r := image.Rect(10, 10, 70, 70) // 60x60
	crop := CropImage(src, r)

	for _, format := range []string{"jpg", "png"} {
		out := filepath.Join(t.TempDir(), "crop."+format)
		if err := SaveCrop(crop, out, format, 90); err != nil {
			t.Fatalf("SaveCrop(%s): %v", format, err)
		}
		f, err := os.Open(out)
		if err != nil {
			t.Fatalf("open %s: %v", out, err)
		}
		decoded, _, err := image.Decode(f)
		f.Close()
		if err != nil {
			t.Fatalf("decode %s: %v", out, err)
		}
		if w, h := decoded.Bounds().Dx(), decoded.Bounds().Dy(); w != 60 || h != 60 {
			t.Errorf("%s round-trip dims = %dx%d, want 60x60", format, w, h)
		}
	}
}

func TestSaveCropRejectsEmpty(t *testing.T) {
	out := filepath.Join(t.TempDir(), "empty.jpg")
	empty := image.NewRGBA(image.Rectangle{})
	if err := SaveCrop(empty, out, "jpg", 90); err == nil {
		t.Error("SaveCrop on empty image should error, got nil")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("SaveCrop on empty image should not have written a file")
	}
}

func TestSaveCropUnsupportedFormat(t *testing.T) {
	out := filepath.Join(t.TempDir(), "crop.gif")
	if err := SaveCrop(gradient(10, 10), out, "gif", 90); err == nil {
		t.Error("SaveCrop with unsupported format should error, got nil")
	}
}
