package location

import (
	"image"
	"testing"
)

func TestCropRect_TalkingHead(t *testing.T) {
	// 1920×1080 talking-head: drop side 15% margins, keep top 30% band.
	got := CropRect(1920, 1080, MaskPreset("talking-head"))
	// x0 = 1920*0.15 = 288; x1 = 1920 - 288 = 1632; y0 = 0; y1 = 1080*0.30 = 324.
	want := image.Rect(288, 0, 1632, 324)
	if got != want {
		t.Fatalf("talking-head crop = %v, want %v", got, want)
	}
}

func TestCropRect_Full(t *testing.T) {
	got := CropRect(1920, 1080, MaskPreset("full"))
	want := image.Rect(0, 0, 1920, 1080)
	if got != want {
		t.Fatalf("full crop = %v, want %v", got, want)
	}
}

func TestCropRect_TopOnly(t *testing.T) {
	got := CropRect(1000, 1000, MaskPreset("top"))
	// no side margins; top 30% band → y1 = 300.
	want := image.Rect(0, 0, 1000, 300)
	if got != want {
		t.Fatalf("top crop = %v, want %v", got, want)
	}
}

func TestParseCrop_Explicit(t *testing.T) {
	// "10,20,20,40" = drop top 10%, left 20%, right 20%, bottom 40% (percent).
	mask, ok := ParseCrop("10,20,20,40")
	if !ok {
		t.Fatalf("ParseCrop should accept a valid explicit spec")
	}
	got := CropRect(1000, 1000, mask)
	// x0=200, x1=800, y0=100, y1=600.
	want := image.Rect(200, 100, 800, 600)
	if got != want {
		t.Fatalf("explicit crop = %v, want %v", got, want)
	}
}

func TestParseCrop_MalformedFallsBack(t *testing.T) {
	mask, ok := ParseCrop("not,a,spec")
	if ok {
		t.Fatalf("ParseCrop should report ok=false on a malformed spec")
	}
	if mask.Name != "talking-head" {
		t.Fatalf("malformed spec should fall back to talking-head, got %q", mask.Name)
	}
}

func TestCropRect_Determinism(t *testing.T) {
	mask := MaskPreset("talking-head")
	a := CropRect(1280, 720, mask)
	b := CropRect(1280, 720, mask)
	if a != b {
		t.Fatalf("CropRect not deterministic: %v vs %v", a, b)
	}
}

func TestCropRect_DegenerateFallsBackToFull(t *testing.T) {
	// A mask that drops everything must not yield an empty rect.
	got := CropRect(100, 100, CropMask{Name: "custom", Left: 0.6, Right: 0.6})
	want := image.Rect(0, 0, 100, 100)
	if got != want {
		t.Fatalf("collapsed mask should fall back to full frame, got %v", got)
	}
}
