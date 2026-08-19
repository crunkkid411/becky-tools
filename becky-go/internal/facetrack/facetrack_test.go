package facetrack

import (
	"math"
	"testing"
)

// box builds a [x1,y1,x2,y2] box from a top-left corner and a size.
func box(x, y, w, h float64) [4]float64 { return [4]float64{x, y, x + w, y + h} }

// vec makes a simple embedding that is close to `seed` — good enough to test
// cosine association without shipping real 512-d ArcFace vectors.
func vec(seed float64) []float64 {
	return []float64{seed, 1 - seed, seed * 0.5, 0.25}
}

// walker generates one face drifting steadily right across n frames — the
// ordinary case a tracker must never split.
func walker(n int, startX float64, step float64, v []float64) []Detection {
	out := make([]Detection, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Detection{
			Frame:    i,
			Time:     float64(i) / 30.0,
			BBox:     box(startX+step*float64(i), 100, 80, 80),
			Vector:   v,
			DetScore: 0.9,
		})
	}
	return out
}

func TestIoU_ValuesNotTruthiness(t *testing.T) {
	// Identical boxes.
	if got := IoU(box(0, 0, 10, 10), box(0, 0, 10, 10)); math.Abs(got-1.0) > 1e-9 {
		t.Errorf("IoU(identical) = %v, want 1.0", got)
	}
	// Disjoint.
	if got := IoU(box(0, 0, 10, 10), box(100, 100, 10, 10)); got != 0 {
		t.Errorf("IoU(disjoint) = %v, want 0", got)
	}
	// Half-overlap: two 10x10 boxes offset by 5 in x -> inter 50, union 150.
	want := 50.0 / 150.0
	if got := IoU(box(0, 0, 10, 10), box(5, 0, 10, 10)); math.Abs(got-want) > 1e-9 {
		t.Errorf("IoU(half) = %v, want %v", got, want)
	}
	// Touching edges is not overlap.
	if got := IoU(box(0, 0, 10, 10), box(10, 0, 10, 10)); got != 0 {
		t.Errorf("IoU(touching) = %v, want 0", got)
	}
	// An inverted box must be normalised, not produce negative area.
	inverted := [4]float64{10, 10, 0, 0}
	if got := IoU(inverted, box(0, 0, 10, 10)); math.Abs(got-1.0) > 1e-9 {
		t.Errorf("IoU(inverted, same area) = %v, want 1.0", got)
	}
	// Degenerate zero-area box.
	if got := IoU(box(0, 0, 0, 0), box(0, 0, 10, 10)); got != 0 {
		t.Errorf("IoU(zero-area) = %v, want 0", got)
	}
}

func TestCosine_RequiresBothVectorsAndSameLength(t *testing.T) {
	if _, ok := cosine(nil, vec(0.5)); ok {
		t.Error("cosine(nil, v) reported usable")
	}
	if _, ok := cosine(vec(0.5), []float64{1, 2}); ok {
		t.Error("cosine(len 4, len 2) reported usable")
	}
	got, ok := cosine([]float64{1, 0}, []float64{1, 0})
	if !ok || math.Abs(got-1.0) > 1e-9 {
		t.Errorf("cosine(identical) = %v, ok=%v; want 1.0, true", got, ok)
	}
	got, _ = cosine([]float64{1, 0}, []float64{0, 1})
	if math.Abs(got) > 1e-9 {
		t.Errorf("cosine(orthogonal) = %v, want 0", got)
	}
	// Un-normalised inputs must still give a correct similarity.
	got, _ = cosine([]float64{3, 0}, []float64{7, 0})
	if math.Abs(got-1.0) > 1e-9 {
		t.Errorf("cosine(un-normalised parallel) = %v, want 1.0", got)
	}
}

func TestBuild_OneMovingFaceIsOneTrack(t *testing.T) {
	dets := walker(30, 100, 2, vec(0.8))
	tracks := Build(dets, DefaultOptions())
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want exactly 1 — a single face was split", len(tracks))
	}
	if n := len(tracks[0].Detections); n != 30 {
		t.Errorf("track has %d detections, want 30", n)
	}
	if tracks[0].Start() != 0 {
		t.Errorf("track starts at %v, want 0", tracks[0].Start())
	}
	if math.Abs(tracks[0].End()-29.0/30.0) > 1e-9 {
		t.Errorf("track ends at %v, want %v", tracks[0].End(), 29.0/30.0)
	}
}

