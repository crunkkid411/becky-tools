// phash.go — the pure-Go phashFingerprinter: the honest deterministic floor that
// ships and works offline today (SPEC §5). It crops a decoded keyframe to the
// decor band (CropRect) and computes the masked-band aHash via
// osintexport.AHashFromImage + a coarse per-region color histogram. No model, no
// cv2 — fully cloud-testable over a synthetic image.
package location

import (
	"image"

	"becky-go/internal/osintexport"
)

// colorBins is the per-channel histogram resolution. A 4×4×4 RGB cube (64 bins)
// is coarse enough to be robust to the subject moving / minor exposure shifts but
// fine enough to separate distinct wall/decor palettes.
const colorBins = 4

// NewPhashFingerprinter returns the pure-Go Fingerprinter. It fills DecorHash +
// ColorHist; Features stays nil (the feature signal is the LOCAL cv2 helper's
// job). This is the default fingerprinter for `becky-location`.
func NewPhashFingerprinter() Fingerprinter { return phashFingerprinter{} }

type phashFingerprinter struct{}

// Print crops img to the decor band and computes the masked aHash + color
// histogram. Degrade-never-crash: a nil/empty image yields a zero-value
// fingerprint rather than a panic.
func (phashFingerprinter) Print(img image.Image, mask CropMask) (Fingerprint, error) {
	if img == nil {
		return Fingerprint{}, nil
	}
	b := img.Bounds()
	rect := CropRect(b.Dx(), b.Dy(), mask)
	// Translate the crop rect into the image's coordinate space (Bounds may not
	// start at 0,0).
	rect = rect.Add(b.Min).Intersect(b)
	sub := cropImage(img, rect)

	return Fingerprint{
		DecorHash: osintexport.AHashFromImage(sub),
		ColorHist: colorHistogram(sub),
	}, nil
}

// subImager is implemented by the stdlib image types (RGBA, NRGBA, YCbCr, …) and
// lets us crop without copying pixels.
type subImager interface {
	SubImage(r image.Rectangle) image.Image
}

func cropImage(img image.Image, rect image.Rectangle) image.Image {
	if si, ok := img.(subImager); ok {
		return si.SubImage(rect)
	}
	// Fallback: copy the region into an RGBA so AHash/histogram see only the crop.
	dst := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			dst.Set(x-rect.Min.X, y-rect.Min.Y, img.At(x, y))
		}
	}
	return dst
}

// colorHistogram computes an L1-normalized 4×4×4 RGB histogram over the (already
// cropped) image. Deterministic and stdlib-only. Returns an empty slice for an
// empty region so the engine treats color as unavailable (degrade-never-crash).
func colorHistogram(img image.Image) []float64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}
	hist := make([]float64, colorBins*colorBins*colorBins)
	var total float64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			ri := int(r>>8) * colorBins / 256
			gi := int(g>>8) * colorBins / 256
			bi := int(bl>>8) * colorBins / 256
			ri = clampBin(ri)
			gi = clampBin(gi)
			bi = clampBin(bi)
			hist[(ri*colorBins+gi)*colorBins+bi]++
			total++
		}
	}
	if total == 0 {
		return nil
	}
	for i := range hist {
		hist[i] /= total
	}
	return hist
}

func clampBin(v int) int {
	if v < 0 {
		return 0
	}
	if v >= colorBins {
		return colorBins - 1
	}
	return v
}
