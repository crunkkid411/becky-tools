package beatgen

import (
	"reflect"
	"testing"
)

// render turns a bool rhythm into x/. for readable assertions.
func render(b []bool) string {
	out := make([]byte, len(b))
	for i, v := range b {
		if v {
			out[i] = 'x'
		} else {
			out[i] = '.'
		}
	}
	return string(out)
}

func TestEuclidean_canonical(t *testing.T) {
	cases := []struct {
		steps, pulses, rot int
		want               string
	}{
		{8, 3, 0, "x..x..x."},          // the textbook E(3,8)
		{8, 5, 0, "x.xx.xx."},          // E(5,8)
		{16, 4, 0, "x...x...x...x..."}, // evenly spaced
		{5, 2, 0, "x.x.."},
		{4, 1, 0, "x..."},
		{16, 5, 0, "x..x..x..x..x..."},
		{8, 3, 1, "..x..x.x"}, // rotate the E(3,8) family left by 1
		{8, 3, -1, ".x..x..x"},
		{8, 3, 8, "x..x..x."}, // full rotation == identity
	}
	for _, c := range cases {
		got := render(Euclidean(c.steps, c.pulses, c.rot))
		if got != c.want {
			t.Errorf("E(%d,%d) rot %d = %q, want %q", c.pulses, c.steps, c.rot, got, c.want)
		}
	}
}

func TestEuclidean_degrade(t *testing.T) {
	if got := Euclidean(0, 3, 0); len(got) != 0 {
		t.Errorf("steps=0 => want empty, got %v", got)
	}
	if got := Euclidean(-4, 3, 0); len(got) != 0 {
		t.Errorf("negative steps => want empty, got %v", got)
	}
	if got := render(Euclidean(8, 0, 0)); got != "........" {
		t.Errorf("pulses=0 => all off, got %q", got)
	}
	if got := render(Euclidean(8, -2, 0)); got != "........" {
		t.Errorf("negative pulses => all off, got %q", got)
	}
	if got := render(Euclidean(8, 8, 0)); got != "xxxxxxxx" {
		t.Errorf("pulses==steps => all on, got %q", got)
	}
	if got := render(Euclidean(8, 99, 0)); got != "xxxxxxxx" {
		t.Errorf("pulses>steps => all on, got %q", got)
	}
}

func TestEuclidean_pulseCount(t *testing.T) {
	// For valid 0<pulses<steps, the count of onsets must equal pulses, for any rotation.
	for steps := 2; steps <= 32; steps++ {
		for pulses := 1; pulses < steps; pulses++ {
			for _, rot := range []int{0, 1, 3, -2, steps} {
				b := Euclidean(steps, pulses, rot)
				if len(b) != steps {
					t.Fatalf("E(%d,%d) length %d != %d", pulses, steps, len(b), steps)
				}
				cnt := 0
				for _, v := range b {
					if v {
						cnt++
					}
				}
				if cnt != pulses {
					t.Fatalf("E(%d,%d) rot %d has %d onsets, want %d (%s)",
						pulses, steps, rot, cnt, pulses, render(b))
				}
			}
		}
	}
}

func TestEuclidean_deterministic(t *testing.T) {
	a := Euclidean(16, 7, 2)
	b := Euclidean(16, 7, 2)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("Euclidean not deterministic: %v vs %v", a, b)
	}
}

func TestApplyEuclidean(t *testing.T) {
	p := NewPattern(8, Lane{Name: "kick", Role: "kick"})
	got := p.ApplyEuclidean("kick", 3, 0)
	on := make([]bool, 8)
	for i, s := range got.Lanes[0].Steps {
		on[i] = s.On
	}
	if render(on) != "x..x..x." {
		t.Errorf("ApplyEuclidean kick = %q, want x..x..x.", render(on))
	}
	// original untouched (immutability)
	for _, s := range p.Lanes[0].Steps {
		if s.On {
			t.Fatal("ApplyEuclidean mutated the input pattern")
		}
	}
	// on-steps got default velocity + full probability
	for i, s := range got.Lanes[0].Steps {
		if s.On && (s.Velocity != DefaultVelocity || s.Probability != MaxProbability) {
			t.Errorf("step %d: bad defaults vel=%d prob=%d", i, s.Velocity, s.Probability)
		}
	}
}

func TestApplyEuclidean_respectsLocks(t *testing.T) {
	p := NewPattern(8, Lane{Name: "kick", Role: "kick"})
	// Pre-set + lock step 1 ON; Euclidean(3,8) would leave it OFF.
	p = p.SetStep("kick", 1, true, 77)
	p = p.SetStepLock("kick", 1, true)
	got := p.ApplyEuclidean("kick", 3, 0)
	if !got.Lanes[0].Steps[1].On || got.Lanes[0].Steps[1].Velocity != 77 {
		t.Error("locked step was overwritten by ApplyEuclidean")
	}

	// Locked lane: whole lane untouched.
	q := NewPattern(8, Lane{Name: "snare", Role: "snare", Locked: true})
	gq := q.ApplyEuclidean("snare", 4, 0)
	for _, s := range gq.Lanes[0].Steps {
		if s.On {
			t.Fatal("ApplyEuclidean edited a locked lane")
		}
	}
}

func TestApplyEuclidean_unknownLane(t *testing.T) {
	p := NewPattern(8, Lane{Name: "kick"})
	got := p.ApplyEuclidean("nope", 3, 0)
	if len(got.Lanes) != 1 || got.Lanes[0].Steps[0].On {
		t.Error("unknown lane should be a no-op copy")
	}
}
