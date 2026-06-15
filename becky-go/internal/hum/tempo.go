package hum

import "math"

// Tempo estimation (SPEC §3 stage 3). The deterministic Go floor: from a set of
// onset times (seconds), estimate BPM by autocorrelating the inter-onset-interval
// structure, then resolve the classic half/double-tempo octave ambiguity with a
// fixed rule (prefer the genre window if supplied, else the band nearest 120).
// Fixed constants + integer-rounded BPM => same onsets => same BPM, every machine.

const (
	minBPM       = 40   // below this we don't trust a tempo
	maxBPM       = 300  // above this it's almost certainly an octave error
	preferCenter = 120  // when octave-ambiguous and no genre, snap toward here
	bpmLagStep   = 0.25 // BPM granularity of the autocorrelation search
)

// TempoOptions tunes octave resolution. GenreLo/GenreHi (inclusive, 0 = unset)
// define a preferred BPM window; when the raw BPM has a half/double sibling inside
// that window, becky reports the sibling and marks ResolvedBy "genre-window".
type TempoOptions struct {
	GenreLo int
	GenreHi int
}

// EstimateTempo returns the BPM decision from onset times. Fewer than 2 onsets =>
// a degraded zero-confidence default (120), never a panic.
func EstimateTempo(onsets []float64, opt TempoOptions) TempoResult {
	iois := interOnsetIntervals(onsets)
	if len(iois) == 0 {
		return TempoResult{BPM: preferCenter, Method: "onset-autocorrelation", Confidence: 0, ResolvedBy: "default-no-onsets"}
	}
	rawBPM, strength := bestPeriodBPM(iois)
	if rawBPM <= 0 {
		return TempoResult{BPM: preferCenter, Method: "onset-autocorrelation", Confidence: 0, ResolvedBy: "default-flat"}
	}
	bpm, resolvedBy := resolveOctave(rawBPM, opt)
	return TempoResult{
		BPM:        bpm,
		Confidence: round2(clamp01(strength)),
		Method:     "onset-autocorrelation",
		Alt:        octaveAlternatives(bpm),
		ResolvedBy: resolvedBy,
	}
}

// interOnsetIntervals returns the gaps (seconds) between consecutive sorted onsets,
// dropping non-positive gaps. Onsets are sorted defensively (determinism).
func interOnsetIntervals(onsets []float64) []float64 {
	if len(onsets) < 2 {
		return nil
	}
	s := append([]float64(nil), onsets...)
	sortFloats(s)
	out := make([]float64, 0, len(s)-1)
	for i := 1; i < len(s); i++ {
		if d := s[i] - s[i-1]; d > 0 {
			out = append(out, d)
		}
	}
	return out
}

// bestPeriodBPM searches candidate BPMs and scores each by how well the IOIs cluster
// at integer multiples of that beat period (a discrete autocorrelation of the onset
// pattern). Returns the best BPM and a 0..1 strength (peak / total mass).
func bestPeriodBPM(iois []float64) (float64, float64) {
	bestBPM, bestScore, total := 0.0, 0.0, 0.0
	steps := 0
	for bpm := float64(minBPM); bpm <= maxBPM; bpm += bpmLagStep {
		score := combFit(iois, 60.0/bpm)
		total += score
		steps++
		if score > bestScore {
			bestScore, bestBPM = score, bpm
		}
	}
	if total == 0 || steps == 0 {
		return 0, 0
	}
	// Strength: how dominant the winning period is over the average — bounded 0..1.
	avg := total / float64(steps)
	strength := 0.0
	if avg > 0 {
		strength = (bestScore/avg - 1) / 4
	}
	return bestBPM, clamp01(strength)
}

// combFit scores how well the IOIs align to a beat period: each IOI contributes a
// triangular weight by its distance to the nearest integer multiple of period. This
// is the autocorrelation/tempogram idea reduced to onset intervals (deterministic).
func combFit(iois []float64, period float64) float64 {
	if period <= 0 {
		return 0
	}
	var fit float64
	for _, d := range iois {
		mult := math.Round(d / period)
		if mult < 1 {
			mult = 1
		}
		ideal := mult * period
		err := math.Abs(d-ideal) / period // 0 = perfect, 0.5 = worst
		if w := 1 - 2*err; w > 0 {
			fit += w / mult // longer multiples count less (favor the base beat)
		}
	}
	return fit
}

// resolveOctave applies the half/double-tempo rule. If a genre window is supplied,
// prefer whichever of {bpm, bpm*2, bpm/2} lands inside it. Otherwise snap toward
// preferCenter (120). Deterministic: a fixed preference order.
func resolveOctave(rawBPM float64, opt TempoOptions) (int, string) {
	bpm := int(math.Round(rawBPM))
	if opt.GenreLo > 0 && opt.GenreHi >= opt.GenreLo {
		for _, c := range []int{bpm, bpm * 2, (bpm + 1) / 2} {
			if c >= opt.GenreLo && c <= opt.GenreHi {
				return c, "genre-window"
			}
		}
	}
	best, bestDist, why := bpm, absInt(bpm-preferCenter), "raw"
	for _, c := range []int{bpm * 2, (bpm + 1) / 2} {
		if c < minBPM || c > maxBPM {
			continue
		}
		if d := absInt(c - preferCenter); d < bestDist {
			best, bestDist, why = c, d, "octave-nearest-120"
		}
	}
	return best, why
}

// octaveAlternatives lists the half/double siblings of bpm that are in range, so the
// JSON shows the producer the other plausible tempos (SPEC §4 "alt").
func octaveAlternatives(bpm int) []int {
	var out []int
	if h := (bpm + 1) / 2; h >= minBPM {
		out = append(out, h)
	}
	if d := bpm * 2; d <= maxBPM {
		out = append(out, d)
	}
	return out
}

// sortFloats is a tiny dependency-free insertion sort (N is small, fully
// deterministic).
func sortFloats(xs []float64) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}
