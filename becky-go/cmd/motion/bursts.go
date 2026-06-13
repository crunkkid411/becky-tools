package main

// bursts.go turns the per-frame motion-energy signal into discrete motion bursts.
//
// The threshold is ADAPTIVE to each clip's own baseline (robust median + k*MAD),
// which is what lets one tool serve both a violent contact clip and a near-static
// one without over-firing on the calm one: a calm clip's tiny spread keeps the bar
// just above its own jitter, so ambient noise (sensor grain, light flicker) does not
// become a "burst." This is the FORENSIC-OUTPUT-PHILOSOPHY corroborate-then-conclude
// principle applied to a single signal: a frame must beat the clip's own typical
// motion by a robust margin before it counts.

import (
	"fmt"
	"math"
	"sort"
)

// burstParams controls grouping. Defaults are tuned for ~30 fps phone footage where
// an evidentiary "quick touch" can be a few frames long.
type burstParams struct {
	K           float64 // sensitivity: threshold = baseline_median + K*MAD (lower = more sensitive)
	FixedThresh float64 // if > 0, override the adaptive threshold with this fixed 0..1 value
	MinFrames   int     // a burst must span at least this many frames (kills 1-frame codec blips)
	MergeGap    int     // bursts within this many sub-threshold frames are merged into one
	PadFrames   int     // frames of context padding added on each side of a burst window
	LocalWin    int     // rolling-baseline half-width in frames (0 = global baseline only)
}

func defaultBurstParams() burstParams {
	return burstParams{
		// K=3.5 is the recall-first robust-outlier cutoff (~2.3 sigma equivalent via
		// the 1.4826 MAD->sigma factor). Verified on real footage: it catches the
		// subtle-but-evidentiary contact movements an analyst must not miss (e.g. the
		// ~0:13 quick movement in test.mp4) while a uniformly-panning clip — whose
		// high MAD raises its own bar — stays near-silent (no over-fire). Forensic
		// recall: a missed subtle movement is lost evidence; an extra candidate window
		// is cheap for a reviewer to dismiss.
		K:           3.5,
		FixedThresh: 0,
		MinFrames:   2,   // >= ~0.067s at 30fps: still sub-second, but not a single-frame blip
		MergeGap:    8,   // ~0.27s lull still counts as one continuous interaction
		PadFrames:   3,   // ~0.1s of lead-in/out so the descriptive model sees the approach
		LocalWin:    150, // ~5s neighborhood at 30fps: a spike is judged against its LOCAL
		// calm, so a quiet-then-touch (the ~0:13 movement) is caught even when unrelated
		// camera motion elsewhere in the clip would inflate a single global threshold.
	}
}

// detectBursts scans the normalized signal and returns burst intervals in SIGNAL
// indices, plus the (global) threshold info reported for auditability.
//
// Detection uses a PER-FRAME threshold: each frame is compared against the robust
// baseline of its LOCAL neighborhood (median + K*MAD over ±LocalWin frames). This is
// what lets a quiet-then-touch movement register even when unrelated motion elsewhere
// in the clip would lift a single global threshold above it — and what keeps a
// uniformly-busy (panning) clip quiet, since there every frame's neighborhood is just
// as busy as the frame itself. When LocalWin <= 0 the global threshold is used for
// every frame (still available via --merge-gap/fixed overrides).
func detectBursts(sig []float64, p burstParams) ([]rawBurst, ThresholdInfo) {
	globalThr, info := chooseThreshold(sig, p)
	perFrame := perFrameThreshold(sig, p, globalThr)

	above := func(i int) bool { return sig[i] >= perFrame[i] }

	var bursts []rawBurst
	i := 0
	for i < len(sig) {
		if !above(i) {
			i++
			continue
		}
		// Start of a candidate run; extend across small sub-threshold gaps.
		start := i
		end := i
		gap := 0
		for j := i + 1; j < len(sig); j++ {
			if above(j) {
				end = j
				gap = 0
				continue
			}
			gap++
			if gap > p.MergeGap {
				break
			}
		}
		bursts = append(bursts, rawBurst{start: start, end: end})
		i = end + 1
	}

	// Drop runs shorter than MinFrames.
	kept := bursts[:0]
	for _, b := range bursts {
		if b.end-b.start+1 >= p.MinFrames {
			kept = append(kept, b)
		}
	}
	return kept, info
}

