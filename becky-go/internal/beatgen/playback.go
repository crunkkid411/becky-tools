package beatgen

import (
	"hash/fnv"
	"math/rand"
)

// playback.go turns the static pattern into the concrete, deterministic events a
// player would sound: lane traversal order (Direction), polymeter resolution
// (which lane-step plays at a given global step), per-step probability+ratchet
// expansion, and swing timing. None of this produces audio — it produces the data
// a future audio player consumes, and it is fully reproducible per seed.

// StepOrder returns the deterministic index order for ONE cycle of the lane's
// effective Length, given its Direction:
//
//	Forward  : 0,1,...,n-1
//	Reverse  : n-1,...,1,0
//	PingPong : 0,1,...,n-1,n-2,...,1  (endpoints not repeated; length 2n-2 for n>=2)
//	Random   : a seeded permutation of 0..n-1 (uses the pattern Seed via the lane)
//
// For Random, the order is seeded by the pattern's Seed combined with the lane
// index so different lanes differ but every run is identical. Because a Lane has
// no back-reference to its Pattern, the seed is taken from the `seed` argument;
// callers in this package pass Pattern.Seed. n<=0 returns nil; n==1 returns [0].
func (l Lane) StepOrder(seed int64) []int {
	n := l.effLength()
	if n <= 0 {
		return nil
	}
	if n == 1 {
		return []int{0}
	}
	switch l.Direction {
	case Reverse:
		out := make([]int, n)
		for i := 0; i < n; i++ {
			out[i] = n - 1 - i
		}
		return out
	case PingPong:
		out := make([]int, 0, 2*n-2)
		for i := 0; i < n; i++ {
			out = append(out, i)
		}
		for i := n - 2; i >= 1; i-- {
			out = append(out, i)
		}
		return out
	case Random:
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		rng := rand.New(rand.NewSource(seed ^ int64(stableHash(l.Name))))
		for i := n - 1; i > 0; i-- {
			j := rng.Intn(i + 1)
			out[i], out[j] = out[j], out[i]
		}
		return out
	default: // Forward
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		return out
	}
}

