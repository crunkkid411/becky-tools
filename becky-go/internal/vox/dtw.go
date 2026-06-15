package vox

import "math"

// Dynamic Time Warping (SPEC §2.1): compute the optimal monotonic alignment (the
// warp path) between two feature sequences. The warp path IS the synchronization
// result — a monotonic sequence of (guide,alt) index pairs with the first/last
// frames pinned (boundary conditions). Constrained by a Sakoe-Chiba band (max local
// warp) so a syllable can't be stretched to absurdity — becky's bounded, explainable
// equivalent of VocALign's blind tightness knob. Fixed cost + fixed band => same
// sequences => same path (SPEC §2.4 determinism).

// FeatureWeights weight the fused DTW cost (SPEC §2.2): onset for consonant attacks,
// MFCC for phoneme identity, chroma for pitched-vowel content. Corroboration is
// built into the cost matrix — the same ">=2 signals agree" principle.
type FeatureWeights struct {
	Onset  float64
	MFCC   float64
	Chroma float64
}

// DefaultWeights are the SPEC's starting weights (onset-led).
func DefaultWeights() FeatureWeights {
	return FeatureWeights{Onset: 0.5, MFCC: 0.3, Chroma: 0.2}
}

// frameCost is the weighted local distance between a guide frame and an alt frame.
func frameCost(g, a FeatureFrame, w FeatureWeights) float64 {
	return w.Onset*math.Abs(g.Onset-a.Onset) +
		w.MFCC*math.Abs(g.MFCC-a.MFCC) +
		w.Chroma*math.Abs(g.Chroma-a.Chroma)
}

// DTW computes the warp path between guide and alt under a Sakoe-Chiba band. band
// is the max |i-j| deviation allowed (<=0 means unconstrained). Returns the path
// (guide-first, monotonic, endpoints pinned) and the total accumulated cost. Empty
// inputs return an empty path and zero cost — never a panic.
func DTW(guide, alt []FeatureFrame, w FeatureWeights, band int) ([]WarpStep, float64) {
	n, m := len(guide), len(alt)
	if n == 0 || m == 0 {
		return nil, 0
	}
	if band <= 0 {
		band = n + m // effectively unconstrained
	}
	acc := newCostMatrix(n, m)
	for i := 0; i < n; i++ {
		for j := bandLo(i, n, m, band); j <= bandHi(i, n, m, band); j++ {
			acc[i][j] = frameCost(guide[i], alt[j], w) + minPrev(acc, i, j)
		}
	}
	return tracePath(acc, n, m), acc[n-1][m-1]
}

// newCostMatrix returns an n x m matrix preset to +Inf (so out-of-band cells never
// win a min), with [0][0] left to be set first.
func newCostMatrix(n, m int) [][]float64 {
	acc := make([][]float64, n)
	for i := range acc {
		acc[i] = make([]float64, m)
		for j := range acc[i] {
			acc[i][j] = math.Inf(1)
		}
	}
	return acc
}

// minPrev is the cheapest of the three predecessors (i-1,j),(i,j-1),(i-1,j-1). For
// the origin it is 0 (the path starts pinned at [0][0]).
func minPrev(acc [][]float64, i, j int) float64 {
	if i == 0 && j == 0 {
		return 0
	}
	best := math.Inf(1)
	if i > 0 {
		best = math.Min(best, acc[i-1][j])
	}
	if j > 0 {
		best = math.Min(best, acc[i][j-1])
	}
	if i > 0 && j > 0 {
		best = math.Min(best, acc[i-1][j-1])
	}
	return best
}

// tracePath backtracks from the pinned end [n-1][m-1] to [0][0], preferring the
// diagonal on ties (deterministic), and returns the path guide-first ascending.
func tracePath(acc [][]float64, n, m int) []WarpStep {
	i, j := n-1, m-1
	var rev []WarpStep
	for i > 0 || j > 0 {
		rev = append(rev, WarpStep{G: i, A: j})
		i, j = bestPredecessor(acc, i, j)
	}
	rev = append(rev, WarpStep{G: 0, A: 0})
	for l, r := 0, len(rev)-1; l < r; l, r = l+1, r-1 { // reverse -> ascending
		rev[l], rev[r] = rev[r], rev[l]
	}
	return rev
}

// bestPredecessor picks the cheapest predecessor of (i,j), diagonal-preferred on
// ties so identical inputs always trace the same path.
func bestPredecessor(acc [][]float64, i, j int) (int, int) {
	switch {
	case i == 0:
		return 0, j - 1
	case j == 0:
		return i - 1, 0
	}
	diag, up, left := acc[i-1][j-1], acc[i-1][j], acc[i][j-1]
	if diag <= up && diag <= left {
		return i - 1, j - 1
	}
	if up <= left {
		return i - 1, j
	}
	return i, j - 1
}

// bandLo/bandHi clamp the alt index range for guide index i to the Sakoe-Chiba band
// around the diagonal (scaled when the sequences differ in length).
func bandLo(i, n, m, band int) int {
	center := i * m / maxInt(n, 1)
	if lo := center - band; lo > 0 {
		return lo
	}
	return 0
}

func bandHi(i, n, m, band int) int {
	center := i * m / maxInt(n, 1)
	if hi := center + band; hi < m-1 {
		return hi
	}
	return m - 1
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
