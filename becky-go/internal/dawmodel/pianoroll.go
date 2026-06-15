package dawmodel

import "fmt"

// pianoroll.go holds the piano-roll edit verbs. Each is PURE: it takes an
// Arrangement and returns a NEW Arrangement with the one change applied (the
// becky immutability rule), leaving the receiver untouched. Edits are addressed
// by (trackID, clipName) + note IDs, so the becky-canvas piano roll and the AI
// command DSL drive the SAME ops. Every op that overrides an auto value records a
// Correction (corrections.go) — the preference-learning substrate.

// AddNote inserts a new note into a clip and returns the new arrangement and the
// new note's stable ID. The note is the editable "blob" the piano roll draws.
func (a *Arrangement) AddNote(trackID, clipName string, n Note) (*Arrangement, uint64, error) {
	out := a.clone()
	_, c := out.findClip(trackID, clipName)
	if c == nil {
		return a, 0, fmt.Errorf("add note: clip %q/%q not found", trackID, clipName)
	}
	n.ID = out.allocID()
	n.Dur = maxInt(n.Dur, 1)
	n.Vel = clampVel(n.Vel)
	n.Pitch = clampPitch(n.Pitch)
	c.Notes = append(c.Notes, n)
	sortNotes(c.Notes)
	return out, n.ID, nil
}

// DeleteNotes removes the given note IDs from a clip. Unknown IDs are ignored
// (degrade, never crash). Returns the new arrangement.
func (a *Arrangement) DeleteNotes(trackID, clipName string, ids []uint64) (*Arrangement, error) {
	out := a.clone()
	_, c := out.findClip(trackID, clipName)
	if c == nil {
		return a, fmt.Errorf("delete: clip %q/%q not found", trackID, clipName)
	}
	drop := idSet(ids)
	kept := make([]Note, 0, len(c.Notes))
	for _, n := range c.Notes {
		if !drop[n.ID] {
			kept = append(kept, n)
		}
	}
	c.Notes = kept
	return out, nil
}

// MoveNotes shifts the given notes by dTicks (time) and dPitch (semitones),
// clamped to valid ranges. Returns the new arrangement. A drag in the UI commits
// as one MoveNotes so it is a single undo step.
func (a *Arrangement) MoveNotes(trackID, clipName string, ids []uint64, dTicks, dPitch int) (*Arrangement, error) {
	out := a.clone()
	_, c := out.findClip(trackID, clipName)
	if c == nil {
		return a, fmt.Errorf("move: clip %q/%q not found", trackID, clipName)
	}
	sel := idSet(ids)
	for i := range c.Notes {
		if !sel[c.Notes[i].ID] {
			continue
		}
		c.Notes[i].Start = maxInt(c.Notes[i].Start+dTicks, 0)
		c.Notes[i].Pitch = clampPitch(c.Notes[i].Pitch + dPitch)
	}
	sortNotes(c.Notes)
	return out, nil
}

// ResizeNotes changes the length of the given notes by dDur (edge-drag), clamped
// to a minimum of 1 tick. Returns the new arrangement.
func (a *Arrangement) ResizeNotes(trackID, clipName string, ids []uint64, dDur int) (*Arrangement, error) {
	out := a.clone()
	_, c := out.findClip(trackID, clipName)
	if c == nil {
		return a, fmt.Errorf("resize: clip %q/%q not found", trackID, clipName)
	}
	sel := idSet(ids)
	for i := range c.Notes {
		if sel[c.Notes[i].ID] {
			c.Notes[i].Dur = maxInt(c.Notes[i].Dur+dDur, 1)
		}
	}
	return out, nil
}

// SetVelocity sets the velocity of the given notes (the velocity lane). When the
// note carried an auto-generated velocity, the override is logged as a Correction
// (Jordan's taste signal) keyed to the note's context. Returns the new arrangement.
func (a *Arrangement) SetVelocity(trackID, clipName string, ids []uint64, vel int) (*Arrangement, error) {
	out := a.clone()
	_, c := out.findClip(trackID, clipName)
	if c == nil {
		return a, fmt.Errorf("set velocity: clip %q/%q not found", trackID, clipName)
	}
	vel = clampVel(vel)
	sel := idSet(ids)
	for i := range c.Notes {
		if !sel[c.Notes[i].ID] {
			continue
		}
		old := c.Notes[i].Vel
		if old != vel {
			out.logCorrection("velocity", clipName, c.Notes[i].Start,
				fmt.Sprintf("%d", old), fmt.Sprintf("%d", vel))
		}
		c.Notes[i].Vel = vel
	}
	return out, nil
}

// Transpose shifts every note in a clip by semis (a batch Inspector op). Returns
// the new arrangement.
func (a *Arrangement) Transpose(trackID, clipName string, semis int) (*Arrangement, error) {
	out := a.clone()
	_, c := out.findClip(trackID, clipName)
	if c == nil {
		return a, fmt.Errorf("transpose: clip %q/%q not found", trackID, clipName)
	}
	for i := range c.Notes {
		c.Notes[i].Pitch = clampPitch(c.Notes[i].Pitch + semis)
	}
	sortNotes(c.Notes)
	return out, nil
}

// idSet builds a lookup set from a slice of note IDs.
func idSet(ids []uint64) map[uint64]bool {
	m := make(map[uint64]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func clampPitch(p int) int {
	if p < 0 {
		return 0
	}
	if p > 127 {
		return 127
	}
	return p
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