// perFrameThreshold builds the threshold applied at each frame. With a local window it
// is the rolling robust baseline (median + K*MAD over the neighborhood), never below
// the global threshold's floor so a single quiet spike in an otherwise dead clip still
// needs to clear an absolute minimum. With LocalWin <= 0 every entry is globalThr.
func perFrameThreshold(sig []float64, p burstParams, globalThr float64) []float64 {
	out := make([]float64, len(sig))
	if p.LocalWin <= 0 || p.FixedThresh > 0 {
		for i := range out {
			out[i] = globalThr
		}
		return out
	}
	k := p.K
	if k <= 0 {
		k = defaultBurstParams().K
	}
	// Absolute floor so ambient sensor noise in a near-dead neighborhood can't make
	// the local bar collapse to ~0 and fire on grain.
	const floor = 0.08
	for i := range sig {
		lo := i - p.LocalWin
		if lo < 0 {
			lo = 0
		}
		hi := i + p.LocalWin
		if hi >= len(sig) {
			hi = len(sig) - 1
		}
		med, mad := medianMAD(sig[lo : hi+1])
		thr := adaptiveThr(med, mad, k)
		if thr < floor {
			thr = floor
		}
		if thr > 1 {
			thr = 1
		}
		out[i] = thr
	}
	return out
}

// adaptiveThr is the robust burst threshold from a baseline (median + k*MAD), with a
// guard for the MAD==0 degeneracy: when more than half a window's frames are
// near-identical (common in calm footage) MAD collapses to 0, which would put the
// threshold exactly ON the baseline and fire on baseline frames. In that case we
// require a minimum spread (a small fraction of the median, or a tiny epsilon) so the
// bar always sits strictly ABOVE the baseline.
func adaptiveThr(med, mad, k float64) float64 {
	const minSpreadFrac = 0.25 // require >=25% over a flat baseline
	const minSpreadAbs = 0.01
	spread := mad
	if floor := minSpreadFrac * med; spread < floor {
		spread = floor
	}
	if spread < minSpreadAbs {
		spread = minSpreadAbs
	}
	return med + k*spread
}

// rawBurst is a burst expressed in SIGNAL indices (each index = a frame-to-frame
// transition). Converted to source frame indices + timestamps by the caller.
type rawBurst struct {
	start int // first signal index >= threshold
	end   int // last signal index >= threshold
}

// chooseThreshold returns the applied threshold and an auditable record of how it was
// derived. Adaptive by default (median + K*MAD of the clip's own signal); fixed if the
// caller pinned a value.
func chooseThreshold(sig []float64, p burstParams) (float64, ThresholdInfo) {
	med, mad := medianMAD(sig)
	if p.FixedThresh > 0 {
		return p.FixedThresh, ThresholdInfo{
			Mode:        "fixed",
			Value:       round4(p.FixedThresh),
			BaselineMed: round4(med),
			BaselineMAD: round4(mad),
			K:           p.K,
		}
	}
	thr := adaptiveThr(med, mad, p.K)
	// Floor: never call sub-2%-of-peak motion a "burst" even if the baseline is
	// pathologically flat (avoids a perfectly static clip firing on rounding dust).
	if thr < 0.02 {
		thr = 0.02
	}
	if thr > 1 {
		thr = 1
	}
	return thr, ThresholdInfo{
		Mode:        "adaptive",
		Value:       round4(thr),
		BaselineMed: round4(med),
		BaselineMAD: round4(mad),
		K:           p.K,
	}
}

// medianMAD returns the median and the median absolute deviation (a robust,
// outlier-resistant spread measure — far better than stddev here because the bursts
// we want to find ARE the outliers and would inflate a stddev).
func medianMAD(sig []float64) (float64, float64) {
	if len(sig) == 0 {
		return 0, 0
	}
	med := median(sig)
	dev := make([]float64, len(sig))
	for i, v := range sig {
		dev[i] = math.Abs(v - med)
	}
	return med, median(dev)
}

func median(in []float64) float64 {
	if len(in) == 0 {
		return 0
	}
	s := make([]float64, len(in))
	copy(s, in)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func round4(f float64) float64 { return math.Round(f*10000) / 10000 }

func methodSummary(p burstParams) string {
	if p.FixedThresh > 0 {
		return fmt.Sprintf("fixed threshold %.3f, min %d frames, merge-gap %d, pad %d",
			p.FixedThresh, p.MinFrames, p.MergeGap, p.PadFrames)
	}
	base := "global"
	if p.LocalWin > 0 {
		base = fmt.Sprintf("rolling ±%d-frame local", p.LocalWin)
	}
	return fmt.Sprintf("%s median+%.1f*MAD, min %d frames, merge-gap %d, pad %d",
		base, p.K, p.MinFrames, p.MergeGap, p.PadFrames)
}
