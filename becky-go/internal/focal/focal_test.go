package focal

import (
	"math"
	"testing"
)

// frame builds a Grid x Grid grayscale frame with a filled square of `val` at
// (cx,cy) with side `size`, on a mid-grey background.
func frame(cx, cy, size int, val byte) []byte {
	f := make([]byte, Grid*Grid)
	for i := range f {
		f[i] = 100
	}
	for y := cy - size/2; y < cy+size/2; y++ {
		for x := cx - size/2; x < cx+size/2; x++ {
			if x < 0 || y < 0 || x >= Grid || y >= Grid {
				continue
			}
			f[y*Grid+x] = val
		}
	}
	return f
}

func TestCentroidXFindsTheMovingObjectNotTheFrameCentre(t *testing.T) {
	// An object sitting at three quarters across, moving slightly. The centroid
	// must land on IT, not in the middle of the frame.
	a := frame(48, 32, 6, 240)
	b := frame(50, 32, 6, 240)
	x, area, ok := CentroidX(a, b, Grid)
	if !ok {
		t.Fatalf("no centroid; area=%.4f", area)
	}
	if math.Abs(x-0.766) > 0.06 {
		t.Errorf("centroid x = %.3f, want ~0.766 (the object at 49/64), area=%.4f", x, area)
	}
	if x < 0.6 {
		t.Errorf("centroid drifted toward frame centre (%.3f) — that is the bug this exists to avoid", x)
	}
}

func TestCentroidXRefusesAWholeFrameChange(t *testing.T) {
	// A camera pan / cut / light change: everything moved. There is no object to
	// point at and aiming at the centroid of "everything" is meaningless.
	a := make([]byte, Grid*Grid)
	b := make([]byte, Grid*Grid)
	for i := range a {
		a[i] = 40
		b[i] = 200
	}
	if _, area, ok := CentroidX(a, b, Grid); ok {
		t.Errorf("accepted a whole-frame change as a focal point (area=%.3f)", area)
	}
}

func TestCentroidXRefusesNoise(t *testing.T) {
	// Codec dithering: a couple of cells wobble by a few levels.
	a := frame(10, 10, 1, 103)
	b := frame(10, 10, 1, 100)
	if _, _, ok := CentroidX(a, b, Grid); ok {
		t.Error("accepted single-cell dithering as a focal point")
	}
	// A dead-still frame pair.
	still := frame(20, 20, 8, 200)
	if _, _, ok := CentroidX(still, still, Grid); ok {
		t.Error("accepted a motionless pair as a focal point")
	}
}

func TestCentroidXRejectsMalformedInput(t *testing.T) {
	if _, _, ok := CentroidX(nil, nil, Grid); ok {
		t.Error("accepted nil frames")
	}
	if _, _, ok := CentroidX(make([]byte, 10), make([]byte, Grid*Grid), Grid); ok {
		t.Error("accepted mismatched frame sizes")
	}
}

func TestSummariseCommitsOnlyWhenTheAimHoldsStill(t *testing.T) {
	// A subject that stays put: the aim is its position and it is trusted.
	var steady []float64
	for i := 0; i < 30; i++ {
		steady = append(steady, 0.72+float64(i%3)*0.005)
	}
	a := Summarise(steady, 31)
	if !a.Stable {
		t.Fatalf("a steady aim was rejected: %+v", a)
	}
	if math.Abs(a.X-0.725) > 0.02 {
		t.Errorf("aim X = %.3f, want ~0.725", a.X)
	}

	// A subject that will not settle: refuse, and say why.
	var jumpy []float64
	for i := 0; i < 30; i++ {
		if i%2 == 0 {
			jumpy = append(jumpy, 0.15)
		} else {
			jumpy = append(jumpy, 0.85)
		}
	}
	b := Summarise(jumpy, 31)
	if b.Stable {
		t.Errorf("a centroid bouncing between 0.15 and 0.85 was called stable: %+v", b)
	}
	if b.Reason == "" {
		t.Error("an unstable aim must say why")
	}

	// Too little evidence is not an aim.
	c := Summarise([]float64{0.5, 0.5, 0.5}, 4)
	if c.Stable {
		t.Errorf("3 pairs was called stable: %+v", c)
	}
	if c.Reason == "" {
		t.Error("an under-evidenced aim must say why")
	}
}

// The end-to-end decision on synthetic frames: an object moving in the right
// third of frame across a whole span must produce a stable aim in that third.
func TestFindDecisionOnASyntheticSpan(t *testing.T) {
	var frames [][]byte
	for i := 0; i < 40; i++ {
		frames = append(frames, frame(46+i%3, 30, 6, 240))
	}
	var xs []float64
	for i := 1; i < len(frames); i++ {
		if x, _, ok := CentroidX(frames[i-1], frames[i], Grid); ok {
			xs = append(xs, x)
		}
	}
	a := Summarise(xs, len(frames))
	if !a.Stable {
		t.Fatalf("a subject moving in place was not a stable aim: %+v", a)
	}
	if a.X < 0.6 || a.X > 0.85 {
		t.Errorf("aim X = %.3f, want the right third where the object actually is", a.X)
	}
}
