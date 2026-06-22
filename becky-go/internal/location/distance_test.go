package location

import (
	"math"
	"testing"
)

func TestDecorHamming_ExactValues(t *testing.T) {
	if d := decorHamming(0x0, 0x0); d != 0 {
		t.Fatalf("hamming(0,0) = %d, want 0", d)
	}
	if d := decorHamming(0x0, 0xF); d != 4 {
		t.Fatalf("hamming(0,0xF) = %d, want 4", d)
	}
	if d := decorHamming(0xFFFFFFFFFFFFFFFF, 0x0); d != 64 {
		t.Fatalf("hamming(all-ones,0) = %d, want 64", d)
	}
}

func TestColorChi2_IdenticalIsZero(t *testing.T) {
	p := []float64{0.25, 0.25, 0.25, 0.25}
	if c := colorChi2(p, p); c != 0 {
		t.Fatalf("chi2(identical) = %v, want 0", c)
	}
}

func TestColorChi2_DisjointApproachesOne(t *testing.T) {
	p := []float64{1, 0, 0, 0}
	q := []float64{0, 1, 0, 0}
	c := colorChi2(p, q)
	// sum = (1-0)^2/(1) + (0-1)^2/(1) = 2; /2 = 1.0.
	if math.Abs(c-1.0) > 1e-9 {
		t.Fatalf("chi2(disjoint) = %v, want 1.0", c)
	}
}

func TestColorChi2_Unavailable(t *testing.T) {
	if c := colorChi2(nil, []float64{1}); c != -1 {
		t.Fatalf("chi2 with missing hist should be -1, got %v", c)
	}
	if c := colorChi2([]float64{1, 2}, []float64{1}); c != -1 {
		t.Fatalf("chi2 with length mismatch should be -1, got %v", c)
	}
}

// fuse must report agreeingSignals == 2 only when two signals are under
// threshold, and 1 (weak link) when only one is.
func TestFuse_AgreementCounts(t *testing.T) {
	t.Helper()
	thr := DefaultThresholds()

	// Both decor (close hash) AND color (close hist) agree → 2 signals.
	a := Fingerprint{DecorHash: 0x0, ColorHist: []float64{0.25, 0.25, 0.25, 0.25}}
	b := Fingerprint{DecorHash: 0x3, ColorHist: []float64{0.26, 0.24, 0.25, 0.25}} // hamming 2
	s := fuse(a, b, thr)
	if s.DecorHamming != 2 {
		t.Fatalf("decor hamming = %d, want 2", s.DecorHamming)
	}
	if s.Agreeing != 2 {
		t.Fatalf("both signals close → agreeing = %d, want 2", s.Agreeing)
	}

	// Decor far apart, color still close → only ONE agreeing signal (weak link).
	c := Fingerprint{DecorHash: 0xFFFFFFFFFFFFFFFF, ColorHist: []float64{0.25, 0.25, 0.25, 0.25}}
	d := Fingerprint{DecorHash: 0x0, ColorHist: []float64{0.25, 0.25, 0.25, 0.25}} // hamming 64
	s2 := fuse(c, d, thr)
	if s2.DecorAgrees {
		t.Fatalf("decor should NOT agree at hamming 64")
	}
	if !s2.ColorAgrees {
		t.Fatalf("identical color should agree")
	}
	if s2.Agreeing != 1 {
		t.Fatalf("only color agrees → agreeing = %d, want 1", s2.Agreeing)
	}
}

func TestFuse_DecorOnlyWhenNoColor(t *testing.T) {
	thr := DefaultThresholds()
	// No color histogram on either → only the decor signal is available.
	a := Fingerprint{DecorHash: 0x0}
	b := Fingerprint{DecorHash: 0x1} // hamming 1
	s := fuse(a, b, thr)
	if s.Available != 1 {
		t.Fatalf("only decor available → Available = %d, want 1", s.Available)
	}
	if s.Agreeing != 1 {
		t.Fatalf("decor agrees → Agreeing = %d, want 1", s.Agreeing)
	}
}

func TestFeatureDistance(t *testing.T) {
	if d := featureDistance(1.0); d != 0 {
		t.Fatalf("featureDistance(1.0) = %v, want 0", d)
	}
	if d := featureDistance(0.0); d != 1 {
		t.Fatalf("featureDistance(0.0) = %v, want 1", d)
	}
	if d := featureDistance(0.4); math.Abs(d-0.6) > 1e-9 {
		t.Fatalf("featureDistance(0.4) = %v, want 0.6", d)
	}
}
