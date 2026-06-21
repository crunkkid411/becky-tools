package beatgen

import (
	"fmt"
	"math/rand"
)

// modes.go adds Playbeat's "Remix" (subtle variation) and "Infinity" (continuous
// regeneration on loop boundaries) behaviors, plus the Mode enum that selects how
// each Infinity tick re-rolls the pattern. All ops are pure, seeded, deterministic,
// immutable, and respect Locked lanes/steps.

// Mode selects how a generative refresh (InfinityTick) re-rolls the pattern.
type Mode int

const (
	// ModeRandom does a full re-fill (like Generate): a fresh pattern each tick.
	ModeRandom Mode = iota
	// ModeRemix keeps the current vibe and nudges it (like Remix): small per-step
	// toggles + velocity nudges around the CURRENT state.
	ModeRemix
	// ModeSmart re-fills using genre/role weighting biased toward musical onsets
	// (here: role-aware Generate with a musical velocity band).
	ModeSmart
)

// String renders a Mode for debugging/JSON-adjacent display.
func (m Mode) String() string {
	switch m {
	case ModeRandom:
		return "random"
	case ModeRemix:
		return "remix"
	case ModeSmart:
		return "smart"
	default:
		return fmt.Sprintf("mode(%d)", int(m))
	}
}

// Remix returns a NEW pattern that keeps the existing pattern's "vibe": with
// small probability (~amount) it toggles individual unlocked steps, and it nudges
// the velocities of surviving onsets by a small amount — all around the CURRENT
// state. It is deliberately GENTLER than Mutate and distinct from Generate:
//
//   - Generate redraws every step from scratch (the groove is replaced).
//   - Mutate perturbs the groove with a flip probability equal to `amount`, and
//     a flipped-on step gets a fresh full-band velocity (a notable change).
//   - Remix uses a SMALLER effective flip probability (amount scaled down) and,
//     critically, when it turns a step on it gives it a velocity tied to the
//     lane's existing onsets (a quiet ghost note), so the character is preserved
//     rather than redrawn. For the same amount it changes fewer steps than Mutate.
//
// amount is clamped to [0,1]; 0 returns an unchanged copy. Locked lanes and locked
// steps are never touched. Each lane uses an index-derived sub-stream for stability.
func (p *Pattern) Remix(amount float64, seed int64) *Pattern {
	out := p.Clone()
	if amount <= 0 {
		return out
	}
	if amount > 1 {
		amount = 1
	}
	// Remix is subtle: scale the toggle probability well below Mutate's so the
	// same `amount` produces a lighter touch.
	flipP := amount * 0.35

	for li := range out.Lanes {
		ln := &out.Lanes[li]
		if ln.Locked {
			continue
		}
		ghostVel := laneGhostVelocity(*ln)
		lrng := rand.New(rand.NewSource(seed + int64(li)*2246822519))
		for s := range ln.Steps {
			st := &ln.Steps[s]
			if st.Locked {
				continue
			}
			if lrng.Float64() < flipP {
				if st.On {
					st.On = false
					st.Velocity = 0
				} else {
					// A new onset enters as a quiet ghost note in keeping with the
					// lane's current loudness, not a fresh full-velocity hit.
					st.On = true
					st.Velocity = clampVelocity(maxInt2(1, ghostVel+lrng.Intn(11)-5))
					st.Probability = MaxProbability
					if st.Ratchet < MinRatchet {
						st.Ratchet = MinRatchet
					}
				}
				continue
			}
			if st.On {
				// gentle velocity nudge (+/- up to 6), smaller than Mutate's +/-12
				delta := lrng.Intn(13) - 6
				st.Velocity = clampVelocity(maxInt2(1, st.Velocity+delta))
			}
		}
	}
	return out
}

// laneGhostVelocity returns a representative quiet velocity for a lane: about 60%
// of the lane's average onset velocity (or a soft default when the lane is empty).
// Used by Remix so newly-introduced notes sit under the existing groove.
func laneGhostVelocity(ln Lane) int {
	sum, n := 0, 0
	for _, s := range ln.Steps {
		if s.On && s.Velocity > 0 {
			sum += s.Velocity
			n++
		}
	}
	if n == 0 {
		return DefaultVelocity * 60 / 100
	}
	avg := sum / n
	v := avg * 60 / 100
	if v < 1 {
		v = 1
	}
	return clampVelocity(v)
}

// InfinityTick implements Playbeat's "Infinity" continuous regeneration: called
// once per loop with the current loopIndex, it regenerates the pattern only on
// loop boundaries (when everyN > 0 and loopIndex % everyN == 0) using the selected
// Mode; otherwise it returns an unchanged copy.
//
// The per-tick seed is derived deterministically from (seed, loopIndex) so each
// regenerating loop differs from the last, yet the WHOLE sequence is reproducible:
// replaying from the same seed yields the same series of patterns. loopIndex < 0
// or everyN <= 0 returns an unchanged copy (degrade-never-crash). Locked lanes and
// steps are honored by the underlying Generate/Remix/Smart op.
func (p *Pattern) InfinityTick(loopIndex, everyN int, mode Mode, opts GenerateOptions, seed int64) *Pattern {
	if everyN <= 0 || loopIndex < 0 || loopIndex%everyN != 0 {
		return p.Clone()
	}
	tickSeed := infinitySeed(seed, loopIndex)
	switch mode {
	case ModeRemix:
		return p.Remix(0.25, tickSeed)
	case ModeSmart:
		smart := opts
		smart.RoleAware = true
		if smart.VelMin == 0 && smart.VelMax == 0 {
			smart.VelMin, smart.VelMax = 80, 120
		}
		return p.Generate(smart, tickSeed)
	default: // ModeRandom
		return p.Generate(opts, tickSeed)
	}
}

// infinitySeed folds the loop index into the base seed deterministically so each
// loop boundary gets its own reproducible sub-seed.
func infinitySeed(seed int64, loopIndex int) int64 {
	return seed*2862933555777941757 + int64(loopIndex)*3037000493 + 1
}
