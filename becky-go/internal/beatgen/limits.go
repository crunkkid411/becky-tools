package beatgen

import "math/rand"

// limits.go adds Playbeat-style per-parameter generation LIMITS: a Limits struct
// the generator respects so randomization only writes values inside a caller-set
// range. This mirrors Playbeat's "set the min/max for each parameter and let it
// roll the dice within those bounds" model. Like every generative op here it is
// pure, seeded, deterministic, immutable, and respects Locked lanes/steps.

// Range is an inclusive [Min,Max] integer bound for a randomized parameter. A
// zero Range (both 0) means "unset" — the generator uses a sensible default for
// that parameter instead of forcing everything to 0.
type Range struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// set reports whether the range was explicitly provided (not the zero value).
func (r Range) set() bool { return r.Min != 0 || r.Max != 0 }

// resolve normalizes the range to lo<=hi within [floor,ceil]; when unset it
// returns the supplied default range (also clamped).
func (r Range) resolve(defLo, defHi, floor, ceil int) (int, int) {
	lo, hi := r.Min, r.Max
	if !r.set() {
		lo, hi = defLo, defHi
	}
	lo = clamp(lo, floor, ceil)
	hi = clamp(hi, floor, ceil)
	if lo > hi {
		lo, hi = hi, lo
	}
	return lo, hi
}

// FloatRange is an inclusive [Min,Max] bound for the density (a 0..1 value). A
// zero FloatRange means "unset".
type FloatRange struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

func (r FloatRange) set() bool { return r.Min != 0 || r.Max != 0 }

// Limits bounds every randomized parameter a Generate pass may write. Any unset
// (zero) sub-range falls back to that parameter's documented default, so a
// zero-value Limits behaves like the plain default generator. StepRange caps how
// many onsets a lane may end up with after generation; Density (when set) is a
// per-pass target fill probability that overrides each lane's own Density.
type Limits struct {
	Velocity Range      `json:"velocity"` // [0,127]
	Pitch    Range      `json:"pitch"`    // semitone offsets (any sign)
	Pan      Range      `json:"pan"`      // [-100,100]
	Flam     Range      `json:"flam"`     // [0,8]
	Steps    Range      `json:"steps"`    // onset-count cap per lane, [0, lane length]
	Density  FloatRange `json:"density"`  // optional target fill probability override
}

// pitchFloor / pitchCeil bound generated pitch offsets to a musical-ish window.
const (
	pitchFloor = -24
	pitchCeil  = 24
)

// GenerateWithLimits is Generate constrained by per-parameter Limits. It fills
// every unlocked step of every unlocked lane with onsets according to the lane
// Density (or limits.Density when set) and the role weighting (when opts.RoleAware
// is set), but every randomized field it writes — velocity, pitch, pan, flam — is
// drawn from within the corresponding Limits range. After filling, StepRange caps
// the onset count per lane by removing the lowest-priority extra onsets (seeded,
// deterministic). Locked lanes and locked steps are never touched.
//
// Determinism: the seed fully determines the output. The pattern's own Seed field
// is left unchanged. A zero-value Limits reproduces DefaultGenerateOptions-style
// behavior for the unset parameters (velocity uses the opts band; pitch/pan stay
// 0; flam stays 0; no onset cap).
func (p *Pattern) GenerateWithLimits(opts GenerateOptions, limits Limits, seed int64) *Pattern {
	out := p.Clone()

	// Resolve parameter bounds once.
	defVMin, defVMax := velBand(opts)
	vlo, vhi := limits.Velocity.resolve(defVMin, defVMax, MinVelocity, MaxVelocity)
	if vlo < 1 {
		vlo = 1 // an ON step should be audible
	}
	if vhi < vlo {
		vhi = vlo
	}
	plo, phi := limits.Pitch.resolve(0, 0, pitchFloor, pitchCeil)
	panLo, panHi := limits.Pan.resolve(0, 0, MinPan, MaxPan)
	flo, fhi := limits.Flam.resolve(0, 0, MinFlam, MaxFlam)

	for li := range out.Lanes {
		ln := &out.Lanes[li]
		if ln.Locked {
			continue
		}
		lrng := rand.New(rand.NewSource(seed + int64(li)*2654435761))

		density := clampDensity(ln.Density)
		if limits.Density.set() {
			// A set density limit overrides the lane's own density with a value
			// drawn from the limited band (deterministic per lane).
			lo := clampDensity(limits.Density.Min)
			hi := clampDensity(limits.Density.Max)
			if lo > hi {
				lo, hi = hi, lo
			}
			if hi <= lo {
				density = lo
			} else {
				density = lo + lrng.Float64()*(hi-lo)
			}
		}

		for s := range ln.Steps {
			st := &ln.Steps[s]
			if st.Locked {
				continue
			}
			w := 1.0
			if opts.RoleAware {
				w = roleWeights(ln.Role, s)
			}
			if lrng.Float64() < density*w {
				st.On = true
				st.Velocity = randInRange(lrng, vlo, vhi)
				st.Pitch = randInRange(lrng, plo, phi)
				st.Pan = randInRange(lrng, panLo, panHi)
				st.Flam = randInRange(lrng, flo, fhi)
				st.Probability = MaxProbability
				if st.Ratchet < MinRatchet {
					st.Ratchet = MinRatchet
				}
			} else {
				st.On = false
				st.Velocity = 0
			}
		}

		if limits.Steps.set() {
			capLaneOnsets(ln, limits.Steps, seed+int64(li)*40503)
		}
	}
	return out
}

