package pianoroll

import (
	"testing"

	"becky-go/internal/dawmodel"
)

// quantClip builds a single-channel clip whose notes start at the given ticks
// (each a short 60-tick note), at 480 PPQ. The clip is named so that a dawmodel
// import (FromSMF) yields a track+clip with a known name for the cross-check.
func quantClip(starts []int) *Clip {
	c := NewClip(480)
	c.Name = "q"
	for _, s := range starts {
		c = c.Add(Note{Pitch: 60, Start: s, Length: 60, Velocity: 100, Channel: 0})
	}
	return c
}

// TestQuantize_hardSnap: strength 1 snaps every start to the nearest grid line.
func TestQuantize_hardSnap(t *testing.T) {
	// grid 120 (a 1/16 at 480 PPQ); 5,118,250 -> 0,120,240.
	c := quantClip([]int{5, 118, 250})
	out := c.Quantize(nil, 120, 1.0, 0.5)
	want := []int{0, 120, 240}
	got := startsOf(out)
	if !equalInts(got, want) {
		t.Errorf("hard-snap starts = %v, want %v", got, want)
	}
}

// TestQuantize_iterative: strength 0.5 moves halfway toward the grid line.
func TestQuantize_iterative(t *testing.T) {
	c := quantClip([]int{40}) // nearest grid line at 0; halfway: 40 + 0.5*(0-40) = 20.
	out := c.Quantize(nil, 120, 0.5, 0.5)
	if got := out.Notes[0].Start; got != 20 {
		t.Errorf("iterative start = %d, want 20", got)
	}
}

// TestQuantize_swingDelaysOddCells: swing shifts odd grid cells later.
func TestQuantize_swingDelaysOddCells(t *testing.T) {
	// swing 0.6 => swingTicks = (0.6-0.5)*2*120 = 24. Cell 1 (start ~120) -> 144.
	c := quantClip([]int{0, 118})
	out := c.Quantize(nil, 120, 1.0, 0.6)
	want := []int{0, 144}
	got := startsOf(out)
	if !equalInts(got, want) {
		t.Errorf("swung starts = %v, want %v", got, want)
	}
}

// TestQuantize_selectedOnly: only the selected note moves.
func TestQuantize_selectedOnly(t *testing.T) {
	c := quantClip([]int{5, 130})
	out := c.Quantize([]int{0}, 120, 1.0, 0.5) // index 0 is the 5-tick note
	if out.Notes[0].Start != 0 {
		t.Errorf("selected note start = %d, want 0", out.Notes[0].Start)
	}
	if out.Notes[1].Start != 130 {
		t.Errorf("unselected note start = %d, want 130 (unchanged)", out.Notes[1].Start)
	}
}

// TestQuantize_badGridNoop: grid<=0 degrades to a no-op (no panic, no change).
func TestQuantize_badGridNoop(t *testing.T) {
	c := quantClip([]int{5, 130})
	out := c.Quantize(nil, 0, 1.0, 0.5)
	if !equalInts(startsOf(out), []int{5, 130}) {
		t.Error("grid 0 should leave starts unchanged")
	}
}

// TestQuantize_deterministic: same inputs -> identical result, repeatedly.
func TestQuantize_deterministic(t *testing.T) {
	a := quantClip([]int{3, 119, 250, 491}).Quantize(nil, 120, 0.7, 0.55)
	b := quantClip([]int{3, 119, 250, 491}).Quantize(nil, 120, 0.7, 0.55)
	if !equalInts(startsOf(a), startsOf(b)) {
		t.Errorf("non-deterministic: %v vs %v", startsOf(a), startsOf(b))
	}
}

// TestQuantizeEnds_snapsLengths: the END snaps to grid, start stays put.
func TestQuantizeEnds_snapsLengths(t *testing.T) {
	c := NewClip(480)
	c = c.Add(Note{Pitch: 60, Start: 0, Length: 110, Velocity: 100}) // end 110 -> 120
	out := c.QuantizeEnds(nil, 120, 1.0)
	if out.Notes[0].Start != 0 {
		t.Errorf("start moved to %d, want 0", out.Notes[0].Start)
	}
	if out.Notes[0].Length != 120 {
		t.Errorf("length = %d, want 120 (end snapped 110->120)", out.Notes[0].Length)
	}
}

// TestQuantize_matchesDawmodel cross-checks that this package's quantizer produces
// the SAME note starts as internal/dawmodel.Quantize (the existing one the drum
// grid uses), proving the swing/strength math was reused, not re-derived
// differently. The dawmodel clip is built by exporting the pianoroll clip to .mid
// and importing it via dawmodel.FromSMF (so both start from identical notes).
func TestQuantize_matchesDawmodel(t *testing.T) {
	grid := 120
	starts := []int{5, 37, 118, 121, 200, 250, 359, 491}
	cases := []struct {
		strength, swing float64
	}{
		{1.0, 0.5},
		{0.5, 0.5},
		{1.0, 0.6},
		{0.75, 0.66},
		{0.3, 0.5},
	}
	for _, cs := range cases {
		base := quantClip(starts)

		// pianoroll result.
		prStarts := startsOf(base.Quantize(nil, grid, cs.strength, cs.swing))

		// dawmodel result on the SAME notes (imported from this clip's .mid).
		dm, err := dawmodel.FromSMF(base.MIDIBytes(ExportOpts{}))
		if err != nil {
			t.Fatalf("dawmodel.FromSMF error: %v", err)
		}
		if len(dm.Tracks) == 0 {
			t.Fatal("dawmodel import produced no tracks")
		}
		tID := dm.Tracks[0].ID
		cName := dm.Tracks[0].Clips[0].Name
		dmOut, err := dm.Quantize(tID, cName, nil, grid, cs.strength, cs.swing)
		if err != nil {
			t.Fatalf("dawmodel.Quantize error: %v", err)
		}
		dmStarts := dawStartsOf(dmOut, tID, cName)

		if !equalInts(prStarts, dmStarts) {
			t.Errorf("strength=%.2f swing=%.2f: pianoroll %v != dawmodel %v",
				cs.strength, cs.swing, prStarts, dmStarts)
		}
	}
}

// --- helpers ---

func startsOf(c *Clip) []int {
	out := make([]int, len(c.Notes))
	for i, n := range c.Notes {
		out[i] = n.Start
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// dawStartsOf returns the sorted note starts of a dawmodel clip.
func dawStartsOf(a *dawmodel.Arrangement, trackID, clipName string) []int {
	tr, ok := a.TrackByID(trackID)
	if !ok {
		return nil
	}
	for _, c := range tr.Clips {
		if c.Name == clipName {
			out := make([]int, len(c.Notes))
			for i, n := range c.Notes {
				out[i] = n.Start
			}
			return out
		}
	}
	return nil
}
