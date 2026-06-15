package dawmodel

import "testing"

// quantClip builds a clip with off-grid note starts to quantize.
func quantClip(starts []int) (*Arrangement, []uint64) {
	a := New().AddTrack("d", KindMIDI)
	a.Tracks[0].Clips = []Clip{{Name: "d", Channel: 9}}
	var ids []uint64
	for _, s := range starts {
		var id uint64
		a, id, _ = a.AddNote("d", "d", Note{Start: s, Dur: 60, Pitch: 36, Vel: 100, Ch: 9})
		ids = append(ids, id)
	}
	return a, ids
}

// TestQuantize_hardSnap: strength 1 snaps every start to the nearest grid line.
func TestQuantize_hardSnap(t *testing.T) {
	a, _ := quantClip([]int{5, 118, 122, 245, 359})
	out, err := a.Quantize("d", "d", nil, 120, 1.0, 0.5)
	if err != nil {
		t.Fatalf("quantize: %v", err)
	}
	want := []int{0, 120, 120, 240, 360}
	got := startsOf(out, "d")
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("hard snap[%d] = %d, want %d (all=%v)", i, got[i], want[i], got)
		}
	}
}

// TestQuantize_iterative: strength 0.5 moves halfway toward the grid line.
func TestQuantize_iterative(t *testing.T) {
	a, ids := quantClip([]int{100}) // grid 120: target 120, halfway from 100 -> 110
	out, err := a.Quantize("d", "d", ids, 120, 0.5, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if got := noteByIDIn(out, ids[0]).Start; got != 110 {
		t.Errorf("iterative = %d, want 110", got)
	}
}

// TestQuantize_deterministic: same inputs -> identical result, repeatedly.
func TestQuantize_deterministic(t *testing.T) {
	a, _ := quantClip([]int{5, 118, 247, 365, 481})
	first := startsOf(mustQuant(t, a, 120, 0.8, 0.58), "d")
	for i := 0; i < 5; i++ {
		got := startsOf(mustQuant(t, a, 120, 0.8, 0.58), "d")
		for j := range first {
			if got[j] != first[j] {
				t.Fatalf("run %d differs at %d: %d vs %d", i, j, got[j], first[j])
			}
		}
	}
}

// TestQuantize_swingDelaysOddCells: swing shifts odd grid cells later.
func TestQuantize_swingDelaysOddCells(t *testing.T) {
	a, ids := quantClip([]int{0, 120})                   // cell 0 (even) stays; cell 1 (odd) is delayed
	out, err := a.Quantize("d", "d", ids, 120, 1.0, 0.6) // swingTicks = (0.6-0.5)*2*120 = 24
	if err != nil {
		t.Fatal(err)
	}
	if s := noteByIDIn(out, ids[0]).Start; s != 0 {
		t.Errorf("even cell moved to %d, want 0", s)
	}
	if s := noteByIDIn(out, ids[1]).Start; s != 144 {
		t.Errorf("odd cell = %d, want 144 (120+24 swing)", s)
	}
}

// TestQuantize_badGrid degrades with an error (no panic).
func TestQuantize_badGrid(t *testing.T) {
	a, _ := quantClip([]int{0})
	if _, err := a.Quantize("d", "d", nil, 0, 1, 0.5); err == nil {
		t.Error("quantize grid 0: want error")
	}
}

func mustQuant(t *testing.T, a *Arrangement, grid int, strength, swing float64) *Arrangement {
	t.Helper()
	out, err := a.Quantize("d", "d", nil, grid, strength, swing)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func startsOf(a *Arrangement, clip string) []int {
	var out []int
	for _, t := range a.Tracks {
		for _, c := range t.Clips {
			if c.Name == clip {
				for _, n := range c.Notes {
					out = append(out, n.Start)
				}
			}
		}
	}
	return out
}
