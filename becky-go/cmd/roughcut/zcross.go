package main

// zcross.go — snap cut boundaries onto zero-crossings in quiet audio.
//
// WHY: cutting a waveform mid-energy puts an audible POP on the cut (Jordan
// hears it instantly; a human editor cuts at zero-crossings for exactly this
// reason). becky-cut's auto-editor margins land NEAR silence but not ON a
// crossing. For every keep boundary this finds the nearest sample-accurate
// zero-crossing inside a +/-20ms window whose neighbourhood is below the
// quiet floor; when the window holds no quiet crossing (the boundary sits on
// real energy) it extends outward to the nearest quiet pocket instead of
// cutting through a word, and only gives up (leaving the boundary untouched,
// observed in the report) when there is no quiet within 0.35s.

import "math"

const (
	snapWindowSec = 0.020 // +/- window for the zero-crossing search
	snapExtendSec = 0.350 // outward search for a quiet pocket when the window fails
	peakWinSec    = 0.003 // half-width of the peak measured around a crossing
	rmsWinSec     = 0.010 // window for the quiet-pocket RMS scan
)

// snapBoundary returns the sample time of the quiet zero-crossing nearest to
// t, or t unchanged (snapped=false) when none exists within the search budget.
// quietDB is the clip's own room tone plus a few dB: on quiet recordings an
// absolute floor like -40 dBFS would call everything quiet and snap straight
// into speech. samples is mono float32 in -1..1 at rate. Ties prefer the
// EARLIER crossing: keeping a hair more audio before a word is the safe
// direction (clipped onsets were the defect this fixes).
func snapBoundary(samples []float32, rate int, t, quietDB float64) (float64, bool) {
	if rate <= 0 || len(samples) == 0 {
		return t, false
	}
	center := int(t * float64(rate))
	if center < 0 {
		center = 0
	}
	if center >= len(samples) {
		center = len(samples) - 1
	}
	w := int(snapWindowSec * float64(rate))
	peakW := int(peakWinSec * float64(rate))

	best := -1
	bestDist := math.MaxInt64
	for i := center - w; i <= center+w; i++ {
		if i < 1 || i >= len(samples) {
			continue
		}
		if !isCrossing(samples[i-1], samples[i]) {
			continue
		}
		if peakDB(samples, i, peakW) > quietDB {
			continue
		}
		if d := abs(i - center); d < bestDist {
			bestDist = d
			best = i
		}
	}
	if best >= 0 {
		return float64(best) / float64(rate), true
	}

	// No quiet crossing at the boundary: it sits on energy. Walk outward to the
	// nearest quiet pocket and cut there instead of through the word.
	ext := int(snapExtendSec * float64(rate))
	rmsW := int(rmsWinSec * float64(rate))
	for d := w; d <= ext; d++ {
		for _, i := range [2]int{center - d, center + d} {
			if i < rmsW || i+rmsW >= len(samples) {
				continue
			}
			if rmsDB(samples, i, rmsW) <= quietDB {
				return float64(i) / float64(rate), true
			}
		}
	}
	return t, false
}

func isCrossing(a, b float32) bool {
	return (a <= 0 && b > 0) || (a > 0 && b <= 0)
}

// peakDB is the dBFS of the largest |sample| within +/-w of i.
func peakDB(samples []float32, i, w int) float64 {
	var peak float32
	for j := i - w; j <= i+w; j++ {
		if j < 0 || j >= len(samples) {
			continue
		}
		if v := float32(math.Abs(float64(samples[j]))); v > peak {
			peak = v
		}
	}
	return dbfs(float64(peak))
}

// rmsDB is the dBFS RMS of samples[i : i+w].
func rmsDB(samples []float32, i, w int) float64 {
	var sum float64
	for j := i; j < i+w && j < len(samples); j++ {
		v := float64(samples[j])
		sum += v * v
	}
	return dbfs(math.Sqrt(sum / float64(w)))
}

func dbfs(v float64) float64 {
	if v <= 1e-7 {
		return -200
	}
	return 20 * math.Log10(v)
}

func abs(i int) int {
	if i < 0 {
		return -i
	}
	return i
}
