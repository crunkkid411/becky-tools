package main

import (
	"image"

	"becky-go/internal/location"
)

// featureFingerprinter is the DOCUMENTED STUB for the optional cv2/ORB feature
// signal — the LOCAL-hardware half of the seam (SPEC §5). The clustering/verdict
// engine consumes location.Fingerprint only, so wiring this is a single,
// well-bounded change for the local agent.
//
// CONTRACT the local impl must satisfy (location.Fingerprinter):
//
//	Print(img image.Image, mask location.CropMask) (location.Fingerprint, error)
//
// The local implementation must:
//  1. Crop img to location.CropRect(w, h, mask) (the decor band) — reuse the
//     SAME crop as the phash path so both signals describe the same region.
//  2. Shell to internal/pyhelpers/room_features.py (cv2 ORB/AKAZE), reading the
//     image via np.fromfile + cv2.imdecode (NEVER cv2.imread — Unicode paths).
//  3. Return a Fingerprint whose DecorHash + ColorHist match the phash path AND
//     whose Features carries the ORB descriptor blob. Pairwise inlier ratio is
//     computed by the engine via the descriptor blob; for a TRUE geometric
//     inlier ratio the helper should expose a match endpoint the engine reads.
//
// Until wired, Available() reports false and `--fingerprint auto` silently
// degrades to phash (degrade-never-crash; never claims feature-grade certainty).
type featureFingerprinter struct {
	// phash is the fallback used until the cv2 helper is wired in.
	phash location.Fingerprinter
}

// newFeatureFingerprinter returns the stub. binPath/modelPath would point at the
// cv2 helper once the local agent wires it; today they are unused.
func newFeatureFingerprinter() *featureFingerprinter {
	return &featureFingerprinter{phash: location.NewPhashFingerprinter()}
}

// Available reports whether the real cv2 feature helper is wired in. It is false
// in the cloud build (stub) so `--fingerprint auto` degrades to phash.
func (f *featureFingerprinter) Available() bool { return false }

// Print currently delegates to the pure-Go phash path (no Features). The local
// agent replaces the body per the contract above.
func (f *featureFingerprinter) Print(img image.Image, mask location.CropMask) (location.Fingerprint, error) {
	return f.phash.Print(img, mask)
}
