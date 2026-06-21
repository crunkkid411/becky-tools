package beatgen

import (
	"reflect"
	"testing"
)

func TestRemix_deterministicAndImmutable(t *testing.T) {
	p := basePattern().Generate(DefaultGenerateOptions(), 1)
	a := p.Remix(0.3, 9)
	b := p.Remix(0.3, 9)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("Remix not deterministic")
	}
	if z := p.Remix(0, 9); !reflect.DeepEqual(z, p) {
		t.Error("Remix(0) should be a no-op copy")
	}
	// immutability: input unchanged
	pCopy := p.Clone()
	_ = p.Remix(1.0, 5)
	if !reflect.DeepEqual(p, pCopy) {
		t.Error("Remix mutated the input")
	}
}

func TestRemix_subtlerThanMutate(t *testing.T) {
	// For the same amount and seed, Remix should change FEWER steps than Mutate.
	p := basePattern().Generate(DefaultGenerateOptions(), 1)
	totalRemix, totalMutate := 0, 0
	for seed := int64(0); seed < 50; seed++ {
		totalRemix += countOnDiffs(p, p.Remix(0.4, seed))
		totalMutate += countOnDiffs(p, p.Mutate(0.4, seed))
	}
	if totalRemix >= totalMutate {
		t.Errorf("Remix not subtler than Mutate: remix=%d mutate=%d", totalRemix, totalMutate)
	}
}

func TestRemix_respectsLocks(t *testing.T) {
	p := basePattern().Generate(DefaultGenerateOptions(), 1)
	p = p.SetStepLock("kick", 0, true)
	before := p.Lanes[0].Steps[0]
	g := p.Remix(1.0, 4)
	if g.Lanes[0].Steps[0] != before {
		t.Error("Remix changed a locked step")
	}

	q := basePattern().Generate(DefaultGenerateOptions(), 2)
	q = q.SetLaneLock("snare", true)
	beforeLane := append([]Step(nil), q.Lanes[1].Steps...)
	gq := q.Remix(1.0, 4)
	if !reflect.DeepEqual(gq.Lanes[1].Steps, beforeLane) {
		t.Error("Remix changed a locked lane")
	}
}

func TestRemix_ghostNotesAreQuiet(t *testing.T) {
	// New onsets Remix introduces should be quieter than the lane's average
	// existing onset (ghost notes), not full-velocity redraws.
	p := NewPattern(16, Lane{Name: "k", Role: "perc"})
	for i := 0; i < 16; i += 2 {
		p = p.SetStep("k", i, true, 120) // loud existing onsets
	}
	g := p.Remix(1.0, 7)
	for i, s := range g.Lanes[0].Steps {
		// a newly-turned-on odd step (was off) should be quiet
		if i%2 == 1 && s.On {
			if s.Velocity > 100 {
				t.Errorf("ghost note at %d too loud: vel %d", i, s.Velocity)
			}
		}
	}
}

func TestMode_string(t *testing.T) {
	for m, want := range map[Mode]string{
		ModeRandom: "random", ModeRemix: "remix", ModeSmart: "smart",
	} {
		if m.String() != want {
			t.Errorf("%d.String() = %q, want %q", m, m.String(), want)
		}
	}
	if Mode(99).String() == "" {
		t.Error("unknown mode should still render something")
	}
}

func TestInfinityTick_onlyOnBoundaries(t *testing.T) {
	p := basePattern().Generate(DefaultGenerateOptions(), 1)
	opts := DefaultGenerateOptions()
	// everyN = 4: ticks 0,4,8 regenerate; 1,2,3,5 return unchanged copy.
	for _, li := range []int{1, 2, 3, 5, 6, 7} {
		g := p.InfinityTick(li, 4, ModeRandom, opts, 100)
		if !reflect.DeepEqual(g, p) {
			t.Errorf("loop %d (not a boundary) should be unchanged", li)
		}
	}
	g0 := p.InfinityTick(0, 4, ModeRandom, opts, 100)
	if reflect.DeepEqual(g0, p) {
		t.Error("loop 0 (boundary) should regenerate")
	}
}

func TestInfinityTick_eachBoundaryDiffersButReproducible(t *testing.T) {
	p := basePattern().Generate(DefaultGenerateOptions(), 1)
	opts := DefaultGenerateOptions()
	g4 := p.InfinityTick(4, 4, ModeRandom, opts, 100)
	g8 := p.InfinityTick(8, 4, ModeRandom, opts, 100)
	if reflect.DeepEqual(g4, g8) {
		t.Error("different loop boundaries should differ")
	}
	// reproducible: same (seed, loopIndex) => same pattern
	if !reflect.DeepEqual(p.InfinityTick(8, 4, ModeRandom, opts, 100), g8) {
		t.Error("InfinityTick not reproducible for same (seed, loopIndex)")
	}
}

func TestInfinityTick_modes(t *testing.T) {
	p := basePattern().Generate(DefaultGenerateOptions(), 1)
	opts := DefaultGenerateOptions()
	// Remix mode should keep closer to the original than a full Random redraw.
	remix := p.InfinityTick(0, 1, ModeRemix, opts, 50)
	random := p.InfinityTick(0, 1, ModeRandom, opts, 50)
	if countOnDiffs(p, remix) >= countOnDiffs(p, random) {
		t.Error("ModeRemix should change less than ModeRandom")
	}
	// Smart mode produces a valid pattern (role-aware) without panicking.
	smart := p.InfinityTick(0, 1, ModeSmart, GenerateOptions{}, 50)
	if smart == nil || len(smart.Lanes) != len(p.Lanes) {
		t.Error("ModeSmart produced an invalid pattern")
	}
}

func TestInfinityTick_degrade(t *testing.T) {
	p := basePattern()
	opts := DefaultGenerateOptions()
	if g := p.InfinityTick(0, 0, ModeRandom, opts, 1); !reflect.DeepEqual(g, p) {
		t.Error("everyN<=0 should return an unchanged copy")
	}
	if g := p.InfinityTick(-1, 4, ModeRandom, opts, 1); !reflect.DeepEqual(g, p) {
		t.Error("negative loopIndex should return an unchanged copy")
	}
}
