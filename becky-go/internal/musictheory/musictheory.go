// Package musictheory is becky's deterministic music-theory toolkit: the analysis
// math the arranger and the genre-research pipeline lean on — transposition,
// harmonic-function classification, and voicing-from-intervals — plus Evaluate (in
// evaluate.go), where becky checks its OWN generated music against the universal
// constraints before shipping it.
//
// Ported from the ACE-Step-DAW music-theory-engine skill (clean-room: the RULES, not
// the text). These are pure functions — no model, no tokens — per becky's "music is
// math, not tokens" invariant. See STANDARDS-MUSIC-RESEARCH.md and ARRANGEMENT-RULES.md.
package musictheory

// Function is a chord's harmonic role.
type Function string

const (
	Tonic       Function = "tonic"       // stability / home — I/i, iii/III, vi/VI
	Subdominant Function = "subdominant" // movement / departure — ii/ii°, IV/iv
	Dominant    Function = "dominant"    // tension → resolves to tonic — V, vii°
)

// ClassifyFunction maps a 0-based scale-degree index (0 = I/i, 4 = V, …) to its
// harmonic function. The degree is taken mod 7, so any octave-displaced index works.
func ClassifyFunction(degreeIndex int) Function {
	switch ((degreeIndex % 7) + 7) % 7 {
	case 0, 2, 5: // I/i, iii/III, vi/VI
		return Tonic
	case 1, 3: // ii/ii°, IV/iv
		return Subdominant
	default: // 4, 6 → V, vii°
		return Dominant
	}
}

// Common chord interval patterns (semitones above the root) — used with
// VoiceFromIntervals so voicings are REASONED FROM INTERVALS, not table-looked-up
// (the music-theory-engine "reason from intervals" rule).
var (
	MajorTriad = []int{0, 4, 7}
	MinorTriad = []int{0, 3, 7}
	DimTriad   = []int{0, 3, 6}
	AugTriad   = []int{0, 4, 8}
	Dom7       = []int{0, 4, 7, 10}
	Maj7       = []int{0, 4, 7, 11}
	Min7       = []int{0, 3, 7, 10}
	Min9       = []int{0, 3, 7, 10, 14}
	Sus4       = []int{0, 5, 7}
	Power      = []int{0, 7} // power chord (root + fifth)
)

// VoiceFromIntervals builds a chord voicing from a root MIDI note and a set of
// intervals in semitones (e.g. VoiceFromIntervals(60, Maj7) → C major 7). Notes that
// would fall outside MIDI [0,127] are dropped. Deterministic, order-preserving.
func VoiceFromIntervals(root int, intervals []int) []int {
	out := make([]int, 0, len(intervals))
	for _, iv := range intervals {
		n := root + iv
		if n >= 0 && n <= 127 {
			out = append(out, n)
		}
	}
	return out
}

// SemitonesBetween returns the smallest non-negative semitone shift that moves
// pitch-class fromPC up to toPC (0..11). Used to transpose between keys.
func SemitonesBetween(fromPC, toPC int) int {
	d := ((toPC-fromPC)%12 + 12) % 12
	return d
}

// Transpose shifts every MIDI note by `semitones`, clamping to [0,127]. The
// deterministic core of a key change; enharmonic spelling is moot for MIDI numbers
// (a pitch class is a number), so the only post-condition worth checking — that the
// result stays in the target scale — is done by InScale / Evaluate.
func Transpose(notes []int, semitones int) []int {
	out := make([]int, len(notes))
	for i, n := range notes {
		v := n + semitones
		if v < 0 {
			v = 0
		}
		if v > 127 {
			v = 127
		}
		out[i] = v
	}
	return out
}

// InScale reports whether a MIDI note's pitch class is in the scale defined by
// rootPC (0..11) and scale intervals (semitones from the root, e.g. natural minor =
// {0,2,3,5,7,8,10}).
func InScale(note, rootPC int, scaleIntervals []int) bool {
	pc := ((note % 12) - rootPC%12 + 12) % 12
	for _, iv := range scaleIntervals {
		if iv%12 == pc {
			return true
		}
	}
	return false
}
