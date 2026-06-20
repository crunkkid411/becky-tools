// Package pianoroll is becky-canvas's piano-roll engine: a small, self-contained
// MIDI clip + note model and a set of PURE, immutable edit operations that the
// piano-roll UI (and the AI command DSL) drive. It is the "logic only" layer —
// no Gio, no audio, no cgo — that a later integration pass wires to the window.
//
// Why a NEW package rather than reusing internal/dawmodel? dawmodel is the
// whole-SESSION arrangement (tracks, clips-with-offset, a mixer, stable note IDs
// addressed by (trackID, clipName)). This package is the single editable CLIP at
// the centre of the piano roll, with the index-addressed note edits a roll needs
// and the verbs dawmodel does not have (humanize, legato, split). For .mid import
// and export it REUSES internal/music's Standard MIDI File reader+writer — the SMF
// codec is not reinvented here.
//
// Invariants (becky house rules):
//   - Immutable: every edit op returns a NEW Clip; the receiver is never mutated.
//   - Deterministic: same input -> same output. Notes are kept in a stable
//     (Start, Pitch, Velocity, Channel) order so output never depends on map
//     iteration, and the ONE randomized op (Humanize) draws from a seeded
//     math/rand source so a given seed reproduces byte-for-byte.
//   - Degrade-never-crash: out-of-range indices/values are clamped or ignored;
//     no edit op panics; malformed .mid returns a wrapped error plus any partial
//     clip decoded so far.
package pianoroll

import "sort"

// DefaultPPQ is the ticks-per-quarter resolution a new clip uses when none is
// given. It matches internal/music.PPQ (480) so clips round-trip through the
// shared SMF writer at the same resolution the rest of becky-canvas uses.
const DefaultPPQ = 480

// pitch/velocity/channel bounds for MIDI.
const (
	minPitch   = 0
	maxPitch   = 127
	minVel     = 1 // a real note-on; velocity 0 is a note-off in MIDI
	maxVel     = 127
	minChannel = 0
	maxChannel = 15
)

// Note is one editable note in the piano roll: the visual blob whose rectangle is
// (Start, Pitch) x (Length, 1 semitone). It is a plain value (no identity) — the
// piano roll and the edit ops address notes by their index within a Clip's sorted
// Notes slice. Fields mirror what a MIDI note carries.
type Note struct {
	Pitch    int `json:"pitch"`    // MIDI note number, 0..127 (60 = middle C)
	Start    int `json:"start"`    // absolute start in ticks from clip start (>=0)
	Length   int `json:"length"`   // duration in ticks (>=1)
	Velocity int `json:"velocity"` // note-on velocity, 1..127
	Channel  int `json:"channel"`  // MIDI channel, 0..15 (9 = GM percussion)
}

// End returns the tick at which the note stops sounding (Start+Length).
func (n Note) End() int { return n.Start + n.Length }

// clamp returns a copy of the note with every field forced into MIDI range and a
// positive length. It is the single normalization point for note input.
func (n Note) clamp() Note {
	n.Pitch = clampInt(n.Pitch, minPitch, maxPitch)
	n.Velocity = clampInt(n.Velocity, minVel, maxVel)
	n.Channel = clampInt(n.Channel, minChannel, maxChannel)
	if n.Start < 0 {
		n.Start = 0
	}
	if n.Length < 1 {
		n.Length = 1
	}
	return n
}

// Clip is an editable MIDI region: a set of Notes plus a Length (in ticks) and a
// PPQ resolution. It is treated as immutable — edit operations return a new Clip
// (a deep copy with the one change applied). Notes are kept sorted so hit-testing
// is cheap and serialization is deterministic.
type Clip struct {
	Name   string `json:"name,omitempty"`
	PPQ    int    `json:"ppq"`    // ticks per quarter note (resolution)
	Length int    `json:"length"` // clip length in ticks
	Notes  []Note `json:"notes,omitempty"`
}

// NewClip returns an empty clip at the given PPQ (DefaultPPQ when ppq<=0).
func NewClip(ppq int) *Clip {
	if ppq <= 0 {
		ppq = DefaultPPQ
	}
	return &Clip{PPQ: ppq}
}

// NoteCount returns how many notes the clip holds (a quick sanity probe used by
// the round-trip verifier).
func (c *Clip) NoteCount() int { return len(c.Notes) }

// clone returns a deep copy so edit ops never mutate the caller's clip. This is
// the immutability boundary.
func (c *Clip) clone() *Clip {
	out := *c
	out.Notes = append([]Note(nil), c.Notes...)
	return &out
}

// sortNotes keeps notes in deterministic (Start, Pitch, Velocity, Channel) order.
// A pure value model has no IDs, so all four fields tie-break to a total order.
func sortNotes(notes []Note) {
	sort.SliceStable(notes, func(i, j int) bool {
		if notes[i].Start != notes[j].Start {
			return notes[i].Start < notes[j].Start
		}
		if notes[i].Pitch != notes[j].Pitch {
			return notes[i].Pitch < notes[j].Pitch
		}
		if notes[i].Velocity != notes[j].Velocity {
			return notes[i].Velocity < notes[j].Velocity
		}
		return notes[i].Channel < notes[j].Channel
	})
}

// withNotes returns a clone whose Notes are replaced with the given slice, sorted,
// and whose Length is grown to cover them. Central helper for ops that rebuild the
// note set. The input slice is taken over (callers pass a fresh slice).
func (c *Clip) withNotes(notes []Note) *Clip {
	out := c.clone()
	sortNotes(notes)
	out.Notes = notes
	out.growToFit()
	return out
}

// growToFit extends Length so it is at least the end of the last note. It never
// shrinks an explicitly-set Length (a clip may be intentionally longer than its
// notes, e.g. a bar of silence at the end).
func (c *Clip) growToFit() {
	for _, n := range c.Notes {
		if e := n.End(); e > c.Length {
			c.Length = e
		}
	}
}

// inRange reports whether i is a valid index into the clip's notes.
func (c *Clip) inRange(i int) bool { return i >= 0 && i < len(c.Notes) }

// selected returns the subset of valid, de-duplicated indices as a set.
// Out-of-range indices are silently dropped (degrade, never crash).
func (c *Clip) selected(indices []int) map[int]bool {
	sel := make(map[int]bool, len(indices))
	for _, i := range indices {
		if c.inRange(i) {
			sel[i] = true
		}
	}
	return sel
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
