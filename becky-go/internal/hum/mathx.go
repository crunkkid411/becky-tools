package hum

import (
	"math"
	"sort"
)

// Small deterministic numeric helpers shared across the hum pipeline. Kept here so
// keyfind/tempo/segment/suggest stay focused; all are pure and stdlib-only.

// HzToMidiF converts a frequency in Hz to a fractional MIDI number
// (12*log2(f/440)+69). Returns 0 for non-positive frequencies (unvoiced frames).
func HzToMidiF(hz float64) float64 {
	if hz <= 0 {
		return 0
	}
	return 12*math.Log2(hz/440.0) + 69
}

// MidiToHz converts a (possibly fractional) MIDI number to Hz.
func MidiToHz(midi float64) float64 {
	return 440.0 * math.Pow(2, (midi-69)/12)
}

// CentsBetween returns the absolute cents distance between two pitch classes a and
// b (0..11), taking the shorter way around the 12-semitone circle. 100 cents = 1
// semitone, so the max is 600 cents (a tritone).
func CentsBetween(aPC, bPC int) float64 {
	d := ((aPC-bPC)%12 + 12) % 12
	if d > 6 {
		d = 12 - d
	}
	return float64(d) * 100
}

// NoteName renders a MIDI number as a note name without octave (C, C#, ... B).
func NoteName(midi int) string {
	return pcNames[((midi%12)+12)%12]
}

// medianInt returns the median of a copy of xs (xs is left untouched). Empty => 0.
func medianInt(xs []int) int {
	if len(xs) == 0 {
		return 0
	}
	c := append([]int(nil), xs...)
	sort.Ints(c)
	return c[len(c)/2]
}

// medianFloat returns the median of a copy of xs (xs is left untouched). Empty => 0.
func medianFloat(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	c := append([]float64(nil), xs...)
	sort.Float64s(c)
	return c[len(c)/2]
}

// meanFloat returns the arithmetic mean of xs (empty => 0).
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

// round4 rounds to 4 decimals for stable, readable JSON (and reproducible compares).
func round4(x float64) float64 { return math.Round(x*1e4) / 1e4 }

func round2(x float64) float64 { return math.Round(x*1e2) / 1e2 }

// sortCandidates orders keys best-first deterministically: correlation desc, then
// rootPC asc, then major before minor — so equal correlations never tie-break on
// slice/map nondeterminism.
func sortCandidates(c []keyCandidate) {
	sort.SliceStable(c, func(i, j int) bool {
		if c[i].corr != c[j].corr {
			return c[i].corr > c[j].corr
		}
		if c[i].rootPC != c[j].rootPC {
			return c[i].rootPC < c[j].rootPC
		}
		return c[i].major && !c[j].major
	})
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
