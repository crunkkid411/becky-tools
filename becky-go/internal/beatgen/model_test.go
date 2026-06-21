package beatgen

import "testing"

func TestNewPattern_sizesLanes(t *testing.T) {
	p := NewPattern(16,
		Lane{Name: "kick", Role: "kick"},
		Lane{Name: "poly", Role: "hat", Length: 6}, // polymeter lane
	)
	if p.Steps != 16 {
		t.Fatalf("Steps = %d, want 16", p.Steps)
	}
	if len(p.Lanes[0].Steps) != 16 {
		t.Errorf("kick lane: %d steps, want 16", len(p.Lanes[0].Steps))
	}
	if len(p.Lanes[1].Steps) != 6 {
		t.Errorf("poly lane: %d steps, want 6 (its Length)", len(p.Lanes[1].Steps))
	}
}

func TestNewPattern_clampsSteps(t *testing.T) {
	if p := NewPattern(-5); p.Steps != 0 {
		t.Errorf("negative steps clamped to %d, want 0", p.Steps)
	}
	if p := NewPattern(99999, Lane{Name: "x"}); p.Steps != MaxSteps {
		t.Errorf("oversized steps clamped to %d, want %d", p.Steps, MaxSteps)
	}
}

func TestStep_normalizeClamps(t *testing.T) {
	s := Step{Velocity: 300, Pan: -500, Probability: 250, Ratchet: 99}.normalize()
	if s.Velocity != MaxVelocity || s.Pan != MinPan || s.Probability != MaxProbability || s.Ratchet != MaxRatchet {
		t.Errorf("normalize did not clamp: %+v", s)
	}
	s2 := Step{Ratchet: 0, Probability: -10, Velocity: -5}.normalize()
	if s2.Ratchet != MinRatchet || s2.Probability != MinProbability || s2.Velocity != MinVelocity {
		t.Errorf("normalize low bound failed: %+v", s2)
	}
}

func TestClone_isDeep(t *testing.T) {
	p := NewPattern(4, Lane{Name: "k"})
	p = p.SetStep("k", 0, true, 100)
	c := p.Clone()
	c.Lanes[0].Steps[0].Velocity = 1
	c.Lanes[0].Name = "changed"
	if p.Lanes[0].Steps[0].Velocity != 100 {
		t.Error("Clone shares step memory with original")
	}
	if p.Lanes[0].Name != "k" {
		t.Error("Clone shares lane header with original")
	}
	if Clone := (*Pattern)(nil).Clone(); Clone != nil {
		t.Error("nil.Clone() should be nil")
	}
}

func TestEffLength(t *testing.T) {
	cases := []struct {
		steps  int
		length int
		want   int
	}{
		{8, 0, 8},   // unset => full
		{8, -3, 8},  // negative => full
		{8, 5, 5},   // valid polymeter
		{8, 99, 8},  // oversized => full
		{0, 4, 0},   // no steps => 0
	}
	for _, c := range cases {
		ln := Lane{Steps: make([]Step, c.steps), Length: c.length}
		if got := ln.effLength(); got != c.want {
			t.Errorf("effLength(steps=%d,length=%d) = %d, want %d", c.steps, c.length, got, c.want)
		}
	}
}

func TestLaneLookup(t *testing.T) {
	p := NewPattern(4, Lane{Name: "a"}, Lane{Name: "b"})
	if _, ok := p.Lane("b"); !ok {
		t.Error("Lane(b) not found")
	}
	if _, ok := p.Lane("zzz"); ok {
		t.Error("Lane(zzz) should not be found")
	}
	// returned lane is a copy
	l, _ := p.Lane("a")
	l.Name = "mutated"
	if p.Lanes[0].Name != "a" {
		t.Error("Lane() returned a non-copy")
	}
}

func TestDirectionString(t *testing.T) {
	for d, want := range map[Direction]string{
		Forward: "forward", Reverse: "reverse", PingPong: "pingpong", Random: "random",
	} {
		if d.String() != want {
			t.Errorf("%d.String() = %q, want %q", d, d.String(), want)
		}
	}
	if Direction(99).String() == "" {
		t.Error("unknown direction should still render something")
	}
}
