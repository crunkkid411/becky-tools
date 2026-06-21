package beatgen

import (
	"reflect"
	"testing"
)

func TestStepOrder_directions(t *testing.T) {
	mk := func(n int, d Direction) Lane {
		return Lane{Name: "l", Steps: make([]Step, n), Direction: d}
	}
	if got := mk(4, Forward).StepOrder(0); !reflect.DeepEqual(got, []int{0, 1, 2, 3}) {
		t.Errorf("Forward = %v", got)
	}
	if got := mk(4, Reverse).StepOrder(0); !reflect.DeepEqual(got, []int{3, 2, 1, 0}) {
		t.Errorf("Reverse = %v", got)
	}
	// PingPong of length 4: 0 1 2 3 2 1 (length 2n-2 = 6, endpoints not repeated)
	if got := mk(4, PingPong).StepOrder(0); !reflect.DeepEqual(got, []int{0, 1, 2, 3, 2, 1}) {
		t.Errorf("PingPong = %v", got)
	}
}

func TestStepOrder_edgeLengths(t *testing.T) {
	if got := (Lane{Steps: nil}).StepOrder(0); got != nil {
		t.Errorf("empty lane order = %v, want nil", got)
	}
	one := Lane{Steps: make([]Step, 1), Direction: PingPong}
	if got := one.StepOrder(0); !reflect.DeepEqual(got, []int{0}) {
		t.Errorf("single-step order = %v, want [0]", got)
	}
}

func TestStepOrder_randomDeterministicPermutation(t *testing.T) {
	l := Lane{Name: "rng", Steps: make([]Step, 8), Direction: Random}
	a := l.StepOrder(77)
	b := l.StepOrder(77)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("Random StepOrder not deterministic for same seed")
	}
	// must be a permutation of 0..7
	seen := make([]bool, 8)
	for _, v := range a {
		if v < 0 || v >= 8 || seen[v] {
			t.Fatalf("Random order not a permutation: %v", a)
		}
		seen[v] = true
	}
	// different lane name => different stream (very likely different order)
	l2 := Lane{Name: "other", Steps: make([]Step, 8), Direction: Random}
	if reflect.DeepEqual(l2.StepOrder(77), a) {
		t.Log("note: two lane names produced same permutation (allowed but unlikely)")
	}
}

func TestStepOrder_polymeterLength(t *testing.T) {
	// 8 steps but Length 5 => order cycles over 0..4 only.
	l := Lane{Steps: make([]Step, 8), Length: 5, Direction: Forward}
	if got := l.StepOrder(0); !reflect.DeepEqual(got, []int{0, 1, 2, 3, 4}) {
		t.Errorf("polymeter Forward order = %v, want 0..4", got)
	}
}

func TestStepAt_polymeter(t *testing.T) {
	// lane length 3, forward. Steps 0,1,2 mark distinct velocities.
	l := Lane{
		Steps: []Step{
			{On: true, Velocity: 10}, {On: true, Velocity: 20}, {On: true, Velocity: 30},
		},
		Length:    3,
		Direction: Forward,
	}
	want := []int{10, 20, 30, 10, 20, 30, 10} // global steps 0..6 cycle mod 3
	for gs, v := range want {
		st, ok := l.StepAt(gs, 0)
		if !ok || st.Velocity != v {
			t.Errorf("StepAt(%d) vel=%d ok=%v, want %d", gs, st.Velocity, ok, v)
		}
	}
}

func TestStepAt_reversePolymeter(t *testing.T) {
	l := Lane{
		Steps:     []Step{{On: true, Velocity: 1}, {On: true, Velocity: 2}, {On: true, Velocity: 3}},
		Length:    3,
		Direction: Reverse,
	}
	// reverse order is 2,1,0 => velocities 3,2,1 repeating
	want := []int{3, 2, 1, 3, 2, 1}
	for gs, v := range want {
		st, _ := l.StepAt(gs, 0)
		if st.Velocity != v {
			t.Errorf("reverse StepAt(%d) = %d, want %d", gs, st.Velocity, v)
		}
	}
}

func TestStepAt_empty(t *testing.T) {
	if _, ok := (Lane{}).StepAt(0, 0); ok {
		t.Error("StepAt on empty lane should be ok=false")
	}
}

func TestExpandStep_ratchet(t *testing.T) {
	s := Step{On: true, Velocity: 90, Probability: 100, Ratchet: 4}
	hits := ExpandStep(s, 0, 0)
	if len(hits) != 4 {
		t.Fatalf("ratchet 4 => %d hits, want 4", len(hits))
	}
	wantOff := []float64{0, 0.25, 0.5, 0.75}
	for i, h := range hits {
		if h.Offset != wantOff[i] || h.Velocity != 90 {
			t.Errorf("hit %d = %+v, want offset %v vel 90", i, h, wantOff[i])
		}
	}
}

func TestExpandStep_off(t *testing.T) {
	if hits := ExpandStep(Step{On: false, Ratchet: 4}, 0, 0); hits != nil {
		t.Errorf("OFF step should expand to nil, got %v", hits)
	}
}

func TestExpandStep_probabilityGate(t *testing.T) {
	// prob 100 always fires; prob 0 never fires.
	always := Step{On: true, Velocity: 100, Probability: 100, Ratchet: 1}
	never := Step{On: true, Velocity: 100, Probability: 0, Ratchet: 1}
	for gs := 0; gs < 50; gs++ {
		if len(ExpandStep(always, 1, gs)) != 1 {
			t.Fatalf("prob 100 failed to fire at gs=%d", gs)
		}
		if len(ExpandStep(never, 1, gs)) != 0 {
			t.Fatalf("prob 0 fired at gs=%d", gs)
		}
	}
}

func TestExpandStep_probabilityDeterministic(t *testing.T) {
	s := Step{On: true, Velocity: 100, Probability: 50, Ratchet: 1}
	// same (seed, globalStep) => same decision every call
	for gs := 0; gs < 20; gs++ {
		a := len(ExpandStep(s, 7, gs))
		b := len(ExpandStep(s, 7, gs))
		if a != b {
			t.Fatalf("probability not deterministic at gs=%d: %d vs %d", gs, a, b)
		}
	}
	// 50% gate should fire on some and not others across many positions
	fired := 0
	for gs := 0; gs < 200; gs++ {
		if len(ExpandStep(s, 7, gs)) > 0 {
			fired++
		}
	}
	if fired == 0 || fired == 200 {
		t.Errorf("50%% probability gate degenerate: fired %d/200", fired)
	}
}

func TestSwingOffset(t *testing.T) {
	// swing 0 => straight everywhere
	for i := 0; i < 8; i++ {
		if SwingOffset(i, 0) != 0 {
			t.Errorf("swing 0 step %d offset != 0", i)
		}
	}
	// even steps never delayed
	if SwingOffset(0, 1) != 0 || SwingOffset(2, 1) != 0 {
		t.Error("even steps should not be delayed")
	}
	// odd steps delayed; max 0.5 at swing 1
	if SwingOffset(1, 1) != 0.5 {
		t.Errorf("odd step at swing 1 = %v, want 0.5", SwingOffset(1, 1))
	}
	if SwingOffset(3, 0.5) != 0.25 {
		t.Errorf("odd step at swing 0.5 = %v, want 0.25", SwingOffset(3, 0.5))
	}
	// clamp
	if SwingOffset(1, -3) != 0 {
		t.Error("negative swing should clamp to straight")
	}
	if SwingOffset(1, 5) != 0.5 {
		t.Error("swing > 1 should clamp to 0.5 offset on odd steps")
	}
}
