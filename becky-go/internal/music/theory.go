package music

import (
	"math/rand"
	"strings"
)

// MIDI pitch-class numbers for note names (C=0 .. B=11). Sharps and flats both map.
var pcOfName = map[string]int{
	"C": 0, "C#": 1, "DB": 1, "D": 2, "D#": 3, "EB": 3, "E": 4,
	"F": 5, "F#": 6, "GB": 6, "G": 7, "G#": 8, "AB": 8, "A": 9,
	"A#": 10, "BB": 10, "B": 11,
}

// Scale interval sets (semitones from the root). Covers the diatonic modes plus
// the darker/exotic scales common in the hyperpop/digicore family.
var scales = map[string][]int{
	"major":             {0, 2, 4, 5, 7, 9, 11},
	"minor":             {0, 2, 3, 5, 7, 8, 10}, // natural minor / aeolian
	"harmonic_minor":    {0, 2, 3, 5, 7, 8, 11},
	"dorian":            {0, 2, 3, 5, 7, 9, 10},
	"phrygian":          {0, 1, 3, 5, 7, 8, 10},
	"phrygian_dominant": {0, 1, 4, 5, 7, 8, 10},
	"mixolydian":        {0, 2, 4, 5, 7, 9, 10},
	"lydian":            {0, 2, 4, 6, 7, 9, 11},
	"locrian":           {0, 1, 3, 5, 6, 8, 10},
	"minor_pentatonic":  {0, 3, 5, 7, 10},
	"major_pentatonic":  {0, 2, 4, 7, 9},
}

// ParseKey turns "Am", "C", "F#m", "Dmin", "Bb minor", "E phrygian" into a root
// pitch class and a scale name. Bare "Xm"/"min" => natural minor; bare letter =>
// major; an explicit trailing scale word overrides. Defaults to A minor.
func ParseKey(s string) (rootPC int, scale string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 9, "minor"
	}
	fields := strings.Fields(strings.ToLower(s))
	head := fields[0]
	scale = ""
	if len(fields) > 1 {
		if _, ok := scales[fields[1]]; ok {
			scale = fields[1]
		}
	}
	up := strings.ToUpper(head)
	root := ""
	if len(up) >= 2 && (up[1] == '#' || up[1] == 'B') {
		if _, ok := pcOfName[up[:2]]; ok {
			root = up[:2]
		}
	}
	if root == "" && len(up) >= 1 {
		if _, ok := pcOfName[up[:1]]; ok {
			root = up[:1]
		}
	}
	if root == "" {
		return 9, "minor"
	}
	rootPC = pcOfName[root]
	rest := strings.ToLower(head[len(root):])
	if scale == "" {
		switch {
		case strings.HasPrefix(rest, "maj"):
			scale = "major"
		case rest == "m" || strings.HasPrefix(rest, "min"):
			scale = "minor"
		case rest == "":
			scale = "major"
		default:
			if _, ok := scales[rest]; ok {
				scale = rest
			} else {
				scale = "minor"
			}
		}
	}
	return rootPC, scale
}

// ScaleIntervals returns the semitone set for a scale name (minor if unknown).
func ScaleIntervals(scale string) []int {
	if iv, ok := scales[scale]; ok {
		return iv
	}
	return scales["minor"]
}

// ScaleMidi maps a (possibly out-of-range) scale degree to a MIDI note. degree 0
// is the root; degree 7 is the root an octave up; negatives go down. octave is
// the octave of the root (MIDI = (octave+1)*12 + pc), so octave 4 => C4 = 60.
func ScaleMidi(rootPC int, scale []int, degree, octave int) int {
	n := len(scale)
	octShift := floorDiv(degree, n)
	idx := degree - octShift*n
	return (octave+1+octShift)*12 + rootPC + scale[idx]
}

// Triad builds a diatonic triad (or seventh) rooted on a scale degree by stacking
// scale thirds. degreeIndex is 0-based (0 = i/I, 4 = v/V). Returns MIDI notes.
func Triad(rootPC int, scale []int, degreeIndex, octave int, seventh bool) []int {
	notes := []int{
		ScaleMidi(rootPC, scale, degreeIndex, octave),
		ScaleMidi(rootPC, scale, degreeIndex+2, octave),
		ScaleMidi(rootPC, scale, degreeIndex+4, octave),
	}
	if seventh {
		notes = append(notes, ScaleMidi(rootPC, scale, degreeIndex+6, octave))
	}
	return notes
}

// RomanToIndex converts a roman-numeral degree ("i","IV","v","vii") to a 0-based
// scale-degree index. Case is ignored (diatonic quality comes from the scale).
func RomanToIndex(r string) int {
	switch strings.ToUpper(strings.TrimRight(strings.TrimSpace(r), "°o+7969sus24")) {
	case "I":
		return 0
	case "II":
		return 1
	case "III":
		return 2
	case "IV":
		return 3
	case "V":
		return 4
	case "VI":
		return 5
	case "VII":
		return 6
	}
	return 0
}

// Clamp keeps a MIDI note inside a register by shifting whole octaves.
func Clamp(note, lo, hi int) int {
	for note < lo {
		note += 12
	}
	for note > hi {
		note -= 12
	}
	return note
}

func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// Rng is a deterministic PRNG wrapper: seeded identically => identical stream, so
// "humanized" velocities/timing are reproducible. Backed by math/rand's stable
// source.
type Rng struct{ r *rand.Rand }

// NewRng seeds the generator. A given seed always yields the same sequence.
func NewRng(seed int64) *Rng { return &Rng{r: rand.New(rand.NewSource(seed))} }

// Intn returns a deterministic int in [0,n).
func (g *Rng) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	return g.r.Intn(n)
}

// Jitter returns a deterministic int in [-amt, amt].
func (g *Rng) Jitter(amt int) int {
	if amt <= 0 {
		return 0
	}
	return g.r.Intn(2*amt+1) - amt
}

// Chance returns true with probability pct/100 (deterministic).
func (g *Rng) Chance(pct int) bool { return g.r.Intn(100) < pct }
