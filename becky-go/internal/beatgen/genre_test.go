package beatgen

import (
	"reflect"
	"testing"
)

func genrePattern() *Pattern {
	return NewPattern(16,
		Lane{Name: "kick", Role: "kick"},
		Lane{Name: "snare", Role: "snare"},
		Lane{Name: "hat", Role: "hat"},
	)
}

func TestGenerateGenre_deterministicAndImmutable(t *testing.T) {
	p := genrePattern()
	a := p.GenerateGenre("house", 42)
	b := p.GenerateGenre("house", 42)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("GenerateGenre not deterministic")
	}
	// input untouched
	for _, ln := range p.Lanes {
		for _, s := range ln.Steps {
			if s.On {
				t.Fatal("GenerateGenre mutated the input pattern")
			}
		}
	}
}

func TestGenerateGenre_unknownDegradesToStraight(t *testing.T) {
	p := genrePattern()
	unknown := p.GenerateGenre("zzz-not-a-genre", 7)
	straight := p.GenerateGenre("straight", 7)
	if !reflect.DeepEqual(unknown, straight) {
		t.Error("unknown genre should degrade to the straight profile")
	}
}

func TestGenerateGenre_houseFourOnFloor(t *testing.T) {
	// House biases the kick strongly toward downbeats 0,4,8,12. Across many seeds
	// those steps should fire far more than offbeats.
	down, off := 0, 0
	for seed := int64(0); seed < 200; seed++ {
		g := genrePattern().GenerateGenre("house", seed)
		kick := g.Lanes[0]
		for _, b := range []int{0, 4, 8, 12} {
			if kick.Steps[b].On {
				down++
			}
		}
		for _, o := range []int{1, 3, 5, 7, 9, 11, 13, 15} {
			if kick.Steps[o].On {
				off++
			}
		}
	}
	// 4 downbeat slots vs 8 offbeat slots; the four-on-the-floor bias should still
	// make per-slot downbeat hits dominate offbeats outright.
	if down <= off {
		t.Errorf("house kick not four-on-the-floor: downbeats=%d offbeats=%d", down, off)
	}
}

func TestGenerateGenre_trapBusyHats(t *testing.T) {
	// Trap profile gives hats a much higher density than the kick.
	hatOn, kickOn := 0, 0
	for seed := int64(0); seed < 100; seed++ {
		g := genrePattern().GenerateGenre("trap", seed)
		hatOn += onCount(g.Lanes[2])
		kickOn += onCount(g.Lanes[0])
	}
	if hatOn <= kickOn {
		t.Errorf("trap hats should be busier than kick: hats=%d kick=%d", hatOn, kickOn)
	}
}

func TestGenerateGenre_setsSwing(t *testing.T) {
	g := genrePattern().GenerateGenre("trap", 1)
	if g.Swing != GenreProfileFor("trap").Swing {
		t.Errorf("genre swing not applied: got %v want %v", g.Swing, GenreProfileFor("trap").Swing)
	}
	if straight := genrePattern().GenerateGenre("straight", 1); straight.Swing != 0 {
		t.Errorf("straight swing should be 0, got %v", straight.Swing)
	}
}

func TestGenerateGenre_respectsLocks(t *testing.T) {
	p := genrePattern()
	p = p.SetStep("snare", 0, true, 33)
	p = p.SetLaneLock("snare", true)
	p = p.SetStepLock("kick", 7, true)
	g := p.GenerateGenre("house", 99)
	if !g.Lanes[1].Steps[0].On || g.Lanes[1].Steps[0].Velocity != 33 {
		t.Error("locked lane content changed by GenerateGenre")
	}
	if g.Lanes[0].Steps[7].On {
		t.Error("locked step turned on by GenerateGenre")
	}
}

func TestGenreProfileFor_aliases(t *testing.T) {
	cases := map[string]string{
		"hiphop":      "trap",
		"HIP-HOP":     "trap",
		"techno":      "house",
		"drumandbass": "dnb",
		"breakbeat":   "dnb",
		"":            "straight",
		"nonsense":    "straight",
		"  House  ":   "house",
	}
	for in, want := range cases {
		if got := GenreProfileFor(in).Name; got != want {
			t.Errorf("GenreProfileFor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGenreNames_sorted(t *testing.T) {
	names := GenreNames()
	if len(names) == 0 {
		t.Fatal("GenreNames returned nothing")
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Errorf("GenreNames not sorted: %v", names)
		}
	}
}

func TestGenreProfile_densityAndPlacementFallback(t *testing.T) {
	g := GenreProfileFor("straight")
	// an unlisted role uses the default density
	if g.densityFor("nonexistent-role") != DefaultRoleDensity {
		t.Error("unlisted role should use DefaultRoleDensity")
	}
	// no placement table => multiplier 1
	if g.placementFor("kick", 0) != 1.0 {
		t.Error("straight has no kick placement override; expected 1.0")
	}
	// house has a kick placement table favoring the downbeat
	h := GenreProfileFor("house")
	if h.placementFor("kick", 0) <= h.placementFor("kick", 1) {
		t.Error("house kick placement should favor the downbeat over step 1")
	}
}
