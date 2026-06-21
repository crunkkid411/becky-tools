package beatgen

import (
	"testing"

	"becky-go/internal/dawmodel"
)

func TestFromDrumGrid(t *testing.T) {
	g := &dawmodel.DrumGrid{
		Steps: 16, Bars: 1, Channel: 9,
		Lanes: []dawmodel.Lane{
			{Name: "kick", Note: 36, On: bools("x...x...x...x..."), Vel: velFor("x...x...x...x...", 110)},
			{Name: "hat", Note: 42, On: bools("x.x.x.x.x.x.x.x."), Vel: velFor("x.x.x.x.x.x.x.x.", 80)},
		},
	}
	p := FromDrumGrid(g)
	if p.Steps != 16 {
		t.Fatalf("Steps = %d, want 16", p.Steps)
	}
	if len(p.Lanes) != 2 {
		t.Fatalf("lanes = %d, want 2", len(p.Lanes))
	}
	if p.Lanes[0].Role != "kick" || p.Lanes[1].Role != "hat" {
		t.Errorf("roles wrong: %q %q", p.Lanes[0].Role, p.Lanes[1].Role)
	}
	if !p.Lanes[0].Steps[0].On || p.Lanes[0].Steps[0].Velocity != 110 {
		t.Error("kick step 0 not lifted with velocity")
	}
	if p.Lanes[0].Steps[1].On {
		t.Error("kick step 1 should be off")
	}
	// nil grid degrades
	if FromDrumGrid(nil) == nil {
		t.Error("FromDrumGrid(nil) should return an empty pattern, not nil")
	}
}

func TestToDrumGrid(t *testing.T) {
	p := NewPattern(16,
		Lane{Name: "kick", Role: "kick"},
		Lane{Name: "snare", Role: "snare"},
	)
	p = p.ApplyEuclidean("kick", 4, 0)
	p = p.SetStep("snare", 4, true, 95)
	g := ToDrumGrid(p)
	if g.Steps != dawmodel.DefaultSteps || g.Bars != 1 || g.Channel != 9 {
		t.Errorf("grid header wrong: %+v", *g)
	}
	if len(g.Lanes) != 2 {
		t.Fatalf("grid lanes = %d, want 2", len(g.Lanes))
	}
	if g.Lanes[0].Note != 36 {
		t.Errorf("kick note = %d, want 36", g.Lanes[0].Note)
	}
	// snare step 4 carried velocity
	if !g.Lanes[1].On[4] || g.Lanes[1].Vel[4] != 95 {
		t.Errorf("snare step 4 not in grid: on=%v vel=%d", g.Lanes[1].On[4], g.Lanes[1].Vel[4])
	}
	// nil/empty degrade
	if gr := ToDrumGrid(nil); gr == nil || gr.Bars != 1 {
		t.Error("ToDrumGrid(nil) should return an empty 1-bar grid")
	}
}

func TestRoundTrip_grid(t *testing.T) {
	// grid -> pattern -> grid should preserve the on/off + velocities for a
	// simple forward pattern.
	g := &dawmodel.DrumGrid{
		Steps: 16, Bars: 1, Channel: 9,
		Lanes: []dawmodel.Lane{
			{Name: "kick", Note: 36, On: bools("x..x..x..x..x..."), Vel: velFor("x..x..x..x..x...", 100)},
			{Name: "snare", Note: 38, On: bools("....x.......x..."), Vel: velFor("....x.......x...", 90)},
		},
	}
	p := FromDrumGrid(g)
	g2 := ToDrumGrid(p)
	if len(g2.Lanes) != len(g.Lanes) {
		t.Fatalf("lane count changed: %d -> %d", len(g.Lanes), len(g2.Lanes))
	}
	for li := range g.Lanes {
		for s := range g.Lanes[li].On {
			if g.Lanes[li].On[s] != g2.Lanes[li].On[s] {
				t.Errorf("lane %d step %d on changed: %v -> %v",
					li, s, g.Lanes[li].On[s], g2.Lanes[li].On[s])
			}
			if g.Lanes[li].On[s] && g.Lanes[li].Vel[s] != g2.Lanes[li].Vel[s] {
				t.Errorf("lane %d step %d vel changed: %d -> %d",
					li, s, g.Lanes[li].Vel[s], g2.Lanes[li].Vel[s])
			}
		}
		// note round-trips through role mapping
		if g2.Lanes[li].Note != g.Lanes[li].Note {
			t.Errorf("lane %d note changed: %d -> %d", li, g.Lanes[li].Note, g2.Lanes[li].Note)
		}
	}
}

func TestToDrumGrid_resolvesPolymeter(t *testing.T) {
	// A length-4 forward lane should tile across 16 global steps.
	p := NewPattern(16, Lane{Name: "k", Role: "kick", Length: 4})
	// turn on step 0 of the 4-step cycle
	p = p.SetStep("k", 0, true, 100)
	g := ToDrumGrid(p)
	on := g.Lanes[0].On
	for i := 0; i < 16; i++ {
		want := i%4 == 0
		if on[i] != want {
			t.Errorf("polymeter step %d on=%v, want %v", i, on[i], want)
		}
	}
}

// bools renders an x/. string into []bool.
func bools(s string) []bool {
	out := make([]bool, len(s))
	for i, c := range s {
		out[i] = c == 'x'
	}
	return out
}

// velFor returns a velocity slice with v at each 'x' position.
func velFor(s string, v int) []int {
	out := make([]int, len(s))
	for i, c := range s {
		if c == 'x' {
			out[i] = v
		}
	}
	return out
}
