package facecrop

import (
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
)

// defaultJPEGQuality is the JPEG quality used when SaveCrop is asked for jpg with a
// non-positive quality. 90 keeps the face crop visually faithful (it is a forensic
// artifact a human reviews + an enroll print) without bloating disk.
const defaultJPEGQuality = 90

// CropImage returns the sub-rectangle r of src copied into a FRESH *image.RGBA.
//
// We copy (rather than return src.(SubImage)) so the result is independent of the
// source decoder and carries no shared backing array — the crop is a faithful pixel
// sub-rectangle (no scaling, warping, or enhancement; it survives "what did you do
// to this image?"). The returned image's Bounds() origin is (0,0) and its size is
// the intersection of r with src.Bounds(), so the dimensions are exactly the visible
// crop. An empty r (or a r that does not intersect src) yields a 0x0 image.
func CropImage(src image.Image, r image.Rectangle) image.Image {
	if src == nil {
		return image.NewRGBA(image.Rectangle{})
	}
	clip := r.Intersect(src.Bounds())
	dst := image.NewRGBA(image.Rect(0, 0, clip.Dx(), clip.Dy()))
	if clip.Empty() {
		return dst
	}
	draw.Draw(dst, dst.Bounds(), src, clip.Min, draw.Src)
	return dst
}

// SaveCrop encodes img to outPath as the given format ("jpg"/"jpeg" or "png"),
// creating the parent directory if needed. jpegQ is the JPEG quality (1-100);
// non-positive defaults to defaultJPEGQuality and is ignored for png. An empty
// (0x0) image is a programming error from a skipped crop and returns an error rather
// than writing a 0-byte file.
func SaveCrop(img image.Image, outPath, format string, jpegQ int) error {
	if img == nil || img.Bounds().Dx() <= 0 || img.Bounds().Dy() <= 0 {
		return fmt.Errorf("facecrop: refusing to save an empty crop to %q", outPath)
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("facecrop: mkdir for crop %q: %w", outPath, err)
	}
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("facecrop: create crop %q: %w", outPath, err)
	}
	defer f.Close()

	switch strings.ToLower(strings.TrimSpace(format)) {
	case "png":
		if err := png.Encode(f, img); err != nil {
			return fmt.Errorf("facecrop: encode png %q: %w", outPath, err)
		}
	case "jpg", "jpeg", "":
		if jpegQ <= 0 || jpegQ > 100 {
			jpegQ = defaultJPEGQuality
		}
		if err := jpeg.Encode(f, img, &jpeg.Options{Quality: jpegQ}); err != nil {
			return fmt.Errorf("facecrop: encode jpeg %q: %w", outPath, err)
		}
	default:
		return fmt.Errorf("facecrop: unsupported crop format %q (want jpg or png)", format)
	}
	return nil
}
