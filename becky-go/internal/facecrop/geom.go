// Package facecrop is the pure-Go, deterministic, offline geometry + artifact layer
// for tight face-crop extraction (SPEC-FACE-CROP-DB.md). It takes a SCRFD/ArcFace
// detection result (a faceembed.Face: BBox, DetScore, Vector) plus the decoded,
// rotation-corrected frame image, and produces (a) a tight, margined, edge-clamped
// crop artifact and (b) the appearance_embeddings DB row that persists the embedding.
//
// It depends on NEITHER ffmpeg, Python, nor a GPU: cropping is done in-process from
// the stdlib image the perceptual hash already decodes, so the geometry is fully
// unit-testable without hardware. The only model in the chain — SCRFD/ArcFace — runs
// upstream in internal/faceembed; this package never calls it.
//
// Degrade, never crash: a degenerate/zero/NaN bbox or non-positive frame dims yield
// the empty image.Rectangle (a "skip this crop" signal the caller honors), never a
// panic.
package facecrop

import (
	"image"
	"math"
)

// CropRect computes the tight face-crop rectangle for a detection: the bbox expanded
// by margin*max(bboxW,bboxH) on every side, then clamped (intersected) to the frame
// bounds [0,frameW]x[0,frameH].
//
// bbox is [x1,y1,x2,y2] in the rotation-corrected frame's pixel coordinates (SCRFD
// ran on the upright frame), matching faceembed.Face.BBox. margin is a fraction of
// the face's LARGER side, so the context scales with face size rather than pixels
// (default 0.4 at the call site). margin is clamped to >= 0 (a negative margin would
// shrink the face and is meaningless here).
//
// The clamp is asymmetric by construction: a face flush against an edge keeps its
// full margin on the opposite side but a truncated margin against the edge — the
// behavior the unit tests pin. Coordinates are rounded to the nearest pixel.
//
// Returns the empty image.Rectangle{} (which reports Empty()==true) when the input
// is unusable: a non-positive frame dimension, a NaN/Inf in the bbox, a bbox of the
// wrong arity, or a degenerate/zero-area bbox (x2<=x1 or y2<=y1). The caller treats
// an empty rect as "skip this crop" and keeps the full-scene frame untouched.
func CropRect(bbox [4]float64, margin float64, frameW, frameH int) image.Rectangle {
	if frameW <= 0 || frameH <= 0 {
		return image.Rectangle{}
	}
	for _, v := range bbox {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return image.Rectangle{}
		}
	}
	x1, y1, x2, y2 := bbox[0], bbox[1], bbox[2], bbox[3]
	if x2 <= x1 || y2 <= y1 {
		return image.Rectangle{} // degenerate / zero-area bbox -> skip
	}
	if margin < 0 {
		margin = 0
	}

	w := x2 - x1
	h := y2 - y1
	pad := margin * math.Max(w, h)

	ex1 := int(math.Round(x1 - pad))
	ey1 := int(math.Round(y1 - pad))
	ex2 := int(math.Round(x2 + pad))
	ey2 := int(math.Round(y2 + pad))

	// Clamp (intersect) to the frame bounds.
	ex1 = clampInt(ex1, 0, frameW)
	ey1 = clampInt(ey1, 0, frameH)
	ex2 = clampInt(ex2, 0, frameW)
	ey2 = clampInt(ey2, 0, frameH)

	r := image.Rect(ex1, ey1, ex2, ey2)
	if r.Dx() <= 0 || r.Dy() <= 0 {
		// The expanded box fell entirely outside the frame (or rounded to a
		// zero-area sliver). Treat as a skip rather than a 0x0 crop.
		return image.Rectangle{}
	}
	return r
}

// clampInt returns v constrained to [lo,hi].
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
