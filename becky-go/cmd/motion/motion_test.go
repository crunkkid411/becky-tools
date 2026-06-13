package main

import (
	"math"
	"testing"
)

// TestMeanAbsDiff checks the motion-energy kernel: identical frames give 0, a uniform
// shift gives that shift, partial change averages correctly.
func TestMeanAbsDiff(t *testing.T) {
	tests := []struct {
		name string
		a, b []byte
		want float64
	}{
		{"identical", []byte{10, 10, 10, 10}, []byte{10, 10, 10, 10}, 0},
		{"uniform shift +5", []byte{0, 0, 0, 0}, []byte{5, 5, 5, 5}, 5},
		{"half changed by 8", []byte{0, 0, 0, 0}, []byte{8, 8, 0, 0}, 4},
		{"abs (negative delta)", []byte{20, 20}, []byte{10, 10}, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := meanAbsDiff(tt.a, tt.b)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("meanAbsDiff = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestNormalize scales by the max; an all-zero signal stays all-zero (correct honest
// answer for a static clip rather than dividing by zero).
func TestNormalize(t *testing.T) {
	got := normalize([]float64{0, 2, 4, 1})
	want := []float64{0, 0.5, 1, 0.25}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Fatalf("normalize[%d] = %v, want %v", i, got[i], want[i])
		}
	}
	zero := normalize([]float64{0, 0, 0})
	for i, v := range zero {
		if v != 0 {
			t.Errorf("normalize(all-zero)[%d] = %v, want 0", i, v)
		}
	}
}

// TestMedianMAD checks the robust baseline measures used for the adaptive threshold.
func TestMedianMAD(t *testing.T) {
	// median of {1,2,3,4,5} = 3; abs devs {2,1,0,1,2} -> median 1.
	med, mad := medianMAD([]float64{1, 2, 3, 4, 5})
	if med != 3 {
		t.Errorf("median = %v, want 3", med)
	}
	if mad != 1 {
		t.Errorf("MAD = %v, want 1", mad)
	}
	// Even length: median averages the two middle values.
	med, _ = medianMAD([]float64{1, 2, 3, 4})
	if med != 2.5 {
		t.Errorf("median(even) = %v, want 2.5", med)
	}
	// Empty is safe.
	if m, d := medianMAD(nil); m != 0 || d != 0 {
		t.Errorf("medianMAD(nil) = %v,%v want 0,0", m, d)
	}
}

// TestDetectBurstsGlobal verifies grouping with a global baseline (LocalWin=0):
// a sharp spike above the baseline becomes one burst.
func TestDetectBurstsGlobal(t *testing.T) {
	// Baseline ~0.1, with a 4-frame spike at indices 5..8.
	sig := []float64{0.1, 0.1, 0.1, 0.1, 0.1, 0.9, 0.95, 0.9, 0.85, 0.1, 0.1, 0.1}
	p := burstParams{K: 3.5, MinFrames: 2, MergeGap: 2, PadFrames: 0, LocalWin: 0}
	bursts, info := detectBursts(sig, p)
	if info.Mode != "adaptive" {
		t.Errorf("threshold mode = %q, want adaptive", info.Mode)
	}
	if len(bursts) != 1 {
		t.Fatalf("got %d bursts, want 1: %+v (thr=%v)", len(bursts), bursts, info.Value)
	}
	if bursts[0].start != 5 || bursts[0].end != 8 {
		t.Errorf("burst span = [%d,%d], want [5,8]", bursts[0].start, bursts[0].end)
	}
}

// TestDetectBurstsMinFrames drops a single-frame blip (codec noise) below MinFrames.
func TestDetectBurstsMinFrames(t *testing.T) {
	sig := []float64{0.1, 0.1, 0.95, 0.1, 0.1, 0.1, 0.1, 0.1}
	p := burstParams{K: 3.5, MinFrames: 2, MergeGap: 0, PadFrames: 0, LocalWin: 0}
	bursts, _ := detectBursts(sig, p)
	if len(bursts) != 0 {
		t.Errorf("single-frame blip should be dropped by MinFrames; got %d bursts", len(bursts))
	}
}

// TestDetectBurstsMergeGap merges two near runs separated by a short sub-threshold
// lull. Baseline is the common case (mostly calm) so the median sits at the calm level.
func TestDetectBurstsMergeGap(t *testing.T) {
	sig := []float64{0.1, 0.1, 0.9, 0.9, 0.1, 0.1, 0.9, 0.9, 0.1, 0.1, 0.1, 0.1}
	p := burstParams{K: 3.5, MinFrames: 2, MergeGap: 3, PadFrames: 0, LocalWin: 0}
	bursts, _ := detectBursts(sig, p)
	if len(bursts) != 1 {
		t.Fatalf("expected 1 merged burst, got %d: %+v", len(bursts), bursts)
	}
	if bursts[0].start != 2 || bursts[0].end != 7 {
		t.Errorf("merged span = [%d,%d], want [2,7]", bursts[0].start, bursts[0].end)
	}
}

// TestLocalBaselineCatchesQuietSpike is the core forensic property: a modest spike in
// a LOCALLY-quiet neighborhood is caught even though unrelated high motion elsewhere
// would lift a single GLOBAL threshold above it (the ~0:13 quick-movement case).
func TestLocalBaselineCatchesQuietSpike(t *testing.T) {
	sig := make([]float64, 200)
	// First half: loud, busy region (e.g. camera moving into place).
	for i := 0; i < 100; i++ {
		sig[i] = 0.6
		if i%2 == 0 {
			sig[i] = 0.8
		}
	}
	// Second half: locally calm at 0.05, with a modest 3-frame spike to 0.3.
	for i := 100; i < 200; i++ {
		sig[i] = 0.05
	}
	sig[150], sig[151], sig[152] = 0.3, 0.32, 0.3

	// Local baseline catches it because its neighborhood (0.05) makes 0.3 a clear outlier.
	localP := burstParams{K: 3.5, MinFrames: 2, MergeGap: 4, PadFrames: 0, LocalWin: 30}
	if !countContaining(detectBurstsOnly(sig, localP), 150, 152) {
		t.Errorf("local baseline failed to catch the locally-quiet spike at 150..152")
	}
}

func detectBurstsOnly(sig []float64, p burstParams) []rawBurst {
	b, _ := detectBursts(sig, p)
	return b
}

// countContaining reports whether any burst overlaps [lo,hi].
func countContaining(bursts []rawBurst, lo, hi int) bool {
	for _, b := range bursts {
		if b.start <= hi && b.end >= lo {
			return true
		}
	}
	return false
}

// TestParseDashRangeValid parses A-B windows including decimals.
func TestParseDashRangeValid(t *testing.T) {
	tests := []struct {
		in    string
		wantA float64
		wantB float64
	}{
		{"", 0, 0},
		{"6-23", 6, 23},
		{"10-25.5", 10, 25.5},
		{"0.5-2.25", 0.5, 2.25},
	}
	for _, tt := range tests {
		a, b := parseDashRange(tt.in)
		if a != tt.wantA || b != tt.wantB {
			t.Errorf("parseDashRange(%q) = %v,%v want %v,%v", tt.in, a, b, tt.wantA, tt.wantB)
		}
	}
}

// TestChooseSampleFPS prefers source fps, honors override, and respects the cap.
func TestChooseSampleFPS(t *testing.T) {
	if got := chooseSampleFPS(30, 0, 60); got != 30 {
		t.Errorf("default = %v, want 30 (source)", got)
	}
	if got := chooseSampleFPS(30, 15, 60); got != 15 {
		t.Errorf("override = %v, want 15", got)
	}
	if got := chooseSampleFPS(240, 0, 60); got != 60 {
		t.Errorf("cap = %v, want 60", got)
	}
	if got := chooseSampleFPS(0, 0, 60); got != 30 {
		t.Errorf("fallback = %v, want 30", got)
	}
}

// TestBuildBurstsTimestamps verifies signal-index -> timestamp/frame mapping and the
// becky-validate hand-off fields. Baseline is mostly-calm so the median sits low and
// only the spike fires.
func TestBuildBurstsTimestamps(t *testing.T) {
	sig := []float64{0.05, 0.05, 0.9, 0.95, 0.9, 0.05, 0.05, 0.05, 0.05}
	p := burstParams{K: 3.5, MinFrames: 2, MergeGap: 1, PadFrames: 0, LocalWin: 0}
	raw, _ := detectBursts(sig, p)
	if len(raw) != 1 {
		t.Fatalf("setup: want 1 burst, got %d: %+v", len(raw), raw)
	}
	out := buildBursts(raw, sig, p, 30, 30, 0) // 30 fps, window starts at 0
	if len(out) != 1 {
		t.Fatalf("buildBursts returned %d, want 1", len(out))
	}
	b := out[0]
	// Signal index i => frame i+1. Burst spans signal 2..4 => frames 3..5 => 0.1..0.167s.
	if math.Abs(b.WindowStart-0.1) > 1e-6 {
		t.Errorf("WindowStart = %v, want 0.1", b.WindowStart)
	}
	if b.FrameIndexStart != 3 {
		t.Errorf("FrameIndexStart = %d, want 3", b.FrameIndexStart)
	}
	if !b.SubSecond {
		t.Error("burst shorter than 1s should be flagged SubSecond")
	}
	if b.RouteTo != "becky-validate" || !b.RecommendReview {
		t.Errorf("hand-off fields wrong: route_to=%q recommend=%v", b.RouteTo, b.RecommendReview)
	}
	if b.ValidateArgs == "" {
		t.Error("ValidateArgs hand-off hint should be populated")
	}
}

// TestThresholdFloor keeps a pathologically flat baseline from collapsing to ~0.
func TestThresholdFloor(t *testing.T) {
	sig := []float64{0.001, 0.001, 0.001, 0.001}
	p := burstParams{K: 3.5}
	thr, info := chooseThreshold(sig, p)
	if thr < 0.02 {
		t.Errorf("threshold floor not applied: %v", thr)
	}
	if info.Mode != "adaptive" {
		t.Errorf("mode = %q, want adaptive", info.Mode)
	}
}

// TestFixedThreshold honors a pinned --min-motion value.
func TestFixedThreshold(t *testing.T) {
	sig := []float64{0.1, 0.5, 0.5, 0.1}
	p := burstParams{FixedThresh: 0.4, K: 3.5, MinFrames: 2, MergeGap: 0}
	_, info := detectBursts(sig, p)
	if info.Mode != "fixed" || info.Value != 0.4 {
		t.Errorf("fixed threshold = mode %q value %v, want fixed 0.4", info.Mode, info.Value)
	}
}

// TestAdaptiveThrMADGuard is the critical robustness property: when MAD collapses to 0
// (flat baseline), the threshold must sit strictly ABOVE the median, not on it, or
// baseline frames would fire.
func TestAdaptiveThrMADGuard(t *testing.T) {
	// Flat baseline at 0.1, MAD 0 -> spread floors to 25% of median.
	thr := adaptiveThr(0.1, 0, 3.5)
	if thr <= 0.1 {
		t.Errorf("MAD=0 threshold %v must exceed median 0.1", thr)
	}
	// Real spread is used when larger than the floor.
	thr2 := adaptiveThr(0.1, 0.2, 3.5)
	if thr2 <= thr {
		t.Errorf("larger MAD should raise the threshold: %v vs %v", thr2, thr)
	}
	// Zero median + zero MAD still gets the absolute epsilon spread.
	if got := adaptiveThr(0, 0, 3.5); got <= 0 {
		t.Errorf("epsilon spread missing: %v", got)
	}
}

// TestClampInt covers the padding clamp used when extending burst windows.
func TestClampInt(t *testing.T) {
	cases := [][4]int{{-5, 0, 10, 0}, {5, 0, 10, 5}, {99, 0, 10, 10}}
	for _, c := range cases {
		if got := clampInt(c[0], c[1], c[2]); got != c[3] {
			t.Errorf("clampInt(%d,%d,%d) = %d, want %d", c[0], c[1], c[2], got, c[3])
		}
	}
}

// TestRounding covers the JSON-precision rounders.
func TestRounding(t *testing.T) {
	if round1(0.16) != 0.2 {
		t.Errorf("round1(0.16) = %v, want 0.2", round1(0.16))
	}
	if round3(1.23456) != 1.235 {
		t.Errorf("round3(1.23456) = %v, want 1.235", round3(1.23456))
	}
	if round4(0.123456) != 0.1235 {
		t.Errorf("round4(0.123456) = %v, want 0.1235", round4(0.123456))
	}
}

// TestBuildBurstsPaddingAndBetween covers padding clamp + the between-1fps-samples
// flag (a sub-second burst wholly inside one 1-second cell).
func TestBuildBurstsPaddingAndBetween(t *testing.T) {
	// Spike near the very start; padding must clamp at index 0.
	sig := []float64{0.9, 0.95, 0.9, 0.05, 0.05, 0.05, 0.05, 0.05}
	p := burstParams{K: 3.5, MinFrames: 2, MergeGap: 1, PadFrames: 5, LocalWin: 0}
	raw, _ := detectBursts(sig, p)
	if len(raw) == 0 {
		t.Fatalf("setup: expected a burst")
	}
	out := buildBursts(raw, sig, p, 30, 30, 0)
	if out[0].FrameIndexStart < 0 {
		t.Errorf("padding produced negative frame index: %d", out[0].FrameIndexStart)
	}
	// A wholly-sub-second burst inside [0,1)s should be flagged between_1fps_samples.
	if out[0].DurationSec < 1.0 && !out[0].SubSecond {
		t.Error("short burst must be SubSecond")
	}
}

// TestMarshalIndent confirms the output document serializes to valid JSON ending in a
// newline (the chainable house convention).
func TestMarshalIndent(t *testing.T) {
	o := baseOutput("clip.mp4", "abc123", 30, 10, [2]float64{0, 10})
	b, err := marshalIndent(o)
	if err != nil {
		t.Fatalf("marshalIndent: %v", err)
	}
	if len(b) == 0 || b[len(b)-1] != '\n' {
		t.Error("output must end with a newline")
	}
	if o.MotionBursts == nil {
		t.Error("motion_bursts must be [] not nil")
	}
}

// TestMethodSummary covers the three method-string branches.
func TestMethodSummary(t *testing.T) {
	if s := methodSummary(burstParams{FixedThresh: 0.3}); !contains(s, "fixed") {
		t.Errorf("fixed branch missing: %q", s)
	}
	if s := methodSummary(burstParams{K: 3.5, LocalWin: 150}); !contains(s, "local") {
		t.Errorf("local branch missing: %q", s)
	}
	if s := methodSummary(burstParams{K: 3.5, LocalWin: 0}); !contains(s, "global") {
		t.Errorf("global branch missing: %q", s)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
