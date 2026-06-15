package vox

import "math"

// Small deterministic numeric helpers shared across the vox pipeline (dtw/align/
// pitch/comp). All pure, stdlib-only.

// hzToMidiF converts Hz to a fractional MIDI number; 0 for non-positive input.
func hzToMidiF(hz float64) float64 {
	if hz <= 0 {
		return 0
	}
	return 12*math.Log2(hz/440.0) + 69
}

// midiToHz converts a (fractional) MIDI number to Hz.
func midiToHz(midi float64) float64 { return 440.0 * math.Pow(2, (midi-69)/12) }

// centsBetweenHz returns the signed cents from a to b (positive = b is higher).
// Returns 0 if either is non-positive (can't compare an unvoiced frame).
func centsBetweenHz(a, b float64) float64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	return 1200 * math.Log2(b/a)
}

// overlapMs returns the overlap in ms of [aLo,aHi] and [bLo,bHi] (0 if disjoint).
func overlapMs(aLo, aHi, bLo, bHi float64) float64 {
	lo := math.Max(aLo, bLo)
	hi := math.Min(aHi, bHi)
	if hi <= lo {
		return 0
	}
	return hi - lo
}

// inSet reports whether pc is in the pitch-class set.
func inSet(pc int, set []int) bool {
	for _, x := range set {
		if x == pc {
			return true
		}
	}
	return false
}

// meanFloat is the arithmetic mean (empty => 0).
func meanFloat(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

func round2(x float64) float64 { return math.Round(x*1e2) / 1e2 }
func round3(x float64) float64 { return math.Round(x*1e3) / 1e3 }

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