func TestBuild_TwoFacesStayTwoDistinctTracks(t *testing.T) {
	var dets []Detection
	for i := 0; i < 20; i++ {
		tm := float64(i) / 30.0
		dets = append(dets,
			Detection{Frame: i, Time: tm, BBox: box(100, 100, 80, 80), Vector: vec(0.9), DetScore: 0.9},
			Detection{Frame: i, Time: tm, BBox: box(500, 100, 80, 80), Vector: vec(0.1), DetScore: 0.9},
		)
	}
	tracks := Build(dets, DefaultOptions())
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2", len(tracks))
	}
	for _, tr := range tracks {
		if len(tr.Detections) != 20 {
			t.Errorf("track %d has %d detections, want 20", tr.ID, len(tr.Detections))
		}
	}
	// The two must not have swapped: each track's boxes stay on its own side.
	for _, tr := range tracks {
		cx, _ := tr.Centroid()
		for _, d := range tr.Detections {
			dcx, _ := center(d.BBox)
			if math.Abs(dcx-cx) > 1 {
				t.Errorf("track %d mixes positions: centroid %v, detection at %v", tr.ID, cx, dcx)
			}
		}
	}
}

func TestBuild_SurvivesAShortGap(t *testing.T) {
	// A face present for 10 frames, missing for 5, then back in the same place.
	var dets []Detection
	for i := 0; i < 10; i++ {
		dets = append(dets, Detection{Frame: i, Time: float64(i) / 30, BBox: box(100, 100, 80, 80), Vector: vec(0.8), DetScore: 0.9})
	}
	for i := 15; i < 25; i++ {
		dets = append(dets, Detection{Frame: i, Time: float64(i) / 30, BBox: box(100, 100, 80, 80), Vector: vec(0.8), DetScore: 0.9})
	}
	tracks := Build(dets, DefaultOptions()) // MaxGapFrames 12 > the 5-frame hole
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want 1 — a 5-frame occlusion split the track", len(tracks))
	}
	if n := len(tracks[0].Detections); n != 20 {
		t.Errorf("track has %d detections, want 20", n)
	}
}

func TestBuild_EndsTheTrackAfterALongGap(t *testing.T) {
	var dets []Detection
	for i := 0; i < 10; i++ {
		dets = append(dets, Detection{Frame: i, Time: float64(i) / 30, BBox: box(100, 100, 80, 80), Vector: vec(0.8), DetScore: 0.9})
	}
	// 40 frames later — well past MaxGapFrames.
	for i := 50; i < 60; i++ {
		dets = append(dets, Detection{Frame: i, Time: float64(i) / 30, BBox: box(100, 100, 80, 80), Vector: vec(0.8), DetScore: 0.9})
	}
	tracks := Build(dets, DefaultOptions())
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2 — a 40-frame absence must end the track", len(tracks))
	}
}

func TestBuild_EmbeddingRescuesAFastMove(t *testing.T) {
	// Frame 1's box does not overlap frame 0's at all, but the embedding matches.
	dets := []Detection{
		{Frame: 0, Time: 0.00, BBox: box(100, 100, 80, 80), Vector: vec(0.9), DetScore: 0.9},
		{Frame: 1, Time: 0.03, BBox: box(400, 100, 80, 80), Vector: vec(0.9), DetScore: 0.9},
		{Frame: 2, Time: 0.06, BBox: box(400, 100, 80, 80), Vector: vec(0.9), DetScore: 0.9},
	}
	if iou := IoU(dets[0].BBox, dets[1].BBox); iou != 0 {
		t.Fatalf("fixture is wrong: boxes overlap (IoU=%v)", iou)
	}
	tracks := Build(dets, Options{MinDetections: 1})
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want 1 — the embedding should have rescued the jump", len(tracks))
	}

	// The control: the SAME geometry with mismatched embeddings must NOT merge.
	dets[1].Vector = vec(0.05)
	dets[2].Vector = vec(0.05)
	tracks = Build(dets, Options{MinDetections: 1})
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2 — different faces must not be joined across a jump", len(tracks))
	}
}

func TestBuild_WorksWithoutEmbeddings(t *testing.T) {
	dets := walker(20, 100, 2, nil)
	tracks := Build(dets, DefaultOptions())
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want 1 — IoU-only tracking must still work", len(tracks))
	}
	if len(tracks[0].Detections) != 20 {
		t.Errorf("track has %d detections, want 20", len(tracks[0].Detections))
	}
}

func TestBuild_DropsTracksBelowMinDetections(t *testing.T) {
	dets := append(walker(20, 100, 1, vec(0.9)),
		// A single spurious detection far away.
		Detection{Frame: 5, Time: 5.0 / 30, BBox: box(900, 900, 20, 20), Vector: vec(0.2), DetScore: 0.6})
	tracks := Build(dets, DefaultOptions())
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want 1 — the one-frame blip should be dropped", len(tracks))
	}
}