// randInRange returns an int uniformly in [lo,hi] (inclusive). lo>hi is treated
// as the single value lo (after a swap by the caller it should not occur).
func randInRange(rng *rand.Rand, lo, hi int) int {
	if hi <= lo {
		return lo
	}
	return lo + rng.Intn(hi-lo+1)
}

// capLaneOnsets enforces the StepRange onset-count cap on a lane: if the lane has
// more unlocked onsets than the resolved maximum, the excess (lowest-velocity
// first, ties broken by a seeded order) are turned off; locked steps are never
// touched and always count toward the total. A minimum is not force-filled (the
// generator already placed onsets) — StepRange.Min is honored only as a floor for
// the cap, i.e. the cap is never set below Min.
func capLaneOnsets(ln *Lane, r Range, seed int64) {
	lo := r.Min
	hi := r.Max
	if lo < 0 {
		lo = 0
	}
	if hi < lo {
		hi = lo
	}
	maxOn := hi

	// Collect currently-ON, unlocked step indices.
	var on []int
	locked := 0
	for s := range ln.Steps {
		if ln.Steps[s].On {
			if ln.Steps[s].Locked {
				locked++
				continue
			}
			on = append(on, s)
		}
	}
	// Locked onsets are immovable; the cap applies to what's left.
	budget := maxOn - locked
	if budget < 0 {
		budget = 0
	}
	if len(on) <= budget {
		return
	}
	// Deterministic priority: keep the loudest; drop the quietest. Seeded shuffle
	// first so equal velocities drop in a stable, seed-derived order.
	rng := rand.New(rand.NewSource(seed))
	for i := len(on) - 1; i > 0; i-- {
		j := rng.Intn(i + 1)
		on[i], on[j] = on[j], on[i]
	}
	// Stable sort by velocity descending (keep loud), preserving the shuffled
	// order for ties.
	insertionSortByVelDesc(ln, on)
	for _, s := range on[budget:] {
		ln.Steps[s].On = false
		ln.Steps[s].Velocity = 0
	}
}

// insertionSortByVelDesc sorts the indices in `idx` by their step velocity in
// descending order, stably (it is a small slice; an insertion sort keeps the
// caller's pre-shuffled tie order, which keeps the result deterministic).
func insertionSortByVelDesc(ln *Lane, idx []int) {
	for i := 1; i < len(idx); i++ {
		k := idx[i]
		kv := ln.Steps[k].Velocity
		j := i - 1
		for j >= 0 && ln.Steps[idx[j]].Velocity < kv {
			idx[j+1] = idx[j]
			j--
		}
		idx[j+1] = k
	}
}