// stableHash gives a deterministic 32-bit hash of a string (used to derive a
// per-lane random seed that does not depend on slice index/order).
func stableHash(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

// StepAt resolves which Step of the lane plays at the given GLOBAL step index,
// honoring polymeter (the lane loops at its effective Length, independent of the
// global pattern length) and Direction. This is the load-bearing polymeter math:
// the lane's traversal order is computed once per cycle and indexed by the global
// position. A muted lane still resolves a step here (muting is a mix decision the
// player applies); an empty lane returns the zero Step and ok=false.
func (l Lane) StepAt(globalStep int, seed int64) (Step, bool) {
	order := l.StepOrder(seed)
	if len(order) == 0 {
		return Step{}, false
	}
	cycle := len(order)
	pos := ((globalStep % cycle) + cycle) % cycle
	idx := order[pos]
	if idx < 0 || idx >= len(l.Steps) {
		return Step{}, false
	}
	return l.Steps[idx], true
}

// Hit is one concrete sounded event produced from a Step: an offset within the
// step's time slot (0..1, where 0 = the step's start and 1 = the next step) and a
// velocity. Ratcheting yields several Hits; probability may yield none.
type Hit struct {
	Offset   float64 `json:"offset"`   // 0..1 within the step
	Velocity int     `json:"velocity"` // 0..127
}

// ExpandStep turns one Step into the concrete Hits it produces at a given global
// step, deterministically:
//
//   - If the step is OFF, it produces no hits.
//   - Probability (0..100) gates the WHOLE step: a deterministic draw keyed on
//     (seed, globalStep) decides if it fires this cycle. 100 always fires, 0 never.
//   - Ratchet (1..8) subdivides the step into evenly-spaced sub-hits at offsets
//     0, 1/r, 2/r, ... (r-1)/r, each carrying the step's velocity.
//   - Flam (0..8) adds a SINGLE quieter grace hit just AHEAD of the main hit
//     (and ahead of each ratchet sub-hit) — see flamHits. Flam and Ratchet are
//     distinct: ratchet = N evenly-spaced full-velocity repeats, flam = one soft
//     grace note. A zero Flam adds nothing, so the output is byte-identical to
//     the pre-flam engine.
//
// The probability draw is per (seed, globalStep) so the same beat plays back
// identically every time, and two different steps at the same global position
// using the same seed share the gate decision-free space via the lane-independent
// key (callers can fold the lane in by varying seed if independent gates are
// wanted; StepAt+ExpandStep as used by a player pass Pattern.Seed plus a lane salt).
func ExpandStep(s Step, seed int64, globalStep int) []Hit {
	if !s.On {
		return nil
	}
	s = s.normalize()
	if s.Probability < MaxProbability {
		if s.Probability <= MinProbability {
			return nil
		}
		// Deterministic gate in [0,100).
		g := probGate(seed, globalStep)
		if g >= s.Probability {
			return nil
		}
	}
	r := s.Ratchet
	if r < MinRatchet {
		r = MinRatchet
	}
	hits := make([]Hit, 0, r)
	for i := 0; i < r; i++ {
		base := float64(i) / float64(r)
		// Grace hit (if any) is emitted just before the main sub-hit so a player
		// sounds it first; it carries a reduced, deterministic velocity.
		hits = append(hits, flamHits(s, base, 1.0/float64(r))...)
		hits = append(hits, Hit{
			Offset:   base,
			Velocity: s.Velocity,
		})
	}
	return hits
}

// probGate returns a deterministic integer in [0,100) for the (seed, globalStep)
// pair. A step with Probability p fires when gate < p.
func probGate(seed int64, globalStep int) int {
	rng := rand.New(rand.NewSource(seed*2862933555777941757 + int64(globalStep)*3037000493 + 1))
	return rng.Intn(100)
}

// SwingOffset returns the timing offset (in fractions of a step, where 1.0 = one
// full step) to apply to the step at stepIndex given a swing amount.
//
// Convention: swing is in [0,1]. swing == 0 is perfectly straight (every step
// returns 0). For swing > 0, the EVEN steps (0,2,4,...) stay put and the ODD steps
// (1,3,5,... — the "and" of each beat in 1/16 terms) are delayed. The maximum
// delay is half a step (0.5) at swing == 1, so swing 0.5 delays odd steps by 0.25
// of a step (a common, musical triplet-ish feel). Negative swing is clamped to 0;
// swing > 1 is clamped to 1.
func SwingOffset(stepIndex int, swing float64) float64 {
	if swing <= 0 {
		return 0
	}
	if swing > 1 {
		swing = 1
	}
	if ((stepIndex%2)+2)%2 == 0 {
		return 0 // on-beat, no delay
	}
	return 0.5 * swing
}

// LaneStepOffset returns the total timing offset (in fractions of a step) to
// apply to the lane's step at stepIndex, honoring the lane's OWN swing override
// (Lane.Swing, falling back to globalSwing when zero) plus its constant
// TrackDelay micro-offset. This is the per-lane timing the playback side should
// use instead of the bare SwingOffset when lanes carry their own feel.
//
// A lane with zero Swing and zero TrackDelay returns exactly SwingOffset(
// stepIndex, globalSwing) — i.e. existing patterns are unaffected.
func (l Lane) LaneStepOffset(stepIndex int, globalSwing float64) float64 {
	return SwingOffset(stepIndex, l.effSwing(globalSwing)) + l.effTrackDelay()
}

// StepOffset is the Pattern-level convenience: the timing offset for the named
// lane's step, combining the lane's swing override (or the pattern's global
// Swing) with the lane's TrackDelay. An unknown lane falls back to the pattern's
// global swing with no track delay.
func (p *Pattern) StepOffset(lane string, stepIndex int) float64 {
	if i := p.laneIndex(lane); i >= 0 {
		return p.Lanes[i].LaneStepOffset(stepIndex, p.Swing)
	}
	return SwingOffset(stepIndex, p.Swing)
}