func TestBuild_IsDeterministicRegardlessOfInputOrder(t *testing.T) {
	var dets []Detection
	for i := 0; i < 15; i++ {
		tm := float64(i) / 30.0
		dets = append(dets,
			Detection{Frame: i, Time: tm, BBox: box(100, 100, 80, 80), Vector: vec(0.9), DetScore: 0.9},
			Detection{Frame: i, Time: tm, BBox: box(500, 100, 80, 80), Vector: vec(0.1), DetScore: 0.9},
		)
	}
	a := Build(dets, DefaultOptions())

	// Reverse the input entirely; grouping must make the result identical.
	rev := make([]Detection, len(dets))
	for i := range dets {
		rev[i] = dets[len(dets)-1-i]
	}
	b := Build(rev, DefaultOptions())

	if len(a) != len(b) {
		t.Fatalf("track count differs with input order: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Start() != b[i].Start() || len(a[i].Detections) != len(b[i].Detections) {
			t.Fatalf("track %d differs with input order: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestBuild_EmptyInput(t *testing.T) {
	if got := Build(nil, DefaultOptions()); got != nil {
		t.Errorf("Build(nil) = %v, want nil", got)
	}
}

func TestCoverageIn_MeasuresNotAsserts(t *testing.T) {
	const fps30 = 1.0 / 30.0

	// 30 detections at 30fps, spanning 0.0 .. 0.9667s.
	tr := Track{ID: 1, Detections: walker(30, 100, 1, nil)}

	// A window the track is densely present in.
	frac, n := tr.CoverageIn(0, 1, fps30)
	if n != 30 {
		t.Errorf("n = %d, want 30", n)
	}
	if frac < 0.9 {
		t.Errorf("coverage = %.3f over a window it is present throughout, want >= 0.9", frac)
	}

	// THE CASE THAT MATTERS. Three glimpses spread across a 20s window used to
	// score 1.000 — identical to a face in every frame — because coverage was
	// (last-first)/(t1-t0), i.e. the SPAN between the outermost sightings. It
	// must now score near zero, because three frames is what was actually seen.
	sparse := Track{ID: 3, Detections: []Detection{
		{Frame: 0, Time: 0.0, BBox: box(0, 0, 10, 10)},
		{Frame: 300, Time: 10.0, BBox: box(0, 0, 10, 10)},
		{Frame: 600, Time: 20.0, BBox: box(0, 0, 10, 10)},
	}}
	frac, n = sparse.CoverageIn(0, 20, fps30)
	if n != 3 {
		t.Fatalf("sparse n = %d, want 3", n)
	}
	if frac > 0.02 {
		t.Errorf("three glimpses across 20s = %.4f coverage, want <= 0.02 — "+
			"a span-based measure scores this 1.000 and promotes a face that was barely there", frac)
	}
	// And it must be strictly beaten by the dense track, which is the whole point.
	dense, _ := tr.CoverageIn(0, 1, fps30)
	if !(dense > frac) {
		t.Errorf("dense coverage %.4f must exceed sparse coverage %.4f", dense, frac)
	}

	// A window the track is entirely outside.
	frac, n = tr.CoverageIn(10, 20, fps30)
	if n != 0 || frac != 0 {
		t.Errorf("coverage outside the track = %.3f (n=%d), want 0 (0)", frac, n)
	}

	// A single sighting proves an instant, not a span.
	single := Track{ID: 2, Detections: []Detection{{Frame: 0, Time: 5, BBox: box(0, 0, 10, 10)}}}
	frac, n = single.CoverageIn(0, 10, fps30)
	if n != 1 || frac > 0.01 {
		t.Errorf("single sighting coverage = %.4f (n=%d), want ~0 (1)", frac, n)
	}

	// Coverage is a fraction: it never exceeds 1 even if the caller passes a
	// sample period longer than the detector really used.
	if frac, _ := tr.CoverageIn(0, 1, 1.0); frac != 1 {
		t.Errorf("coverage = %.3f, want it capped at 1", frac)
	}

	// A zero-width or inverted window is not an error.
	if frac, n := tr.CoverageIn(5, 5, fps30); frac != 0 || n != 0 {
		t.Errorf("zero-width window = %.3f (n=%d), want 0 (0)", frac, n)
	}

	// An unknown sample period cannot be guessed, so it is refused rather than
	// answered with a number that looks real.
	if frac, n := tr.CoverageIn(0, 1, 0); frac != 0 || n != 0 {
		t.Errorf("samplePeriod=0 = %.3f (n=%d), want 0 (0)", frac, n)
	}
}

func TestCentroid(t *testing.T) {
	tr := Track{Detections: []Detection{
		{BBox: box(0, 0, 10, 10)},   // centre (5,5)
		{BBox: box(10, 10, 10, 10)}, // centre (15,15)
	}}
	x, y := tr.Centroid()
	if x != 10 || y != 10 {
		t.Errorf("Centroid() = (%v,%v), want (10,10)", x, y)
	}
}

func TestWithDefaults_RejectsOutOfRangeWeight(t *testing.T) {
	got := withDefaults(Options{IoUWeight: 5})
	if got.IoUWeight != DefaultOptions().IoUWeight {
		t.Errorf("IoUWeight = %v for an out-of-range input, want the default %v",
			got.IoUWeight, DefaultOptions().IoUWeight)
	}
}
