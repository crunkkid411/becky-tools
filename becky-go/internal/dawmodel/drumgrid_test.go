package dawmodel

import "testing"

// drumClip builds a one-bar drum clip: kick on steps 0 and 4, snare on step 2,
// at a 1/16 step (120 ticks). It mimics a becky-compose drum stem.
func drumClip() *Arrangement {
	a := New().AddTrack("drums", KindMIDI)
	a.Tracks[0].Clips = []Clip{{Name: "drums", Channel: 9}}
	hits := []Note{
		{Start: 0, Dur: 60, Pitch: 36, Vel: 118, Ch: 9},   // kick step 0
		{Start: 240, Dur: 60, Pitch: 38, Vel: 104, Ch: 9}, // snare step 2
		{Start: 480, Dur: 60, Pitch: 36, Vel: 118, Ch: 9}, // kick step 4
	}
	for _, n := range hits {
		a, _, _ = a.AddNote("drums", "drums", n)
	}
	return a
}

// TestDrumGridOf_derivesLanesAndSteps: notes derive into kick/snare lanes with the
// right cells lit.
func TestDrumGridOf_derivesLanesAndSteps(t *testing.T) {
	g, err := drumClip().DrumGridOf("drums", "drums", 120)
	if err != nil {
		t.Fatalf("DrumGridOf: %v", err)
	}
	if g.Steps != 16 || g.Bars != 1 || g.StepTicks != 120 {
		t.Errorf("grid dims = %d steps x %d bars step %d, want 16x1 step 120", g.Steps, g.Bars, g.StepTicks)
	}
	kick := laneByName(g, "kick")
	snare := laneByName(g, "snare")
	if kick == nil || snare == nil {
		t.Fatalf("missing lanes; got %v", laneNames(g))
	}
	if !kick.On[0] || !kick.On[4] || kick.On[2] {
		t.Errorf("kick lane = %v, want steps 0 and 4 only", kick.On)
	}
	if !snare.On[2] || snare.On[0] {
		t.Errorf("snare lane = %v, want step 2", snare.On)
	}
	if kick.Vel[0] != 118 {
		t.Errorf("kick vel[0] = %d, want 118", kick.Vel[0])
	}
}

// TestDrumGrid_roundTripNotes: grid -> Compile -> derive again is identical (the
// model<->grid two-way path is lossless on the 16-step lattice).
func TestDrumGrid_roundTripNotes(t *testing.T) {
	a := drumClip()
	g1, err := a.DrumGridOf("drums", "drums", 120)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := a.ApplyDrumGrid("drums", "drums", g1)
	if err != nil {
		t.Fatal(err)
	}
	g2, err := applied.DrumGridOf("drums", "drums", 120)
	if err != nil {
		t.Fatal(err)
	}
	if !gridsEqual(g1, g2) {
		t.Errorf("grid changed across round-trip\n g1=%v\n g2=%v", laneCells(g1), laneCells(g2))
	}
}

// TestDrumGrid_setStepImmutable: SetStep returns a new grid; the original is intact.
func TestDrumGrid_setStepImmutable(t *testing.T) {
	g, err := drumClip().DrumGridOf("drums", "drums", 120)
	if err != nil {
		t.Fatal(err)
	}
	kickIdx := laneIndex(g, "kick")
	before := g.Lanes[kickIdx].On[8]
	g2 := g.SetStep(kickIdx, 8, true, 100)
	if g.Lanes[kickIdx].On[8] != before {
		t.Error("SetStep mutated the original grid")
	}
	if !g2.Lanes[kickIdx].On[8] || g2.Lanes[kickIdx].Vel[8] != 100 {
		t.Error("SetStep did not set the cell on the new grid")
	}
}

// TestDrumGrid_compileThenWriteRoundTrips: an applied grid still round-trips through
// the byte-stable SMF writer (the becky determinism tripwire for drums).
func TestDrumGrid_compileThenWriteRoundTrips(t *testing.T) {
	a := drumClip()
	g, _ := a.DrumGridOf("drums", "drums", 120)
	g = g.SetStep(laneIndex(g, "kick"), 8, true, 110)
	applied, err := a.ApplyDrumGrid("drums", "drums", g)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := FromSMF(applied.ToSMF())
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if reparsed.NoteCount() != applied.NoteCount() {
		t.Errorf("note count after write/parse = %d, want %d", reparsed.NoteCount(), applied.NoteCount())
	}
}

// TestDrumGridOf_unknownClip degrades with an error (no panic).
func TestDrumGridOf_unknownClip(t *testing.T) {
	if _, err := New().DrumGridOf("x", "y", 120); err == nil {
		t.Error("DrumGridOf missing clip: want error")
	}
}

// ---- helpers ----

func laneByName(g *DrumGrid, name string) *Lane {
	for i := range g.Lanes {
		if g.Lanes[i].Name == name {
			return &g.Lanes[i]
		}
	}
	return nil
}

func laneIndex(g *DrumGrid, name string) int {
	for i := range g.Lanes {
		if g.Lanes[i].Name == name {
			return i
		}
	}
	return -1
}

func laneNames(g *DrumGrid) []string {
	out := make([]string, 0, len(g.Lanes))
	for _, ln := range g.Lanes {
		out = append(out, ln.Name)
	}
	return out
}

func laneCells(g *DrumGrid) map[string][]bool {
	out := map[string][]bool{}
	for _, ln := range g.Lanes {
		out[ln.Name] = ln.On
	}
	return out
}

func gridsEqual(a, b *DrumGrid) bool {
	if len(a.Lanes) != len(b.Lanes) {
		return false
	}
	for i := range a.Lanes {
		if a.Lanes[i].Note != b.Lanes[i].Note || len(a.Lanes[i].On) != len(b.Lanes[i].On) {
			return false
		}
		for j := range a.Lanes[i].On {
			if a.Lanes[i].On[j] != b.Lanes[i].On[j] {
				return false
			}
		}
	}
	return true
}
