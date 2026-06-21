package beatgen

import (
	"reflect"
	"testing"
)

func basePattern() *Pattern {
	return NewPattern(16,
		Lane{Name: "kick", Role: "kick", Density: 0.5},
		Lane{Name: "snare", Role: "snare", Density: 0.5},
		Lane{Name: "hat", Role: "hat", Density: 0.7},
	)
}

func TestGenerate_deterministic(t *testing.T) {
	p := basePattern()
	a := p.Generate(DefaultGenerateOptions(), 42)
	b := p.Generate(DefaultGenerateOptions(), 42)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("Generate not deterministic for same seed")
	}
	c := p.Generate(DefaultGenerateOptions(), 43)
	if reflect.DeepEqual(a, c) {
		t.Fatal("Generate produced identical output for different seeds (suspicious)")
	}
}

func TestGenerate_immutable(t *testing.T) {
	p := basePattern()
	_ = p.Generate(DefaultGenerateOptions(), 7)
	for _, ln := range p.Lanes {
		for _, s := range ln.Steps {
			if s.On {
				t.Fatal("Generate mutated the input pattern")
			}
		}
	}
}

func TestGenerate_respectsLocks(t *testing.T) {
	p := basePattern()
	// lock the whole snare lane with a known content
	p = p.SetStep("snare", 0, true, 33)
	p = p.SetLaneLock("snare", true)
	// lock one kick step OFF
	p = p.SetStepLock("kick", 5, true)
	g := p.Generate(DefaultGenerateOptions(), 99)

	// snare lane unchanged
	if !g.Lanes[1].Steps[0].On || g.Lanes[1].Steps[0].Velocity != 33 {
		t.Error("locked lane content changed")
	}
	for i, s := range g.Lanes[1].Steps {
		if i != 0 && s.On {
			t.Error("locked lane gained onsets")
		}
	}
	// locked kick step 5 stayed OFF
	if g.Lanes[0].Steps[5].On {
		t.Error("locked step was turned on by Generate")
	}
}

func TestGenerate_densityZeroAndOne(t *testing.T) {
	p := NewPattern(16,
		Lane{Name: "empty", Role: "perc", Density: 0},
		Lane{Name: "full", Role: "perc", Density: 1},
	)
	g := p.Generate(GenerateOptions{RoleAware: false}, 5)
	for _, s := range g.Lanes[0].Steps {
		if s.On {
			t.Error("density 0 produced an onset")
		}
	}
	for i, s := range g.Lanes[1].Steps {
		if !s.On {
			t.Errorf("density 1 left step %d off", i)
		}
		if s.Velocity < 1 {
			t.Errorf("on step %d has silent velocity", i)
		}
	}
}

func TestGenerate_roleWeightingBiasesKick(t *testing.T) {
	// With role-aware weighting and moderate density, kicks should land on
	// downbeats more than offbeats across many seeds.
	downbeat, offbeat := 0, 0
	for seed := int64(0); seed < 200; seed++ {
		p := NewPattern(16, Lane{Name: "kick", Role: "kick", Density: 0.4})
		g := p.Generate(GenerateOptions{RoleAware: true, VelMin: 90, VelMax: 110}, seed)
		for _, beat := range []int{0, 4, 8, 12} {
			if g.Lanes[0].Steps[beat].On {
				downbeat++
			}
		}
		for _, off := range []int{1, 3, 5, 7} {
			if g.Lanes[0].Steps[off].On {
				offbeat++
			}
		}
	}
	if downbeat <= offbeat {
		t.Errorf("kick role weighting failed: downbeats=%d offbeats=%d", downbeat, offbeat)
	}
}

func TestGenerate_velocityBand(t *testing.T) {
	p := NewPattern(16, Lane{Name: "k", Role: "perc", Density: 1})
	g := p.Generate(GenerateOptions{RoleAware: false, VelMin: 70, VelMax: 80}, 11)
	for i, s := range g.Lanes[0].Steps {
		if s.On && (s.Velocity < 70 || s.Velocity > 80) {
			t.Errorf("step %d velocity %d out of band [70,80]", i, s.Velocity)
		}
	}
}

func TestGenerate_laneIndependence(t *testing.T) {
	// Adding/locking one lane must not shift another lane's draws (per-lane streams).
	p1 := NewPattern(16, Lane{Name: "kick", Role: "kick", Density: 0.5})
	g1 := p1.Generate(DefaultGenerateOptions(), 123)

	p2 := NewPattern(16,
		Lane{Name: "kick", Role: "kick", Density: 0.5},
		Lane{Name: "extra", Role: "hat", Density: 0.5, Locked: true},
	)
	g2 := p2.Generate(DefaultGenerateOptions(), 123)

	if !reflect.DeepEqual(g1.Lanes[0].Steps, g2.Lanes[0].Steps) {
		t.Error("a locked second lane shifted the first lane's generation")
	}
}

func TestMutate_deterministicAndSmall(t *testing.T) {
	p := basePattern().Generate(DefaultGenerateOptions(), 1)
	a := p.Mutate(0.2, 9)
	b := p.Mutate(0.2, 9)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("Mutate not deterministic")
	}
	if p2 := p.Mutate(0, 9); !reflect.DeepEqual(p2, p) {
		t.Error("Mutate(0) should be a no-op copy")
	}
	// amount 1 changes a lot; amount 0.1 changes little — count flips.
	changes := countOnDiffs(p, p.Mutate(0.1, 3))
	heavy := countOnDiffs(p, p.Mutate(1.0, 3))
	if heavy <= changes {
		t.Errorf("higher mutate amount should flip more steps: light=%d heavy=%d", changes, heavy)
	}
}

func TestMutate_respectsLocks(t *testing.T) {
	p := basePattern().Generate(DefaultGenerateOptions(), 1)
	p = p.SetStepLock("kick", 0, true)
	before := p.Lanes[0].Steps[0]
	g := p.Mutate(1.0, 4) // maximum churn
	if g.Lanes[0].Steps[0] != before {
		t.Error("Mutate changed a locked step")
	}

	q := basePattern().Generate(DefaultGenerateOptions(), 2)
	q = q.SetLaneLock("snare", true)
	beforeLane := append([]Step(nil), q.Lanes[1].Steps...)
	gq := q.Mutate(1.0, 4)
	if !reflect.DeepEqual(gq.Lanes[1].Steps, beforeLane) {
		t.Error("Mutate changed a locked lane")
	}
}

// countOnDiffs counts steps whose On bit differs between two patterns.
func countOnDiffs(a, b *Pattern) int {
	n := 0
	for li := range a.Lanes {
		for s := range a.Lanes[li].Steps {
			if a.Lanes[li].Steps[s].On != b.Lanes[li].Steps[s].On {
				n++
			}
		}
	}
	return n
}
