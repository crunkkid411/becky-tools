package dawmodel

import "testing"

// fixture builds a one-clip arrangement with three known notes for edit-op tests.
func fixture() (*Arrangement, []uint64) {
	a := New()
	a.Genre, a.Root, a.Scale, a.BPM = "crunkcore", "A", "minor", 140
	a = a.AddTrack("melody", KindMIDI)
	a.Tracks[0].Clips = []Clip{{Name: "melody", Channel: 0, Program: 81}}
	var ids []uint64
	specs := []Note{
		{Start: 0, Dur: 240, Pitch: 60, Vel: 88, Ch: 0},
		{Start: 240, Dur: 240, Pitch: 64, Vel: 88, Ch: 0},
		{Start: 480, Dur: 240, Pitch: 67, Vel: 88, Ch: 0},
	}
	for _, n := range specs {
		var id uint64
		a, id, _ = a.AddNote("melody", "melody", n)
		ids = append(ids, id)
	}
	return a, ids
}

// TestEditOps_table drives each pure op and asserts the resulting clip state.
func TestEditOps_table(t *testing.T) {
	base, ids := fixture()
	cases := []struct {
		name   string
		apply  func() (*Arrangement, error)
		verify func(*testing.T, *Arrangement)
	}{
		{
			name:  "move shifts time and pitch",
			apply: func() (*Arrangement, error) { return base.MoveNotes("melody", "melody", ids[:1], 120, 2) },
			verify: func(t *testing.T, a *Arrangement) {
				n := noteByIDIn(a, ids[0])
				if n.Start != 120 || n.Pitch != 62 {
					t.Errorf("move: got start=%d pitch=%d, want 120/62", n.Start, n.Pitch)
				}
			},
		},
		{
			name:  "resize lengthens",
			apply: func() (*Arrangement, error) { return base.ResizeNotes("melody", "melody", ids[:1], 240) },
			verify: func(t *testing.T, a *Arrangement) {
				if n := noteByIDIn(a, ids[0]); n.Dur != 480 {
					t.Errorf("resize: dur=%d, want 480", n.Dur)
				}
			},
		},
		{
			name:  "delete removes",
			apply: func() (*Arrangement, error) { return base.DeleteNotes("melody", "melody", ids[:1]) },
			verify: func(t *testing.T, a *Arrangement) {
				if a.NoteCount() != 2 {
					t.Errorf("delete: count=%d, want 2", a.NoteCount())
				}
				if noteByIDIn(a, ids[0]) != nil {
					t.Errorf("delete: note %d still present", ids[0])
				}
			},
		},
		{
			name:  "setvel changes velocity",
			apply: func() (*Arrangement, error) { return base.SetVelocity("melody", "melody", ids, 104) },
			verify: func(t *testing.T, a *Arrangement) {
				for _, id := range ids {
					if n := noteByIDIn(a, id); n.Vel != 104 {
						t.Errorf("setvel: note %d vel=%d, want 104", id, n.Vel)
					}
				}
			},
		},
		{
			name:  "transpose shifts every note",
			apply: func() (*Arrangement, error) { return base.Transpose("melody", "melody", 12) },
			verify: func(t *testing.T, a *Arrangement) {
				if n := noteByIDIn(a, ids[0]); n.Pitch != 72 {
					t.Errorf("transpose: pitch=%d, want 72", n.Pitch)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.apply()
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			c.verify(t, got)
		})
	}
}

// TestEditOps_immutable: an op never mutates the receiver; the original is intact.
func TestEditOps_immutable(t *testing.T) {
	base, ids := fixture()
	before := base.NoteCount()
	beforeVel := noteByIDIn(base, ids[0]).Vel

	if _, err := base.DeleteNotes("melody", "melody", ids); err != nil {
		t.Fatal(err)
	}
	if _, err := base.SetVelocity("melody", "melody", ids, 1); err != nil {
		t.Fatal(err)
	}
	if base.NoteCount() != before {
		t.Errorf("receiver mutated: count %d -> %d", before, base.NoteCount())
	}
	if noteByIDIn(base, ids[0]).Vel != beforeVel {
		t.Errorf("receiver vel mutated to %d, want %d", noteByIDIn(base, ids[0]).Vel, beforeVel)
	}
}

// TestEditOps_unknownClip degrades with an error (no panic) and returns the input.
func TestEditOps_unknownClip(t *testing.T) {
	base, ids := fixture()
	if _, err := base.MoveNotes("nope", "nope", ids, 1, 1); err == nil {
		t.Error("MoveNotes on missing clip: want error")
	}
	if _, _, err := base.AddNote("nope", "nope", Note{Pitch: 60}); err == nil {
		t.Error("AddNote on missing clip: want error")
	}
}

// TestAddNote_clampsAndAllocsID checks defaults/clamps and stable unique IDs.
func TestAddNote_clampsAndAllocsID(t *testing.T) {
	a := New().AddTrack("t", KindMIDI)
	a.Tracks[0].Clips = []Clip{{Name: "c"}}
	a, id1, _ := a.AddNote("t", "c", Note{Start: 0, Dur: 0, Pitch: 200, Vel: 0})
	a, id2, _ := a.AddNote("t", "c", Note{Start: 10, Dur: 5, Pitch: 50, Vel: 200})
	if id1 == id2 || id1 == 0 {
		t.Errorf("IDs not unique/nonzero: %d %d", id1, id2)
	}
	n := noteByIDIn(a, id1)
	if n.Dur != 1 || n.Pitch != 127 || n.Vel != 1 {
		t.Errorf("clamp: got dur=%d pitch=%d vel=%d, want 1/127/1", n.Dur, n.Pitch, n.Vel)
	}
}

// noteByIDIn finds a note across all clips by ID (test helper).
func noteByIDIn(a *Arrangement, id uint64) *Note {
	for ti := range a.Tracks {
		for ci := range a.Tracks[ti].Clips {
			if n := a.Tracks[ti].Clips[ci].noteByID(id); n != nil {
				return n
			}
		}
	}
	return nil
}
