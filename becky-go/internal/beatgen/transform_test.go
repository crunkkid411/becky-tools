package beatgen

import (
	"reflect"
	"testing"
)

func onString(ln Lane) string {
	b := make([]bool, len(ln.Steps))
	for i, s := range ln.Steps {
		b[i] = s.On
	}
	return render(b)
}

func TestSetDensity(t *testing.T) {
	p := NewPattern(8, Lane{Name: "k", Density: 0.2})
	g := p.SetDensity("k", 0.9)
	if g.Lanes[0].Density != 0.9 {
		t.Errorf("density = %v, want 0.9", g.Lanes[0].Density)
	}
	if p.Lanes[0].Density != 0.2 {
		t.Error("SetDensity mutated input")
	}
	// clamp
	if g2 := p.SetDensity("k", 5); g2.Lanes[0].Density != 1 {
		t.Errorf("density not clamped high: %v", g2.Lanes[0].Density)
	}
	if g3 := p.SetDensity("k", -2); g3.Lanes[0].Density != 0 {
		t.Errorf("density not clamped low: %v", g3.Lanes[0].Density)
	}
	// locked lane
	pl := p.SetLaneLock("k", true)
	if g4 := pl.SetDensity("k", 1); g4.Lanes[0].Density != 0.2 {
		t.Error("SetDensity changed a locked lane")
	}
}

func TestBusierSparser(t *testing.T) {
	p := NewPattern(16, Lane{Name: "k"})
	// fully off; Busier 4 turns on exactly 4
	b := p.Busier("k", 4, 1)
	if cnt := onCount(b.Lanes[0]); cnt != 4 {
		t.Errorf("Busier 4 => %d onsets, want 4", cnt)
	}
	// deterministic
	if !reflect.DeepEqual(p.Busier("k", 4, 1), b) {
		t.Error("Busier not deterministic")
	}
	// Busier more than available => all on
	allOn := p.Busier("k", 999, 1)
	if onCount(allOn.Lanes[0]) != 16 {
		t.Error("Busier beyond capacity should fill all")
	}
	// Sparser removes hits
	s := allOn.Sparser("k", 6, 2)
	if cnt := onCount(s.Lanes[0]); cnt != 10 {
		t.Errorf("Sparser 6 from full => %d onsets, want 10", cnt)
	}
	if onCount(allOn.Lanes[0]) != 16 {
		t.Error("Sparser mutated input")
	}
}

func TestBusier_respectsLocks(t *testing.T) {
	p := NewPattern(4, Lane{Name: "k"})
	p = p.SetStepLock("k", 0, true) // off + locked, must never turn on
	p = p.SetStepLock("k", 1, true)
	p = p.SetStepLock("k", 2, true)
	// Only step 3 is eligible; Busier 4 should turn on only step 3.
	g := p.Busier("k", 4, 5)
	if g.Lanes[0].Steps[0].On || g.Lanes[0].Steps[1].On || g.Lanes[0].Steps[2].On {
		t.Error("Busier turned on a locked step")
	}
	if !g.Lanes[0].Steps[3].On {
		t.Error("Busier failed to fill the one eligible step")
	}

	// locked lane no-op
	pl := NewPattern(4, Lane{Name: "k", Locked: true})
	if onCount(pl.Busier("k", 2, 1).Lanes[0]) != 0 {
		t.Error("Busier edited a locked lane")
	}
}

func TestRotate(t *testing.T) {
	p := NewPattern(8, Lane{Name: "k"})
	p = p.SetStep("k", 0, true, 100).SetStep("k", 2, true, 100)
	// pattern: x.x.....
	if onString(p.Lanes[0]) != "x.x....." {
		t.Fatalf("setup: %q", onString(p.Lanes[0]))
	}
	// rotate left 1 => .x.x....  -> actually wrapping: index0 takes old index1
	g := p.Rotate("k", 1)
	if onString(g.Lanes[0]) != ".x......" {
		// old idx1(off)->0, idx2(on)->1, idx3->2... so x at old 0 wraps to position 7
		// x.x..... rotate left 1 => .x.....x
	}
	got := onString(g.Lanes[0])
	if got != ".x.....x" {
		t.Errorf("rotate left 1 = %q, want .x.....x", got)
	}
	// rotate right 1 (n=-1)
	gr := p.Rotate("k", -1)
	if onString(gr.Lanes[0]) != ".x.x...." {
		t.Errorf("rotate right 1 = %q, want .x.x....", onString(gr.Lanes[0]))
	}
	// full rotation identity
	if !reflect.DeepEqual(p.Rotate("k", 8), p) {
		t.Error("rotate by length should be identity")
	}
	// immutability
	if onString(p.Lanes[0]) != "x.x....." {
		t.Error("Rotate mutated input")
	}
	// locked lane
	pl := p.SetLaneLock("k", true)
	if !reflect.DeepEqual(pl.Rotate("k", 3).Lanes[0].Steps, pl.Lanes[0].Steps) {
		t.Error("Rotate changed a locked lane")
	}
}

func TestSetStep(t *testing.T) {
	p := NewPattern(4, Lane{Name: "k"})
	g := p.SetStep("k", 1, true, 0) // vel<=0 => default
	if !g.Lanes[0].Steps[1].On || g.Lanes[0].Steps[1].Velocity != DefaultVelocity {
		t.Error("SetStep default velocity wrong")
	}
	if g.Lanes[0].Steps[1].Probability != MaxProbability || g.Lanes[0].Steps[1].Ratchet != MinRatchet {
		t.Error("SetStep should seed probability/ratchet")
	}
	// turn off clears velocity
	off := g.SetStep("k", 1, false, 0)
	if off.Lanes[0].Steps[1].On || off.Lanes[0].Steps[1].Velocity != 0 {
		t.Error("SetStep off did not clear")
	}
	// out of range no-op
	if !reflect.DeepEqual(p.SetStep("k", 99, true, 1), p) {
		t.Error("out-of-range SetStep should be a no-op")
	}
	// locked step no-op
	pl := p.SetStepLock("k", 0, true)
	if pl.SetStep("k", 0, true, 50).Lanes[0].Steps[0].On {
		t.Error("SetStep changed a locked step")
	}
}

func TestLockToggles(t *testing.T) {
	p := NewPattern(4, Lane{Name: "k"})
	if !p.SetLaneLock("k", true).Lanes[0].Locked {
		t.Error("SetLaneLock did not set")
	}
	if !p.SetStepLock("k", 2, true).Lanes[0].Steps[2].Locked {
		t.Error("SetStepLock did not set")
	}
	// invalid indices are safe no-ops
	_ = p.SetStepLock("k", 99, true)
	_ = p.SetStepLock("nope", 0, true)
	_ = p.SetLaneLock("nope", true)
}

func onCount(ln Lane) int {
	n := 0
	for _, s := range ln.Steps {
		if s.On {
			n++
		}
	}
	return n
}
