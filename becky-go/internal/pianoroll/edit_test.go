package pianoroll

import (
	"reflect"
	"testing"
)

// fixtureClip builds a small 3-note melody for the edit tests: C4, E4, G4 on
// channel 0, each a 1/4 note at 480 PPQ (start 0/480/960, length 240).
func fixtureClip() *Clip {
	c := NewClip(480)
	c = c.Add(Note{Pitch: 60, Start: 0, Length: 240, Velocity: 100, Channel: 0})
	c = c.Add(Note{Pitch: 64, Start: 480, Length: 240, Velocity: 90, Channel: 0})
	c = c.Add(Note{Pitch: 67, Start: 960, Length: 240, Velocity: 110, Channel: 0})
	return c
}

// TestAdd_clampsAndGrows checks Add clamps out-of-range fields and grows Length.
func TestAdd_clampsAndGrows(t *testing.T) {
	c := NewClip(480)
	c = c.Add(Note{Pitch: 200, Start: -50, Length: 0, Velocity: 999, Channel: 99})
	if c.NoteCount() != 1 {
		t.Fatalf("note count = %d, want 1", c.NoteCount())
	}
	got := c.Notes[0]
	want := Note{Pitch: 127, Start: 0, Length: 1, Velocity: 127, Channel: 15}
	if got != want {
		t.Errorf("clamped note = %+v, want %+v", got, want)
	}
	if c.Length != 1 {
		t.Errorf("clip length = %d, want 1 (grown to note end)", c.Length)
	}
}

