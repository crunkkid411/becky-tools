package beatgen

import "testing"

func TestEffRate_default(t *testing.T) {
	cases := []struct {
		rate float64
		want float64
	}{
		{0, 1.0},   // unset => global rate
		{-2, 1.0},  // negative => global rate (degrade)
		{1.0, 1.0}, // explicit normal
		{2.0, 2.0}, // double-time
		{0.5, 0.5}, // half-time
	}
	for _, c := range cases {
		if got := (Lane{Rate: c.rate}).effRate(); got != c.want {
			t.Errorf("effRate(%v) = %v, want %v", c.rate, got, c.want)
		}
	}
}

func TestEffSwing_override(t *testing.T) {
	// zero lane swing inherits the global; a set lane swing overrides it.
	if got := (Lane{Swing: 0}).effSwing(0.5); got != 0.5 {
		t.Errorf("zero lane swing should inherit global 0.5, got %v", got)
	}
	if got := (Lane{Swing: 0.8}).effSwing(0.2); got != 0.8 {
		t.Errorf("lane swing 0.8 should override global, got %v", got)
	}
}

func TestEffTrackDelay_clamp(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{0, 0},
		{0.02, 0.02},
		{-0.02, -0.02},
		{5, 1},   // clamp high
		{-5, -1}, // clamp low
	}
	for _, c := range cases {
		if got := (Lane{TrackDelay: c.in}).effTrackDelay(); got != c.want {
			t.Errorf("effTrackDelay(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestLaneStepOffset_zeroIsLegacy(t *testing.T) {
	// A lane with zero Swing and zero TrackDelay must equal SwingOffset(i, global).
	ln := Lane{Steps: make([]Step, 8)}
	for i := 0; i < 8; i++ {
		want := SwingOffset(i, 0.6)
		if got := ln.LaneStepOffset(i, 0.6); got != want {
			t.Errorf("step %d: LaneStepOffset=%v, want SwingOffset=%v", i, got, want)
		}
	}
}

func TestLaneStepOffset_swingOverridePlusDelay(t *testing.T) {
	ln := Lane{Swing: 1.0, TrackDelay: 0.1, Steps: make([]Step, 4)}
	// odd step at swing 1 => 0.5, plus track delay 0.1 = 0.6
	if got := ln.LaneStepOffset(1, 0); got != 0.6 {
		t.Errorf("odd step offset = %v, want 0.6 (0.5 swing + 0.1 delay)", got)
	}
	// even step => 0 swing + 0.1 delay = 0.1
	if got := ln.LaneStepOffset(0, 0); got != 0.1 {
		t.Errorf("even step offset = %v, want 0.1 (track delay only)", got)
	}
}

func TestPatternStepOffset(t *testing.T) {
	p := NewPattern(8,
		Lane{Name: "a", Swing: 1.0},       // overrides global
		Lane{Name: "b", TrackDelay: 0.05}, // delay only, inherits global swing
	)
	p.Swing = 0 // global straight
	// lane a, odd step: own swing 1 => 0.5
	if got := p.StepOffset("a", 1); got != 0.5 {
		t.Errorf("lane a odd step = %v, want 0.5", got)
	}
	// lane b, odd step: global swing 0 => 0, + delay 0.05
	if got := p.StepOffset("b", 1); got != 0.05 {
		t.Errorf("lane b odd step = %v, want 0.05", got)
	}
	// unknown lane falls back to global swing, no delay
	if got := p.StepOffset("zzz", 1); got != SwingOffset(1, p.Swing) {
		t.Errorf("unknown lane = %v, want %v", got, SwingOffset(1, p.Swing))
	}
}

func TestNewPattern_zeroLaneTimingUnchanged(t *testing.T) {
	// Constructing lanes with zero Rate/Swing/TrackDelay must leave the timing
	// behavior identical to a bare SwingOffset (no regression).
	p := NewPattern(8, Lane{Name: "k"})
	for i := 0; i < 8; i++ {
		if p.StepOffset("k", i) != SwingOffset(i, p.Swing) {
			t.Fatalf("zero-timing lane diverged from SwingOffset at step %d", i)
		}
	}
}
