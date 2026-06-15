package vox

import "testing"

// seq builds a feature sequence from onset values (mfcc/chroma left at 0), at 0.1s
// frame spacing — the simplest synthetic DTW fixture.
func seq(onsets ...float64) []FeatureFrame {
	out := make([]FeatureFrame, len(onsets))
	for i, o := range onsets {
		out[i] = FeatureFrame{T: float64(i) * 0.1, Onset: o}
	}
	return out
}

func TestDTW_IdenticalSequencesDiagonal(t *testing.T) {
	// Two identical sequences -> the path is the diagonal, cost 0.
	a := seq(0, 1, 0, 1, 0)
	path, cost := DTW(a, a, DefaultWeights(), 0)
	if cost != 0 {
		t.Errorf("identical sequences should cost 0, got %.4f", cost)
	}
	if len(path) != 5 {
		t.Fatalf("diagonal path length = %d, want 5", len(path))
	}
	for i, s := range path {
		if s.G != i || s.A != i {
			t.Errorf("step %d = (%d,%d), want (%d,%d)", i, s.G, s.A, i, i)
		}
	}
}

func TestDTW_EndpointsPinned(t *testing.T) {
	a := seq(0, 1, 1, 0)
	b := seq(0, 1, 0)
	path, _ := DTW(a, b, DefaultWeights(), 0)
	if len(path) == 0 {
		t.Fatal("empty path")
	}
	first, last := path[0], path[len(path)-1]
	if first.G != 0 || first.A != 0 {
		t.Errorf("first step not pinned to (0,0): %+v", first)
	}
	if last.G != len(a)-1 || last.A != len(b)-1 {
		t.Errorf("last step not pinned to (%d,%d): %+v", len(a)-1, len(b)-1, last)
	}
}

func TestDTW_PathIsMonotonic(t *testing.T) {
	a := seq(0, 1, 0, 1, 0, 1, 0)
	b := seq(0, 0, 1, 0, 1)
	path, _ := DTW(a, b, DefaultWeights(), 0)
	for i := 1; i < len(path); i++ {
		if path[i].G < path[i-1].G || path[i].A < path[i-1].A {
			t.Errorf("path not monotonic at step %d: %+v then %+v", i, path[i-1], path[i])
		}
	}
}

func TestDTW_Deterministic(t *testing.T) {
	a := seq(1, 0, 0, 1, 0, 0)
	b := seq(0, 1, 0, 0, 1, 0)
	_, c1 := DTW(a, b, DefaultWeights(), 0)
	_, c2 := DTW(a, b, DefaultWeights(), 0)
	if c1 != c2 {
		t.Errorf("DTW not deterministic: %.4f vs %.4f", c1, c2)
	}
	if c1 < 0 {
		t.Errorf("cost must be non-negative, got %.4f", c1)
	}
}

func TestDTW_EmptyInputs(t *testing.T) {
	path, cost := DTW(nil, seq(0, 1), DefaultWeights(), 0)
	if path != nil || cost != 0 {
		t.Errorf("empty guide should give nil path, 0 cost; got %+v, %.4f", path, cost)
	}
}

func TestDTW_BandConstrains(t *testing.T) {
	// A tight band still produces a valid pinned, monotonic path on equal-length seqs.
	a := seq(0, 1, 2, 3, 4, 5)
	b := seq(0, 1, 2, 3, 4, 5)
	path, _ := DTW(a, b, DefaultWeights(), 1)
	if len(path) == 0 || path[0].G != 0 || path[len(path)-1].G != 5 {
		t.Errorf("banded path malformed: %+v", path)
	}
}
