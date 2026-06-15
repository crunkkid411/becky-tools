package hum

import (
	"math"

	"becky-go/internal/music"
)

// Krumhansl-Schmuckler key-finding (SPEC §3 stage 2). Deterministic, no ML: build
// a 12-bin pitch-class profile (PCP) weighted by note DURATION, correlate it
// against all 24 key profiles (12 major + 12 minor) — each profile is one K-S
// template vector rotated to each tonic — and the highest Pearson correlation
// wins. The runner-up gap is the confidence signal (a narrow gap = relative
// major/minor ambiguity, reported not guessed). A histogram + 24 fixed
// correlations => same PCP => same key, always.

// ksMajor / ksMinor are the empirically-derived K-S tonal-hierarchy template
// vectors (tonic first), the field standard since 1990 (SPEC §3, §9).
var ksMajor = [12]float64{6.35, 2.23, 3.48, 2.33, 4.38, 4.09, 2.52, 5.19, 2.39, 3.66, 2.29, 2.88}
var ksMinor = [12]float64{6.33, 2.68, 3.52, 5.38, 2.60, 3.53, 2.54, 4.75, 3.98, 2.69, 3.34, 3.17}

var pcNames = [12]string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}

// keyCandidate is one of the 24 keys scored against the PCP.
type keyCandidate struct {
	rootPC int
	major  bool
	corr   float64
}

// PitchClassProfile builds the 12-bin duration-weighted PCP from notes. Each
// note's MIDI pitch class is weighted by its duration in seconds (a held note
// counts more than a passing one, SPEC §3 stage 2).
func PitchClassProfile(notes []Note) [12]float64 {
	var pcp [12]float64
	for _, n := range notes {
		w := n.DurSec
		if w <= 0 {
			w = 1 // a zero-duration note still counts once, never negative
		}
		pcp[((n.Midi%12)+12)%12] += w
	}
	return pcp
}

// DetectKey runs K-S over the PCP and returns the winning key plus the runner-up
// gap. An empty/degenerate PCP yields the A-minor default (matching ParseKey) with
// zero confidence and Ambiguous=true — never a panic.
func DetectKey(pcp [12]float64) KeyResult {
	if sum12(pcp) == 0 {
		return KeyResult{
			Root: "A", Scale: "minor", Compose: "Am", Method: "krumhansl-schmuckler",
			Ambiguous: true, Confidence: 0,
		}
	}
	cands := scoreAllKeys(pcp)
	best, runner := cands[0], cands[1]
	gap := best.corr - runner.corr
	res := KeyResult{
		Root:     pcNames[best.rootPC],
		Scale:    scaleName(best.major),
		Compose:  composeKey(best.rootPC, best.major),
		Method:   "krumhansl-schmuckler",
		RunnerUp: composeKey(runner.rootPC, runner.major),
		CorrGap:  round4(gap),
	}
	res.Confidence, res.Ambiguous = keyConfidence(best.corr, gap)
	return res
}

// scoreAllKeys correlates the PCP against all 24 keys and returns them sorted
// best-first. Ordering is fully deterministic (corr desc, then rootPC, then
// major-before-minor) so ties never depend on map iteration.
func scoreAllKeys(pcp [12]float64) []keyCandidate {
	out := make([]keyCandidate, 0, 24)
	for root := 0; root < 12; root++ {
		out = append(out, keyCandidate{root, true, pearson(pcp, rotate(ksMajor, root))})
		out = append(out, keyCandidate{root, false, pearson(pcp, rotate(ksMinor, root))})
	}
	sortCandidates(out)
	return out
}

// keyConfidence maps the winning correlation and runner-up gap to a 0..1 number
// and an ambiguous flag. A wide gap over a strong winner => conclude; a narrow gap
// (relative major/minor) => ambiguous, both candidates already reported.
func keyConfidence(bestCorr, gap float64) (conf float64, ambiguous bool) {
	const narrowGap = 0.04 // relative-major/minor confusion zone (K-S known weakness)
	c := 0.5*clamp01(bestCorr) + 0.5*clamp01(gap/0.25)
	if gap < narrowGap {
		return round4(c * 0.7), true
	}
	return round4(c), false
}

// rotate shifts a K-S template so index 0 lands on tonic pc=root.
func rotate(tpl [12]float64, root int) [12]float64 {
	var out [12]float64
	for i := 0; i < 12; i++ {
		out[i] = tpl[((i-root)%12+12)%12]
	}
	return out
}

// pearson is the Pearson correlation between two 12-bin vectors.
func pearson(a, b [12]float64) float64 {
	ma, mb := mean12(a), mean12(b)
	var num, da, db float64
	for i := 0; i < 12; i++ {
		x, y := a[i]-ma, b[i]-mb
		num += x * y
		da += x * x
		db += y * y
	}
	den := math.Sqrt(da * db)
	if den == 0 {
		return 0
	}
	return num / den
}

func scaleName(major bool) string {
	if major {
		return "major"
	}
	return "minor"
}

// composeKey renders the key in the exact form becky-compose --key parses (uppercase
// root, trailing "m" for minor), cross-checked against music.ParseKey.
func composeKey(rootPC int, major bool) string {
	if major {
		return pcNames[rootPC]
	}
	return pcNames[rootPC] + "m"
}

// ScaleTonesPC returns the in-key pitch classes for a compose-style key string,
// reusing the music package so hum and compose agree on the scale exactly.
func ScaleTonesPC(composeKey string) []int {
	rootPC, scale := music.ParseKey(composeKey)
	iv := music.ScaleIntervals(scale)
	out := make([]int, 0, len(iv))
	for _, semi := range iv {
		out = append(out, (rootPC+semi)%12)
	}
	return out
}

func sum12(v [12]float64) float64 {
	var s float64
	for _, x := range v {
		s += x
	}
	return s
}

func mean12(v [12]float64) float64 { return sum12(v) / 12 }
