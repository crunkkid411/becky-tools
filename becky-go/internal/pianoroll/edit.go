package pianoroll

import "sort"

// edit.go holds the deterministic, immutable piano-roll edit verbs. Each takes a
// Clip and returns a NEW Clip with the one change applied, leaving the receiver
// untouched. Notes are addressed by their index in the clip's sorted Notes slice
// (the order sortNotes establishes); out-of-range indices are ignored so the UI
// and the AI DSL can pass stale selections without crashing.
//
// These verbs are pure CRUD/geometry. The musical transforms with their own math
// live next door: Quantize (quantize.go) and Humanize (humanize.go).

// Add inserts a note (clamped into MIDI range) and returns the new clip. The clip
// grows to cover the new note. Because notes carry no identity, callers re-derive
// selection indices from the returned clip after an Add.
func (c *Clip) Add(n Note) *Clip {
	notes := append([]Note(nil), c.Notes...)
	notes = append(notes, n.clamp())
	return c.withNotes(notes)
}

// Delete removes the notes at the given indices and returns the new clip. Unknown
// indices are ignored (degrade, never crash).
func (c *Clip) Delete(indices []int) *Clip {
	drop := c.selected(indices)
	if len(drop) == 0 {
		return c.clone()
	}
	kept := make([]Note, 0, len(c.Notes)-len(drop))
	for i, n := range c.Notes {
		if !drop[i] {
			kept = append(kept, n)
		}
	}
	return c.withNotes(kept)
}

// Move shifts the selected notes by dTicks (time) and dPitch (semitones), clamped
// to valid ranges, and returns the new clip. A drag in the UI commits as one Move
// so it is a single undo step. Empty indices => no-op (returns a clone).
func (c *Clip) Move(indices []int, dTicks, dPitch int) *Clip {
	sel := c.selected(indices)
	notes := append([]Note(nil), c.Notes...)
	for i := range notes {
		if !sel[i] {
			continue
		}
		notes[i].Start = maxInt(notes[i].Start+dTicks, 0)
		notes[i].Pitch = clampInt(notes[i].Pitch+dPitch, minPitch, maxPitch)
	}
	return c.withNotes(notes)
}

// Resize changes the length of the selected notes by dTicks (an edge-drag),
// clamped to a minimum of 1 tick, and returns the new clip. Empty indices =>
// no-op.
func (c *Clip) Resize(indices []int, dTicks int) *Clip {
	sel := c.selected(indices)
	notes := append([]Note(nil), c.Notes...)
	for i := range notes {
		if sel[i] {
			notes[i].Length = maxInt(notes[i].Length+dTicks, 1)
		}
	}
	return c.withNotes(notes)
}

// SetLength sets the length of the selected notes to an absolute tick value
// (>=1), and returns the new clip. Useful for "make these all 1/4 notes".
func (c *Clip) SetLength(indices []int, length int) *Clip {
	if length < 1 {
		length = 1
	}
	sel := c.selected(indices)
	notes := append([]Note(nil), c.Notes...)
	for i := range notes {
		if sel[i] {
			notes[i].Length = length
		}
	}
	return c.withNotes(notes)
}

// Transpose shifts EVERY note in the clip by semis (a batch Inspector op), clamped
// to MIDI range, and returns the new clip.
func (c *Clip) Transpose(semis int) *Clip {
	notes := append([]Note(nil), c.Notes...)
	for i := range notes {
		notes[i].Pitch = clampInt(notes[i].Pitch+semis, minPitch, maxPitch)
	}
	return c.withNotes(notes)
}

// TransposeNotes shifts only the selected notes by semis and returns the new clip.
func (c *Clip) TransposeNotes(indices []int, semis int) *Clip {
	sel := c.selected(indices)
	notes := append([]Note(nil), c.Notes...)
	for i := range notes {
		if sel[i] {
			notes[i].Pitch = clampInt(notes[i].Pitch+semis, minPitch, maxPitch)
		}
	}
	return c.withNotes(notes)
}

// SetVelocity sets the velocity of the selected notes (the velocity lane) to an
// absolute value (clamped 1..127) and returns the new clip. Empty indices =>
// every note (a clip-wide velocity set, e.g. "flatten dynamics").
func (c *Clip) SetVelocity(indices []int, vel int) *Clip {
	vel = clampInt(vel, minVel, maxVel)
	all := len(indices) == 0
	sel := c.selected(indices)
	notes := append([]Note(nil), c.Notes...)
	for i := range notes {
		if all || sel[i] {
			notes[i].Velocity = vel
		}
	}
	return c.withNotes(notes)
}

// Legato extends each selected note (or every note when indices is empty) so it
// runs up to the start of the next note on the SAME pitch+channel voice, removing
// the gap between consecutive notes of a line. The last note of a voice keeps its
// length. This is Cubase's "Legato": tie a melodic line together. Returns the new
// clip.
//
// "Next note in the same voice" is found by (Channel, Pitch): within that voice,
// notes are ordered by Start and each note's length becomes nextStart-thisStart
// (>=1). Notes whose selection is off are left untouched but still act as voice
// neighbours (so a selected note can be legato'd up to an unselected one).
func (c *Clip) Legato(indices []int) *Clip {
	all := len(indices) == 0
	sel := c.selected(indices)
	notes := append([]Note(nil), c.Notes...)

	// Group note indices by voice (channel, pitch), each ordered by Start.
	type voiceKey struct{ ch, pitch int }
	voices := map[voiceKey][]int{}
	for i, n := range notes {
		k := voiceKey{n.Channel, n.Pitch}
		voices[k] = append(voices[k], i)
	}
	for _, idxs := range voices {
		sort.SliceStable(idxs, func(a, b int) bool {
			return notes[idxs[a]].Start < notes[idxs[b]].Start
		})
		for pos := 0; pos+1 < len(idxs); pos++ {
			cur := idxs[pos]
			next := idxs[pos+1]
			if !all && !sel[cur] {
				continue
			}
			gap := notes[next].Start - notes[cur].Start
			if gap >= 1 {
				notes[cur].Length = gap
			}
		}
	}
	return c.withNotes(notes)
}

// Split cuts the note at index i into two notes at the absolute tick at. The
// original keeps [Start, at) and a new note covers [at, End). When at is not
// strictly inside the note (at<=Start or at>=End), the clip is returned unchanged
// (a clone) — there is nothing to split. The second piece inherits pitch,
// velocity and channel. Returns the new clip.
func (c *Clip) Split(i, at int) *Clip {
	if !c.inRange(i) {
		return c.clone()
	}
	orig := c.Notes[i]
	if at <= orig.Start || at >= orig.End() {
		return c.clone()
	}
	left := orig
	left.Length = at - orig.Start
	right := orig
	right.Start = at
	right.Length = orig.End() - at

	notes := make([]Note, 0, len(c.Notes)+1)
	notes = append(notes, c.Notes[:i]...)
	notes = append(notes, left, right)
	notes = append(notes, c.Notes[i+1:]...)
	return c.withNotes(notes)
}

// SplitAll cuts EVERY note that straddles the absolute tick at into two pieces at
// that tick, and returns the new clip. A "scissors at the playhead" gesture across
// a selection. Notes that do not straddle at are left untouched.
func (c *Clip) SplitAll(at int) *Clip {
	notes := make([]Note, 0, len(c.Notes)+4)
	for _, n := range c.Notes {
		if at > n.Start && at < n.End() {
			left := n
			left.Length = at - n.Start
			right := n
			right.Start = at
			right.Length = n.End() - at
			notes = append(notes, left, right)
		} else {
			notes = append(notes, n)
		}
	}
	return c.withNotes(notes)
}
