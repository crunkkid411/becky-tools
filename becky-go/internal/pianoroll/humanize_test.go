package pianoroll

import "testing"

// humClip builds a quantized 8-note run (all on grid) so humanize has something to
// nudge off the grid.
func humClip() *Clip {
	c := NewClip(480)
	for i := 0; i < 8; i++ {
		c = c.Add(Note{Pitch: 60 + i, Start: i * 120, Length: 120, Velocity: 96, Channel: 0})
	}
	return c
}

// TestHumanize_deterministicSameSeed: same seed => byte-identical result.
func TestHumanize_deterministicSameSeed(t *testing.T) {
	a := humClip().Humanize(nil, HumanizeOpts{Seed: 7})
	b := humClip().Humanize(nil, HumanizeOpts{Seed: 7})
	if !notesEqual(a.Notes, b.Notes) {
		t.Errorf("same seed gave different output:\n%v\n%v", a.Notes, b.Notes)
	}
}

// TestHumanize_differsBySeed: different seeds generally produce a different result.
func TestHumanize_differsBySeed(t *testing.T) {
	a := humClip().Humanize(nil, HumanizeOpts{Seed: 1})
	b := humClip().Humanize(nil, HumanizeOpts{Seed: 2})
	if notesEqual(a.Notes, b.Notes) {
		t.Error("seeds 1 and 2 produced identical output (expected variation)")
	}
}

// TestHumanize_defaultSeedStable: the documented DefaultSeed path is reproducible
// and matches an explicit Seed:DefaultSeed call.
func TestHumanize_defaultSeedStable(t *testing.T) {
	a := humClip().Humanize(nil, HumanizeOpts{})                  // 0 => DefaultSeed
	b := humClip().Humanize(nil, HumanizeOpts{Seed: DefaultSeed}) // explicit
	if !notesEqual(a.Notes, b.Notes) {
		t.Error("default-seed path differs from explicit DefaultSeed")
	}
}

// TestHumanize_boundsRespected: humanized values stay within their configured
// spread and never leave MIDI range; starts never go negative.
func TestHumanize_boundsRespected(t *testing.T) {
	base := humClip()
	const tspread, vspread = 20, 15
	out := base.Humanize(nil, HumanizeOpts{TimingTicks: tspread, VelSpread: vspread, Seed: 99})
	for i := range out.Notes {
		got := out.Notes[i]
		orig := base.Notes[i]
		if got.Start < 0 {
			t.Errorf("note %d start went negative: %d", i, got.Start)
		}
		if dt := abs(got.Start - orig.Start); dt > tspread {
			t.Errorf("note %d timing moved %d, > spread %d", i, dt, tspread)
		}
		if dv := abs(got.Velocity - orig.Velocity); dv > vspread {
			t.Errorf("note %d velocity moved %d, > spread %d", i, dv, vspread)
		}
		if got.Velocity < 1 || got.Velocity > 127 {
			t.Errorf("note %d velocity out of MIDI range: %d", i, got.Velocity)
		}
	}
}

// TestHumanize_disableAxes: a negative amount disables that axis entirely.
func TestHumanize_disableAxes(t *testing.T) {
	base := humClip()
	// Disable timing; only velocity should move.
	out := base.Humanize(nil, HumanizeOpts{TimingTicks: -1, VelSpread: 10, Seed: 5})
	for i := range out.Notes {
		if out.Notes[i].Start != base.Notes[i].Start {
			t.Errorf("note %d start moved despite timing disabled", i)
		}
	}
	// Disable velocity; only timing should move.
	out2 := base.Humanize(nil, HumanizeOpts{TimingTicks: 10, VelSpread: -1, Seed: 5})
	for i := range out2.Notes {
		if out2.Notes[i].Velocity != base.Notes[i].Velocity {
			t.Errorf("note %d velocity moved despite velocity disabled", i)
		}
	}
}

// TestHumanize_streamAnchoredToClip: humanizing one note draws the same offset that
// note would receive in a full-clip humanize (the random stream is anchored to the
// clip, not the selection).
func TestHumanize_streamAnchoredToClip(t *testing.T) {
	base := humClip()
	full := base.Humanize(nil, HumanizeOpts{Seed: 3})
	// Find the index of note pitch 63 in the sorted clip (it's index 3 by start).
	idx := 3
	single := base.Humanize([]int{idx}, HumanizeOpts{Seed: 3})
	if single.Notes[idx] != full.Notes[idx] {
		t.Errorf("selected note %d differs: single %+v vs full %+v",
			idx, single.Notes[idx], full.Notes[idx])
	}
	// And the unselected notes are untouched in the single-note humanize.
	for i := range base.Notes {
		if i == idx {
			continue
		}
		if single.Notes[i] != base.Notes[i] {
			t.Errorf("unselected note %d changed in single-note humanize", i)
		}
	}
}

// TestHumanize_immutable: the receiver is not mutated.
func TestHumanize_immutable(t *testing.T) {
	base := humClip()
	snap := append([]Note(nil), base.Notes...)
	_ = base.Humanize(nil, HumanizeOpts{Seed: 11})
	if !notesEqual(base.Notes, snap) {
		t.Error("Humanize mutated the receiver")
	}
}

// --- helpers ---

func notesEqual(a, b []Note) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
