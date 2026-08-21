package ground

import "testing"

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// A subject that moves must produce a PATH, not a refusal. This is the case
// becky refused outright on Jordan's Mouse Trap prank: "the grounded subject
// moves across 46% of the frame in this span, so no single framing holds it".
// A subject that moves is a pan.
func TestPanPath_FollowsAMovingSubject(t *testing.T) {
	samples := []Sample{{T: 0, X: 0.20}, {T: 1, X: 0.40}, {T: 2, X: 0.66}}
	path := PanPath(samples, 0, 2, 12, 0.25, nil)

	if len(path) < 20 {
		t.Fatalf("got %d path points for 2s at 12fps, want >= 20", len(path))
	}
	if got := Travel(path); got < 0.35 {
		t.Errorf("Travel = %.3f, want >= 0.35 — the path is not following the subject", got)
	}
	// It must START near the first sighting and END near the last, or it is
	// following something other than the subject.
	if abs(path[0].X-0.20) > 0.10 {
		t.Errorf("path starts at %.3f, want ~0.20", path[0].X)
	}
	if last := path[len(path)-1].X; abs(last-0.66) > 0.10 {
		t.Errorf("path ends at %.3f, want ~0.66", last)
	}
	// And it must be SMOOTH: no single 1/12s step may jump more than a few
	// percent of the frame, or it steps rather than pans.
	for i := 1; i < len(path); i++ {
		if d := abs(path[i].X - path[i-1].X); d > 0.06 {
			t.Errorf("step %d jumps %.3f of the frame — that is a cut, not a pan", i, d)
			break
		}
	}
}

// A CUT is a hard wall. Jordan, 2026-08-20: "occasional multi-camera clips
// (such as the mouse-trap prank it couldn't edit)". Across a camera cut the
// subject is simply somewhere else; sliding the crop through the cut drags the
// frame across an edit the viewer already accepted.
func TestPanPath_DoesNotPanAcrossACut(t *testing.T) {
	// Camera A holds the subject left; after the cut at t=1, camera B holds it
	// right. Without the cut this would smoothly sweep the whole way across.
	samples := []Sample{{T: 0.0, X: 0.20}, {T: 0.5, X: 0.21}, {T: 1.5, X: 0.80}, {T: 1.9, X: 0.81}}

	withCut := PanPath(samples, 0, 2, 12, 0.25, []float64{1.0})
	noCut := PanPath(samples, 0, 2, 12, 0.25, nil)

	biggestStep := func(p []Sample) float64 {
		m := 0.0
		for i := 1; i < len(p); i++ {
			if d := abs(p[i].X - p[i-1].X); d > m {
				m = d
			}
		}
		return m
	}
	// With the cut the move happens in ONE jump; without it, it is spread out.
	if biggestStep(withCut) < 0.3 {
		t.Errorf("with a cut the biggest step is %.3f — it smoothed across the cut instead of jumping",
			biggestStep(withCut))
	}
	if biggestStep(noCut) > 0.15 {
		t.Errorf("with no cut the biggest step is %.3f — it should be a smooth sweep", biggestStep(noCut))
	}
	// Each side must still sit on its own camera's subject.
	if abs(withCut[0].X-0.20) > 0.06 {
		t.Errorf("before the cut the path is at %.3f, want ~0.20", withCut[0].X)
	}
	if last := withCut[len(withCut)-1].X; abs(last-0.81) > 0.06 {
		t.Errorf("after the cut the path is at %.3f, want ~0.81", last)
	}
}

// A subject that holds still must NOT produce a drifting crop.
func TestPanPath_HoldsStillForAStillSubject(t *testing.T) {
	samples := []Sample{{T: 0, X: 0.50}, {T: 1, X: 0.505}, {T: 2, X: 0.498}}
	path := PanPath(samples, 0, 2, 12, 0.25, nil)
	if got := Travel(path); got > 0.02 {
		t.Errorf("Travel = %.4f on a still subject, want <= 0.02", got)
	}
}

// A single sighting is a position, not a movement.
func TestPanPath_OneSightingHolds(t *testing.T) {
	path := PanPath([]Sample{{T: 1, X: 0.7}}, 0, 3, 12, 0.25, nil)
	if len(path) != 1 || abs(path[0].X-0.7) > 1e-9 {
		t.Errorf("got %+v, want a single held position at 0.7", path)
	}
}

// Samples must drop OCCLUDED frames rather than let a full-frame box vote on
// where to aim — a shirt across the lens carries no position.
func TestSamples_DropsOcclusions(t *testing.T) {
	res := Result{OK: true, Detections: []Detection{
		{T: 0, Boxes: []Box{{X: 0.3, Y: 0.1, W: 0.2, H: 0.8}}},
		{T: 1, Boxes: []Box{{X: 0, Y: 0, W: 1, H: 1}}}, // lens covered
		{T: 2, Boxes: []Box{{X: 0.5, Y: 0.1, W: 0.2, H: 0.8}}},
	}}
	got := Samples(res, 0.92)
	if len(got) != 2 {
		t.Fatalf("got %d samples, want 2 (the occluded frame carries no position): %+v", len(got), got)
	}
	if abs(got[0].X-0.4) > 1e-9 || abs(got[1].X-0.6) > 1e-9 {
		t.Errorf("sample centres = %+v, want 0.4 and 0.6", got)
	}
}