// TestEditOps_table drives the index-addressed edit verbs through table cases.
func TestEditOps_table(t *testing.T) {
	tests := []struct {
		name  string
		apply func(*Clip) *Clip
		check func(*testing.T, *Clip)
	}{
		{
			name:  "move time+pitch",
			apply: func(c *Clip) *Clip { return c.Move([]int{0}, 120, 2) },
			check: func(t *testing.T, c *Clip) {
				// C4@0 -> moves to start 120, pitch 62; re-sorts so it may shift index.
				n := findNote(c, 62)
				if n == nil || n.Start != 120 {
					t.Errorf("moved note = %+v, want pitch 62 start 120", n)
				}
			},
		},
		{
			name:  "resize longer",
			apply: func(c *Clip) *Clip { return c.Resize([]int{0}, 240) },
			check: func(t *testing.T, c *Clip) {
				if c.Notes[0].Length != 480 {
					t.Errorf("length = %d, want 480", c.Notes[0].Length)
				}
			},
		},
		{
			name:  "resize floors at 1",
			apply: func(c *Clip) *Clip { return c.Resize([]int{0}, -10000) },
			check: func(t *testing.T, c *Clip) {
				if c.Notes[0].Length != 1 {
					t.Errorf("length = %d, want 1 (floored)", c.Notes[0].Length)
				}
			},
		},
		{
			name:  "set absolute length",
			apply: func(c *Clip) *Clip { return c.SetLength([]int{0, 1, 2}, 120) },
			check: func(t *testing.T, c *Clip) {
				for _, n := range c.Notes {
					if n.Length != 120 {
						t.Errorf("note %+v length != 120", n)
					}
				}
			},
		},
		{
			name:  "delete middle",
			apply: func(c *Clip) *Clip { return c.Delete([]int{1}) },
			check: func(t *testing.T, c *Clip) {
				if c.NoteCount() != 2 {
					t.Fatalf("count = %d, want 2", c.NoteCount())
				}
				if findNote(c, 64) != nil {
					t.Error("E4 should be deleted")
				}
			},
		},
		{
			name:  "transpose all up an octave",
			apply: func(c *Clip) *Clip { return c.Transpose(12) },
			check: func(t *testing.T, c *Clip) {
				want := []int{72, 76, 79}
				for i, p := range want {
					if c.Notes[i].Pitch != p {
						t.Errorf("note %d pitch = %d, want %d", i, c.Notes[i].Pitch, p)
					}
				}
			},
		},
		{
			name:  "transpose selected only",
			apply: func(c *Clip) *Clip { return c.TransposeNotes([]int{0}, 12) },
			check: func(t *testing.T, c *Clip) {
				if findNote(c, 72) == nil {
					t.Error("C4 should become C5 (72)")
				}
				if findNote(c, 64) == nil || findNote(c, 67) == nil {
					t.Error("other notes should be unchanged")
				}
			},
		},
		{
			name:  "set velocity selected",
			apply: func(c *Clip) *Clip { return c.SetVelocity([]int{0}, 40) },
			check: func(t *testing.T, c *Clip) {
				if c.Notes[0].Velocity != 40 {
					t.Errorf("vel = %d, want 40", c.Notes[0].Velocity)
				}
				if c.Notes[1].Velocity == 40 {
					t.Error("note 1 velocity should be unchanged")
				}
			},
		},
		{
			name:  "set velocity all (empty selection)",
			apply: func(c *Clip) *Clip { return c.SetVelocity(nil, 77) },
			check: func(t *testing.T, c *Clip) {
				for _, n := range c.Notes {
					if n.Velocity != 77 {
						t.Errorf("note %+v vel != 77", n)
					}
				}
			},
		},
		{
			name:  "transpose clamps at MIDI ceiling",
			apply: func(c *Clip) *Clip { return c.Transpose(1000) },
			check: func(t *testing.T, c *Clip) {
				for _, n := range c.Notes {
					if n.Pitch != 127 {
						t.Errorf("pitch = %d, want clamped 127", n.Pitch)
					}
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := fixtureClip()
			got := tc.apply(base)
			tc.check(t, got)
		})
	}
}

// TestEditOps_immutable proves the receiver is never mutated by an edit op.
func TestEditOps_immutable(t *testing.T) {
	base := fixtureClip()
	snapshot := append([]Note(nil), base.Notes...)
	baseLen := base.Length

	_ = base.Move([]int{0}, 999, 5)
	_ = base.Delete([]int{0, 1, 2})
	_ = base.Resize([]int{0}, 100)
	_ = base.Transpose(7)
	_ = base.SetVelocity(nil, 1)
	_ = base.Legato(nil)
	_ = base.Split(0, 120)

	if !reflect.DeepEqual(base.Notes, snapshot) {
		t.Errorf("receiver notes mutated:\n got %+v\nwant %+v", base.Notes, snapshot)
	}
	if base.Length != baseLen {
		t.Errorf("receiver length mutated: %d -> %d", baseLen, base.Length)
	}
}

// TestEditOps_outOfRangeSafe checks unknown indices degrade (no panic, no change).
func TestEditOps_outOfRangeSafe(t *testing.T) {
	base := fixtureClip()
	ops := map[string]*Clip{
		"delete": base.Delete([]int{99, -1}),
		"move":   base.Move([]int{99}, 10, 1),
		"resize": base.Resize([]int{-5}, 10),
		"split":  base.Split(42, 100),
	}
	for name, got := range ops {
		if got.NoteCount() != base.NoteCount() {
			t.Errorf("%s: count changed on out-of-range index (%d -> %d)", name, base.NoteCount(), got.NoteCount())
		}
	}
}

// TestLegato_closesGapsPerVoice checks Legato extends each note to the next note of
// the SAME pitch+channel voice, and leaves the last note of a voice alone.
func TestLegato_closesGapsPerVoice(t *testing.T) {
	// Two notes on pitch 60 ch0 with a gap, plus an unrelated pitch-64 note.
	c := NewClip(480)
	c = c.Add(Note{Pitch: 60, Start: 0, Length: 100, Velocity: 100, Channel: 0})   // gap to 480
	c = c.Add(Note{Pitch: 60, Start: 480, Length: 100, Velocity: 100, Channel: 0}) // last of voice
	c = c.Add(Note{Pitch: 64, Start: 0, Length: 100, Velocity: 100, Channel: 0})   // other voice
	out := c.Legato(nil)

	first := findNoteAt(out, 60, 0)
	if first == nil || first.Length != 480 {
		t.Errorf("first pitch-60 note length = %v, want 480 (extended to next)", first)
	}
	last := findNoteAt(out, 60, 480)
	if last == nil || last.Length != 100 {
		t.Errorf("last pitch-60 note length = %v, want 100 (unchanged)", last)
	}
	other := findNoteAt(out, 64, 0)
	if other == nil || other.Length != 100 {
		t.Errorf("pitch-64 note length = %v, want 100 (single-note voice unchanged)", other)
	}
}

// TestSplit_cutsInsideOnly checks Split bisects a note and rejects out-of-bounds.
func TestSplit_cutsInsideOnly(t *testing.T) {
	base := fixtureClip() // C4@0 len240, E4@480, G4@960

	// Split the first note (C4, [0,240)) at 100 -> [0,100)+[100,240).
	out := base.Split(0, 100)
	if out.NoteCount() != 4 {
		t.Fatalf("count after split = %d, want 4", out.NoteCount())
	}
	left := findNoteAt(out, 60, 0)
	right := findNoteAt(out, 60, 100)
	if left == nil || left.Length != 100 {
		t.Errorf("left piece = %v, want start 0 len 100", left)
	}
	if right == nil || right.Length != 140 {
		t.Errorf("right piece = %v, want start 100 len 140", right)
	}
	// Both pieces preserve velocity/channel.
	if right.Velocity != 100 || right.Channel != 0 {
		t.Errorf("right piece lost vel/channel: %+v", right)
	}

	// Split exactly on an edge is a no-op.
	if got := base.Split(0, 0); got.NoteCount() != base.NoteCount() {
		t.Error("split at note start should be a no-op")
	}
	if got := base.Split(0, 240); got.NoteCount() != base.NoteCount() {
		t.Error("split at note end should be a no-op")
	}
}

// TestSplitAll_straddlingOnly checks SplitAll cuts only notes crossing the tick.
func TestSplitAll_straddlingOnly(t *testing.T) {
	base := fixtureClip() // C4[0,240), E4[480,720), G4[960,1200)
	out := base.SplitAll(120)
	// Only C4 straddles 120; E4 and G4 start after it. Count grows by exactly 1.
	if out.NoteCount() != base.NoteCount()+1 {
		t.Fatalf("count = %d, want %d (only C4 split)", out.NoteCount(), base.NoteCount()+1)
	}
	if findNoteAt(out, 60, 0) == nil || findNoteAt(out, 60, 120) == nil {
		t.Error("C4 should be cut into [0,120)+[120,240)")
	}
}

// --- helpers ---

func findNote(c *Clip, pitch int) *Note {
	for i := range c.Notes {
		if c.Notes[i].Pitch == pitch {
			return &c.Notes[i]
		}
	}
	return nil
}

func findNoteAt(c *Clip, pitch, start int) *Note {
	for i := range c.Notes {
		if c.Notes[i].Pitch == pitch && c.Notes[i].Start == start {
			return &c.Notes[i]
		}
	}
	return nil
}
