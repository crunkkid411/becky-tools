package beatgen

import (
	"math/rand"
	"sort"
	"strings"
)

// genre.go adds Playbeat's "Smart" / genre-based generation: a small set of
// hand-tuned GenreProfiles that bias lane densities, onset placement, and the
// default swing toward a named style. These are WEIGHTED-PROBABILITY profiles —
// honest priors a human picked, NOT a trained model and not a guarantee that the
// output "is" the genre. They simply nudge the existing seeded generator so a
// trap pattern tends sparse-kick + busy-hat, a house pattern tends four-on-the-
// floor, etc. Everything stays pure, seeded, deterministic, and immutable.

// GenreProfile is the per-genre bias used by GenerateGenre. It carries:
//
//   - RoleDensity: a target fill probability per role (kick/snare/hat/...). A
//     role not listed falls back to DefaultRoleDensity.
//   - Placement: a per-role, 16-phase multiplier applied ON TOP of the built-in
//     roleWeights, so a genre can favor specific steps (e.g. house puts a kick on
//     every quarter). A nil/short slice means "use roleWeights unchanged".
//   - Swing: the default swing this genre applies to the generated pattern.
//
// Profiles are honest priors, not a model: same genre + same seed => identical
// output.
type GenreProfile struct {
	Name        string
	RoleDensity map[string]float64
	Placement   map[string][16]float64
	Swing       float64
}

// DefaultRoleDensity is used for any role a profile does not specify.
const DefaultRoleDensity = 0.3

// densityFor returns the profile's target density for a role, or the default.
func (g GenreProfile) densityFor(role string) float64 {
	if d, ok := g.RoleDensity[canonRole(role)]; ok {
		return clampDensity(d)
	}
	return DefaultRoleDensity
}

// placementFor returns the profile's per-step placement multiplier for a role at
// a 16-phase, or 1.0 when the profile has no placement override for that role.
func (g GenreProfile) placementFor(role string, phase int) float64 {
	if g.Placement == nil {
		return 1.0
	}
	tbl, ok := g.Placement[canonRole(role)]
	if !ok {
		return 1.0
	}
	return tbl[((phase%16)+16)%16]
}

// canonRole folds role aliases so profiles can key on one name (hat covers
// hat/hihat; clap and snare share the backbeat family for lookup of density).
func canonRole(role string) string {
	switch role {
	case "hihat":
		return "hat"
	default:
		return role
	}
}

// fourOnFloor is a placement table that strongly favors the quarter-note
// downbeats (steps 0,4,8,12) — the house/techno kick signature.
func fourOnFloor() [16]float64 {
	var t [16]float64
	for i := range t {
		t[i] = 0.05
	}
	for _, b := range []int{0, 4, 8, 12} {
		t[b] = 2.2
	}
	return t
}

// genreProfiles is the shipped profile table (Go data, deterministic map literal).
// Lookup is case-insensitive and alias-folded by GenreProfileFor; unknown genres
// fall back to "straight". Keep this list honest and small.
var genreProfiles = map[string]GenreProfile{
	"straight": {
		Name: "straight",
		RoleDensity: map[string]float64{
			"kick": 0.45, "snare": 0.4, "hat": 0.6, "clap": 0.4,
			"ride": 0.4, "tom": 0.2, "perc": 0.25,
		},
		Swing: 0,
	},
	"trap": {
		Name: "trap",
		RoleDensity: map[string]float64{
			// Sparse, syncopated kick; backbeat snare/clap; busy, rolling hats.
			"kick": 0.3, "snare": 0.35, "clap": 0.35, "hat": 0.85,
			"ride": 0.2, "tom": 0.15, "perc": 0.2,
		},
		Swing: 0.12,
	},
	"house": {
		Name: "house",
		RoleDensity: map[string]float64{
			"kick": 0.95, "snare": 0.35, "clap": 0.5, "hat": 0.7,
			"ride": 0.3, "tom": 0.15, "perc": 0.3,
		},
		Placement: map[string][16]float64{
			"kick": fourOnFloor(),
		},
		Swing: 0.08,
	},
	"dnb": {
		Name: "dnb",
		RoleDensity: map[string]float64{
			// Breakbeat: syncopated kick, snare on the 2-and-4 backbeats, busy hats.
			"kick": 0.4, "snare": 0.45, "clap": 0.3, "hat": 0.8,
			"ride": 0.3, "tom": 0.25, "perc": 0.35,
		},
		Swing: 0.05,
	},
}

// genreAliases maps common spellings/synonyms onto a canonical profile key.
var genreAliases = map[string]string{
	"hiphop":        "trap",
	"hip-hop":       "trap",
	"hip hop":       "trap",
	"techno":        "house",
	"fourfloor":     "house",
	"four-floor":    "house",
	"drumandbass":   "dnb",
	"drum-and-bass": "dnb",
	"dnb":           "dnb",
	"breakbeat":     "dnb",
	"breaks":        "dnb",
	"default":       "straight",
	"":              "straight",
}

// GenreProfileFor returns the profile for a genre name (case-insensitive, alias-
// folded). An unknown genre degrades to the "straight" default — never an error.
func GenreProfileFor(genre string) GenreProfile {
	key := strings.ToLower(strings.TrimSpace(genre))
	if canon, ok := genreAliases[key]; ok {
		key = canon
	}
	if g, ok := genreProfiles[key]; ok {
		return g
	}
	return genreProfiles["straight"]
}

// GenreNames returns the canonical profile names, sorted, for discovery/UX.
func GenreNames() []string {
	out := make([]string, 0, len(genreProfiles))
	for k := range genreProfiles {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// GenerateGenre returns a NEW pattern generated with the named genre's biases:
// each unlocked lane's fill probability is the genre's per-role target density,
// scaled by the built-in roleWeights AND the genre's per-role placement table,
// and the pattern's Swing is set to the genre default. Velocities use a musical
// band. Unknown genre => the "straight" default profile (degrade, no error).
//
// This is a thin, honest bias over the existing seeded generator — same genre +
// same seed => identical output. Locked lanes and locked steps are untouched, and
// the input pattern is not mutated.
func (p *Pattern) GenerateGenre(genre string, seed int64) *Pattern {
	prof := GenreProfileFor(genre)
	out := p.Clone()
	out.Swing = prof.Swing
	vmin, vmax := velBand(GenerateOptions{VelMin: 84, VelMax: 118})

	for li := range out.Lanes {
		ln := &out.Lanes[li]
		if ln.Locked {
			continue
		}
		lrng := rand.New(rand.NewSource(seed + int64(li)*2654435761))
		base := prof.densityFor(ln.Role)
		for s := range ln.Steps {
			st := &ln.Steps[s]
			if st.Locked {
				continue
			}
			w := roleWeights(ln.Role, s) * prof.placementFor(ln.Role, s)
			if lrng.Float64() < base*w {
				st.On = true
				st.Velocity = randVel(lrng, vmin, vmax)
				st.Probability = MaxProbability
				if st.Ratchet < MinRatchet {
					st.Ratchet = MinRatchet
				}
			} else {
				st.On = false
				st.Velocity = 0
			}
		}
	}
	return out
}
