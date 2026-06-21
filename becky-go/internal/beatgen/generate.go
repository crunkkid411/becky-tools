package beatgen

import "math/rand"

// generate.go holds the seeded generative operations: Generate (full fill),
// Mutate (small variation), and the role-aware weighting table that biases where
// onsets land. All randomness comes from an explicit seed; same seed => identical
// output. Locked lanes and locked steps are never touched.

// GenerateOptions tunes a Generate pass.
type GenerateOptions struct {
	// RoleAware applies the per-role positional weighting (kicks favor downbeats,
	// snares backbeats, hats every/offbeat). When false, every step uses the flat
	// lane Density.
	RoleAware bool
	// VelMin / VelMax bound randomized velocities for newly-placed onsets. When
	// both are zero, DefaultVelocity is used with a small band.
	VelMin int
	VelMax int
}

// DefaultGenerateOptions is the role-aware default with a musical velocity band.
func DefaultGenerateOptions() GenerateOptions {
	return GenerateOptions{RoleAware: true, VelMin: 80, VelMax: 120}
}

// roleWeights returns, for a role and a 16-step bar phase, a multiplier in [0,~2]
// applied to a lane's Density at each step. The table is deliberately simple and
// documented so the output is predictable:
//
//	kick   : strong on beats (steps 0,4,8,12), weak elsewhere
//	snare  : strong on backbeats (steps 4,12), weak elsewhere
//	hat    : even everywhere, slightly favoring offbeats (odd steps)
//	clap   : like snare (backbeats)
//	ride   : even, favoring on-beats
//	tom/perc/other : flat (weight 1)
//
// The phase is the step index taken modulo 16 so the weighting tiles across bars
// and works for any lane length.
func roleWeights(role string, phase int) float64 {
	p := ((phase % 16) + 16) % 16
	switch role {
	case "kick":
		switch p {
		case 0, 4, 8, 12:
			return 1.8
		case 2, 6, 10, 14:
			return 0.5
		default:
			return 0.2
		}
	case "snare", "clap":
		switch p {
		case 4, 12:
			return 1.9
		case 0, 8:
			return 0.15
		default:
			return 0.3
		}
	case "hat", "hihat":
		if p%2 == 1 { // offbeats
			return 1.3
		}
		return 1.0
	case "ride":
		if p%4 == 0 {
			return 1.3
		}
		return 1.0
	default:
		return 1.0
	}
}

// Generate returns a NEW pattern with every unlocked lane re-filled with onsets
// according to its Density (0..1 probability per step) and, when opts.RoleAware
// is set, the role weighting table. Velocities are randomized within the option
// band. Locked lanes and locked steps keep their existing value. Determinism: the
// passed seed fully determines the output; the pattern's own Seed field is left
// unchanged for the caller to record.
func (p *Pattern) Generate(opts GenerateOptions, seed int64) *Pattern {
	out := p.Clone()
	vmin, vmax := velBand(opts)
	// Give each lane an independent, index-derived sub-stream so locking or
	// reordering one lane never changes another lane's draws.
	for li := range out.Lanes {
		ln := &out.Lanes[li]
		if ln.Locked {
			continue
		}
		lrng := rand.New(rand.NewSource(seed + int64(li)*2654435761))
		density := clampDensity(ln.Density)
		for s := range ln.Steps {
			st := &ln.Steps[s]
			if st.Locked {
				continue
			}
			w := 1.0
			if opts.RoleAware {
				w = roleWeights(ln.Role, s)
			}
			pOn := density * w
			if lrng.Float64() < pOn {
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

// velBand resolves the velocity range, falling back to a band around the default.
func velBand(opts GenerateOptions) (int, int) {
	vmin, vmax := opts.VelMin, opts.VelMax
	if vmin == 0 && vmax == 0 {
		vmin, vmax = DefaultVelocity-15, DefaultVelocity+15
	}
	vmin = clampVelocity(vmin)
	vmax = clampVelocity(vmax)
	if vmin > vmax {
		vmin, vmax = vmax, vmin
	}
	if vmin < 1 {
		vmin = 1 // an ON step should be audible
	}
	return vmin, vmax
}

// randVel returns a velocity uniformly in [lo,hi].
func randVel(rng *rand.Rand, lo, hi int) int {
	if hi <= lo {
		return clampVelocity(lo)
	}
	return clampVelocity(lo + rng.Intn(hi-lo+1))
}

// clampDensity constrains a density to [0,1].
func clampDensity(d float64) float64 {
	if d < 0 {
		return 0
	}
	if d > 1 {
		return 1
	}
	return d
}

// Mutate returns a NEW pattern with a SMALL seeded variation: a fraction (~amount)
// of unlocked steps in unlocked lanes are flipped on<->off, and the velocities of
// surviving onsets are nudged. amount is clamped to [0,1]; 0 returns an unchanged
// copy. This is distinct from Generate — it perturbs the existing groove rather
// than regenerating it. Each lane uses an index-derived sub-stream for stability.
func (p *Pattern) Mutate(amount float64, seed int64) *Pattern {
	out := p.Clone()
	if amount <= 0 {
		return out
	}
	if amount > 1 {
		amount = 1
	}
	for li := range out.Lanes {
		ln := &out.Lanes[li]
		if ln.Locked {
			continue
		}
		lrng := rand.New(rand.NewSource(seed + int64(li)*40503))
		for s := range ln.Steps {
			st := &ln.Steps[s]
			if st.Locked {
				continue
			}
			if lrng.Float64() < amount {
				// flip
				if st.On {
					st.On = false
					st.Velocity = 0
				} else {
					st.On = true
					st.Velocity = randVel(lrng, DefaultVelocity-15, DefaultVelocity+15)
					st.Probability = MaxProbability
					if st.Ratchet < MinRatchet {
						st.Ratchet = MinRatchet
					}
				}
				continue
			}
			if st.On {
				// nudge velocity by +/- up to 12, staying audible
				delta := lrng.Intn(25) - 12
				st.Velocity = clampVelocity(maxInt2(1, st.Velocity+delta))
			}
		}
	}
	return out
}

func maxInt2(a, b int) int {
	if a > b {
		return a
	}
	return b
}
