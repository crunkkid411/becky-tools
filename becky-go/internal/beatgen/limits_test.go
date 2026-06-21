package beatgen

import (
	"reflect"
	"testing"
)

func TestGenerateWithLimits_deterministic(t *testing.T) {
	p := basePattern()
	lim := Limits{Velocity: Range{60, 70}, Pan: Range{-30, 30}}
	a := p.GenerateWithLimits(DefaultGenerateOptions(), lim, 42)
	b := p.GenerateWithLimits(DefaultGenerateOptions(), lim, 42)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("GenerateWithLimits not deterministic for same seed")
	}
	c := p.GenerateWithLimits(DefaultGenerateOptions(), lim, 43)
	if reflect.DeepEqual(a, c) {
		t.Fatal("different seeds produced identical output (suspicious)")
	}
}

func TestGenerateWithLimits_immutable(t *testing.T) {
	p := basePattern()
	_ = p.GenerateWithLimits(DefaultGenerateOptions(), Limits{Velocity: Range{50, 60}}, 7)
	for _, ln := range p.Lanes {
		for _, s := range ln.Steps {
			if s.On {
				t.Fatal("GenerateWithLimits mutated the input pattern")
			}
		}
	}
}

func TestGenerateWithLimits_respectsRanges(t *testing.T) {
	p := NewPattern(16, Lane{Name: "k", Role: "perc", Density: 1})
	lim := Limits{
		Velocity: Range{70, 80},
		Pitch:    Range{-3, 3},
		Pan:      Range{-50, 50},
		Flam:     Range{2, 4},
	}
	g := p.GenerateWithLimits(GenerateOptions{RoleAware: false}, lim, 11)
	for i, s := range g.Lanes[0].Steps {
		if !s.On {
			continue
		}
		if s.Velocity < 70 || s.Velocity > 80 {
			t.Errorf("step %d velocity %d out of [70,80]", i, s.Velocity)
		}
		if s.Pitch < -3 || s.Pitch > 3 {
			t.Errorf("step %d pitch %d out of [-3,3]", i, s.Pitch)
		}
		if s.Pan < -50 || s.Pan > 50 {
			t.Errorf("step %d pan %d out of [-50,50]", i, s.Pan)
		}
		if s.Flam < 2 || s.Flam > 4 {
			t.Errorf("step %d flam %d out of [2,4]", i, s.Flam)
		}
	}
}

func TestGenerateWithLimits_unsetRangesDefault(t *testing.T) {
	// A zero-value Limits should leave pitch/pan/flam at 0 and velocity in the
	// opts band (i.e. behave like the plain generator for unset params).
	p := NewPattern(16, Lane{Name: "k", Role: "perc", Density: 1})
	g := p.GenerateWithLimits(GenerateOptions{RoleAware: false, VelMin: 90, VelMax: 100}, Limits{}, 5)
	for i, s := range g.Lanes[0].Steps {
		if !s.On {
			continue
		}
		if s.Pitch != 0 || s.Pan != 0 || s.Flam != 0 {
			t.Errorf("step %d unset params not zero: pitch=%d pan=%d flam=%d", i, s.Pitch, s.Pan, s.Flam)
		}
		if s.Velocity < 90 || s.Velocity > 100 {
			t.Errorf("step %d velocity %d out of opts band [90,100]", i, s.Velocity)
		}
	}
}

func TestGenerateWithLimits_stepCap(t *testing.T) {
	// With density 1 and a StepRange cap of 5, no lane may end with > 5 onsets.
	p := NewPattern(16, Lane{Name: "k", Role: "perc", Density: 1})
	lim := Limits{Steps: Range{0, 5}}
	g := p.GenerateWithLimits(GenerateOptions{RoleAware: false}, lim, 3)
	if c := onCount(g.Lanes[0]); c > 5 {
		t.Errorf("step cap not enforced: %d onsets, want <= 5", c)
	}
	// determinism of the cap
	g2 := p.GenerateWithLimits(GenerateOptions{RoleAware: false}, lim, 3)
	if !reflect.DeepEqual(g, g2) {
		t.Error("step cap not deterministic")
	}
}

func TestGenerateWithLimits_stepCapKeepsLockedAndLoudest(t *testing.T) {
	// A locked ON step must survive the cap and count toward it.
	p := NewPattern(16, Lane{Name: "k", Role: "perc", Density: 1})
	p = p.SetStep("k", 0, true, 120)
	p = p.SetStepLock("k", 0, true)
	g := p.GenerateWithLimits(GenerateOptions{RoleAware: false}, Limits{Steps: Range{0, 3}}, 8)
	if !g.Lanes[0].Steps[0].On {
		t.Error("cap removed a locked onset")
	}
	if c := onCount(g.Lanes[0]); c > 3 {
		t.Errorf("cap with locked step exceeded: %d > 3", c)
	}
}

func TestGenerateWithLimits_respectsLocks(t *testing.T) {
	p := basePattern()
	p = p.SetStep("snare", 0, true, 33)
	p = p.SetLaneLock("snare", true)
	p = p.SetStepLock("kick", 5, true)
	g := p.GenerateWithLimits(DefaultGenerateOptions(), Limits{Velocity: Range{40, 50}}, 99)
	if !g.Lanes[1].Steps[0].On || g.Lanes[1].Steps[0].Velocity != 33 {
		t.Error("locked lane content changed")
	}
	if g.Lanes[0].Steps[5].On {
		t.Error("locked step turned on")
	}
}

func TestGenerateWithLimits_densityOverride(t *testing.T) {
	// A set density limit overrides the lane's own (near-zero) density: with a
	// high density band the lane should fill noticeably.
	p := NewPattern(16, Lane{Name: "k", Role: "perc", Density: 0})
	g := p.GenerateWithLimits(GenerateOptions{RoleAware: false}, Limits{Density: FloatRange{0.9, 1.0}}, 4)
	if onCount(g.Lanes[0]) < 8 {
		t.Errorf("density override did not fill (got %d onsets)", onCount(g.Lanes[0]))
	}
}

func TestRange_resolve(t *testing.T) {
	cases := []struct {
		r              Range
		defLo, defHi   int
		floor, ceil    int
		wantLo, wantHi int
	}{
		{Range{}, 5, 9, 0, 127, 5, 9},           // unset => default
		{Range{70, 80}, 0, 0, 0, 127, 70, 80},   // explicit
		{Range{80, 70}, 0, 0, 0, 127, 70, 80},   // swapped => sorted
		{Range{-50, 500}, 0, 0, 0, 127, 0, 127}, // clamped to floor/ceil
	}
	for _, c := range cases {
		lo, hi := c.r.resolve(c.defLo, c.defHi, c.floor, c.ceil)
		if lo != c.wantLo || hi != c.wantHi {
			t.Errorf("resolve(%+v) = (%d,%d), want (%d,%d)", c.r, lo, hi, c.wantLo, c.wantHi)
		}
	}
}
