package beatgen

import (
	"reflect"
	"testing"
)

func TestExpandStep_zeroFlamUnchanged(t *testing.T) {
	// A zero-Flam step must expand EXACTLY as before (no grace hits) — the
	// invariant that keeps the pre-flam tests passing.
	cases := []Step{
		{On: true, Velocity: 90, Probability: 100, Ratchet: 1},
		{On: true, Velocity: 90, Probability: 100, Ratchet: 4},
		{On: true, Velocity: 50, Probability: 100, Ratchet: 8},
	}
	for _, s := range cases {
		hits := ExpandStep(s, 0, 0)
		if len(hits) != s.Ratchet {
			t.Fatalf("Flam 0 ratchet %d => %d hits, want %d", s.Ratchet, len(hits), s.Ratchet)
		}
		for i, h := range hits {
			if h.Velocity != s.Velocity {
				t.Errorf("Flam 0: hit %d velocity %d, want %d", i, h.Velocity, s.Velocity)
			}
			wantOff := float64(i) / float64(s.Ratchet)
			if h.Offset != wantOff {
				t.Errorf("Flam 0: hit %d offset %v, want %v", i, h.Offset, wantOff)
			}
		}
	}
}

func TestExpandStep_flamAddsGraceHit(t *testing.T) {
	s := Step{On: true, Velocity: 100, Probability: 100, Ratchet: 1, Flam: 1}
	hits := ExpandStep(s, 0, 0)
	if len(hits) != 2 {
		t.Fatalf("ratchet 1 + flam => %d hits, want 2 (grace + main)", len(hits))
	}
	grace, main := hits[0], hits[1]
	// The grace hit is emitted first and is quieter. NOTE: for a sub-hit at
	// offset 0 the grace can't physically land earlier (offset is clamped to >=0),
	// so here both share offset 0; the ordering guarantee is by emit order +
	// velocity. The offset-precedes-main case is covered separately below.
	if grace.Offset < 0 {
		t.Errorf("grace offset %v should not be negative", grace.Offset)
	}
	if grace.Velocity >= main.Velocity {
		t.Errorf("grace velocity %d should be quieter than main %d", grace.Velocity, main.Velocity)
	}
	if main.Offset != 0 || main.Velocity != 100 {
		t.Errorf("main hit changed: %+v", main)
	}
}

func TestExpandStep_flamGracePrecedesMidStepHit(t *testing.T) {
	// For a sub-hit NOT at the step start (ratchet places one at offset 0.5),
	// the grace genuinely lands before its main hit.
	s := Step{On: true, Velocity: 100, Probability: 100, Ratchet: 2, Flam: 1}
	hits := ExpandStep(s, 0, 0)
	// hits: [grace@0, main@0, grace<0.5, main@0.5]
	if len(hits) != 4 {
		t.Fatalf("ratchet 2 + flam => %d hits, want 4", len(hits))
	}
	grace2, main2 := hits[2], hits[3]
	if main2.Offset != 0.5 {
		t.Fatalf("second sub-hit main offset = %v, want 0.5", main2.Offset)
	}
	if grace2.Offset >= main2.Offset {
		t.Errorf("grace offset %v should precede main offset %v", grace2.Offset, main2.Offset)
	}
}

func TestExpandStep_flamWithRatchet(t *testing.T) {
	// flam + ratchet are distinct: each of the r sub-hits gets its own grace hit.
	s := Step{On: true, Velocity: 100, Probability: 100, Ratchet: 4, Flam: 3}
	hits := ExpandStep(s, 0, 0)
	if len(hits) != 8 { // 4 main + 4 grace
		t.Fatalf("ratchet 4 + flam => %d hits, want 8", len(hits))
	}
	// Hits come in (grace, main) pairs; mains land on 0,.25,.5,.75.
	mains := []float64{0, 0.25, 0.5, 0.75}
	for i := 0; i < 4; i++ {
		grace := hits[i*2]
		main := hits[i*2+1]
		if main.Offset != mains[i] {
			t.Errorf("sub-hit %d main offset %v, want %v", i, main.Offset, mains[i])
		}
		if grace.Offset >= main.Offset && main.Offset > 0 {
			t.Errorf("sub-hit %d grace %v not before main %v", i, grace.Offset, main.Offset)
		}
		if grace.Velocity >= main.Velocity {
			t.Errorf("sub-hit %d grace vel %d not quieter than main %d", i, grace.Velocity, main.Velocity)
		}
	}
}

func TestExpandStep_flamDeterministic(t *testing.T) {
	s := Step{On: true, Velocity: 77, Probability: 100, Ratchet: 2, Flam: 5}
	a := ExpandStep(s, 9, 3)
	b := ExpandStep(s, 9, 3)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("flam expansion not deterministic")
	}
}

func TestExpandStep_flamOffStepNoHits(t *testing.T) {
	if hits := ExpandStep(Step{On: false, Flam: 8, Ratchet: 4}, 0, 0); hits != nil {
		t.Errorf("OFF step with flam should expand to nil, got %v", hits)
	}
}

func TestFlamLead_monotonicTighter(t *testing.T) {
	// Higher Flam => grace sits CLOSER to the main hit (smaller lead).
	prev := flamLead(1)
	for f := 2; f <= MaxFlam; f++ {
		cur := flamLead(f)
		if cur >= prev {
			t.Errorf("flamLead(%d)=%v not < flamLead(%d)=%v (should tighten)", f, cur, f-1, prev)
		}
		prev = cur
	}
	if flamLead(0) != 0 {
		t.Errorf("flamLead(0) = %v, want 0 (no grace)", flamLead(0))
	}
	if flamLead(999) != flamLead(MaxFlam) {
		t.Error("flamLead should clamp above MaxFlam")
	}
}

func TestFlamGraceVelocity_audibleAndQuieter(t *testing.T) {
	if v := flamGraceVelocity(100); v <= 0 || v >= 100 {
		t.Errorf("grace vel %d not in (0,100)", v)
	}
	// Always at least the minimum audible value, even for a near-silent main hit.
	if v := flamGraceVelocity(1); v < flamMinGraceVel {
		t.Errorf("grace vel %d below min %d", v, flamMinGraceVel)
	}
}

func TestStep_normalizeClampsFlam(t *testing.T) {
	if s := (Step{Flam: 99}).normalize(); s.Flam != MaxFlam {
		t.Errorf("normalize flam high = %d, want %d", s.Flam, MaxFlam)
	}
	if s := (Step{Flam: -5}).normalize(); s.Flam != MinFlam {
		t.Errorf("normalize flam low = %d, want %d", s.Flam, MinFlam)
	}
}
