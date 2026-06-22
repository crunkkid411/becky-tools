// Package location is the corpus-level room-fingerprint engine behind
// `becky-location`: feed it many clips, get back the set of DISTINCT rooms, a
// per-clip room assignment with confidence, and a same/different-dwelling
// verdict — corroborated, then concluded (FORENSIC-OUTPUT-PHILOSOPHY.md TOP
// PRINCIPLE), not a pile of pairwise maybes.
//
// SPLIT (SPEC-BECKY-LOCATION.md §5): everything in this package is pure-Go math
// over an ABSTRACT Fingerprint (decor-hash bits + color histogram + optional
// feature descriptor). It needs no media and no model, so it is fully
// cloud-buildable AND cloud-testable. The PRODUCTION of a real Fingerprint from
// a real keyframe (ffmpeg keyframe extract + cv2/ORB feature descriptors) is the
// LOCAL-hardware half and lives behind the Fingerprinter interface — a pure-Go
// phash implementation ships here, the feature implementation is a documented
// stub for the local agent to wire to the cv2 helper.
package location

import "image"

// Fingerprint is the abstract room descriptor the clustering/verdict engine
// consumes. It is intentionally media-free: the engine never sees a frame, only
// these vectors, which is what makes the whole engine cloud-testable.
//
// Seam contract (SPEC §5): the pure-Go phashFingerprinter fills DecorHash +
// ColorHist; the local cv2 featureFingerprinter additionally fills Features. The
// engine treats a nil/empty signal as "this signal is unavailable" and never
// claims a certainty the data did not earn (degrade-never-crash).
type Fingerprint struct {
	// DecorHash is the 64-bit average perceptual hash (aHash) computed over the
	// MASKED decor band (osintexport.AHashFromImage over the CropRect), NOT the
	// whole frame — that masking is the README portrait-footage fix: it keys on
	// the static room structure (ceiling/trim/corners), not the speaker's body.
	DecorHash uint64
	// ColorHist is a coarse per-region color histogram of the same masked band,
	// L1-normalized so it sums to 1. Wall/decor color is a strong, body-robust
	// dwelling signal. Empty (len 0) means "color signal unavailable".
	ColorHist []float64
	// Features holds optional ORB/AKAZE-class descriptors of the static decor.
	// nil when the local cv2 helper is absent — the engine then degrades to the
	// decor-hash + color signals with a stated lower confidence.
	Features []byte
	// FeatureInliers, when Features-based matching has been run pairwise, is not
	// stored here; feature DISTANCE is computed pairwise in distance.go. Features
	// is the raw descriptor blob carried for that comparison.
}

// HasColor reports whether the color-histogram signal is present.
func (f Fingerprint) HasColor() bool { return len(f.ColorHist) > 0 }

// HasFeatures reports whether the optional feature descriptor is present.
func (f Fingerprint) HasFeatures() bool { return len(f.Features) > 0 }

// CropMask names the decor-band crop applied before fingerprinting. The mask
// excludes the central-lower region where a talking-head subject sits so the
// fingerprint describes the ROOM, not the PERSON (SPEC §2a).
type CropMask struct {
	// Top/Left/Right/Bottom are the fractions (0..1) of the frame to DROP from
	// each edge before fingerprinting the remaining band. The talking-head preset
	// keeps the top band + side margins and drops the lower-center.
	Top, Left, Right, Bottom float64
	// KeepTopBand, when >0, restricts the kept region to the TOP fraction of the
	// frame height (plus the side margins implied by Left/Right). The talking-head
	// preset keeps the top 30% band; "top" keeps top 30% with no side margins.
	KeepTopBand float64
	// Name is the human label echoed into JSON (talking-head/top/full/custom).
	Name string
}

// Fingerprinter turns one keyframe image into a room Fingerprint.
//
// SEAM CONTRACT (the one boundary the cloud stubs — SPEC §5):
//   - phashFingerprinter (this package, pure-Go) implements it over the masked
//     decor band using osintexport.AHashFromImage + a coarse color histogram.
//     It ships and is fully testable now.
//   - featureFingerprinter (cmd/location/features_stub.go) is the documented stub
//     the LOCAL agent wires to internal/pyhelpers/room_features.py (cv2 ORB).
//
// The clustering/verdict engine consumes Fingerprint ONLY and never calls this —
// the CLI produces Fingerprints, the engine reasons over them.
type Fingerprinter interface {
	Print(img image.Image, mask CropMask) (Fingerprint, error)
}

// Standard crop presets (SPEC §3a --crop). Fractions are dropped from each edge;
// the talking-head preset additionally keeps only the top band via KeepTopBand.
const (
	defaultTopBand    = 0.30 // keep the top 30% (ceiling/upper wall/trim/window head)
	defaultSideMargin = 0.15 // keep the left/right 15% margins (door frames, wall edges)
)

// MaskPreset resolves a preset name to a CropMask. Unknown names fall back to the
// talking-head default (degrade-never-crash). "custom" is produced by ParseCrop.
func MaskPreset(name string) CropMask {
	switch name {
	case "full":
		return CropMask{Name: "full"}
	case "top":
		// Top band only, no side margins.
		return CropMask{Name: "top", KeepTopBand: defaultTopBand}
	case "talking-head", "":
		return CropMask{
			Name:        "talking-head",
			Left:        defaultSideMargin,
			Right:       defaultSideMargin,
			KeepTopBand: defaultTopBand,
		}
	default:
		return CropMask{
			Name:        "talking-head",
			Left:        defaultSideMargin,
			Right:       defaultSideMargin,
			KeepTopBand: defaultTopBand,
		}
	}
}
