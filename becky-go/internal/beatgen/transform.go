package beatgen

import (
	"math/rand"
	"sort"
)

// transform.go holds the non-(re)generating shaping operations: density set/adjust,
// rotation, and lane-level mute/solo/lock toggles. All return a NEW pattern and
// respect Locked lanes/steps.

// SetDensity returns a NEW pattern with the named lane's Density field set to d
// (clamped to [0,1]). It records intent for the next Generate; it does NOT itself
// add or remove hits. A Locked lane is unchanged; an unknown name is a no-op.
func (p *Pattern) SetDensity(lane string, d float64) *Pattern {
	out := p.Clone()
	i := out.laneIndex(lane)
	if i < 0 || out.Lanes[i].Locked {
		return out
	}
	out.Lanes[i].Density = clampDensity(d)
	return out
}

// Busier returns a NEW pattern with roughly `count` additional onsets turned on in
// the named lane, chosen from currently-OFF, unlocked steps. Selection is seeded
// and deterministic. If fewer eligible steps exist than requested, all are turned
// on. A Locked lane / unknown name is a no-op.
func (p *Pattern) Busier(lane string, count int, seed int64) *Pattern {
	return p.adjustDensity(lane, count, true, seed)
}

// Sparser returns a NEW pattern with roughly `count` onsets removed from the named
// lane, chosen from currently-ON, unlocked steps. Seeded and deterministic.
func (p *Pattern) Sparser(lane string, count int, seed int64) *Pattern {
	return p.adjustDensity(lane, count, false, seed)
}

// adjustDensity adds (add=true) or removes (add=false) up to count onsets among
// eligible steps of a lane. Eligible = unlocked AND (off when adding / on when
// removing). The chosen indices are a seeded shuffle, so the result is stable.
func (p *Pattern) adjustDensity(lane string, count int, add bool, seed int64) *Pattern {
	out := p.Clone()
	i := out.laneIndex(lane)
	if i < 0 || out.Lanes[i].Locked || count <= 0 {
		return out
	}
	ln := &out.Lanes[i]
	var eligible []int
	for s := range ln.Steps {
		st := ln.Steps[s]
		if st.Locked {
			continue
		}
		if add && !st.On {
			eligible = append(eligible, s)
		} else if !add && st.On {
			eligible = append(eligible, s)
		}
	}
	pick := seededPick(eligible, count, seed)
	for _, s := range pick {
		setOn(&ln.Steps[s], add)
		if add {
			ln.Steps[s].Velocity = DefaultVelocity
		}
	}
	return out
}

// seededPick returns up to n elements of in, selected by a deterministic shuffle
// keyed on seed. The returned indices are sorted ascending for stable application.
func seededPick(in []int, n int, seed int64) []int {
	if n >= len(in) {
		out := append([]int(nil), in...)
		sort.Ints(out)
		return out
	}
	if n <= 0 {
		return nil
	}
	pool := append([]int(nil), in...)
	rng := rand.New(rand.NewSource(seed))
	// Fisher-Yates partial shuffle.
	for i := 0; i < n; i++ {
		j := i + rng.Intn(len(pool)-i)
		pool[i], pool[j] = pool[j], pool[i]
	}
	out := append([]int(nil), pool[:n]...)
	sort.Ints(out)
	return out
}

// Rotate returns a NEW pattern with the named lane's steps rotated by n positions
// (positive = left/earlier, negative = right/later), wrapping. A fully Locked lane
// is unchanged. Individual step Locked flags travel with their step (the whole
// Step struct moves), which is the intuitive behavior for a shift. An unknown name
// is a no-op.
func (p *Pattern) Rotate(lane string, n int) *Pattern {
	out := p.Clone()
	i := out.laneIndex(lane)
	if i < 0 || out.Lanes[i].Locked {
		return out
	}
	ln := &out.Lanes[i]
	ln.Steps = rotateSteps(ln.Steps, n)
	return out
}

// rotateSteps rotates a step slice left by n, wrapping; negative n rotates right.
func rotateSteps(in []Step, n int) []Step {
	l := len(in)
	if l == 0 {
		return []Step{}
	}
	n = ((n % l) + l) % l
	out := make([]Step, l)
	for i := 0; i < l; i++ {
		out[i] = in[(i+n)%l]
	}
	return out
}

// SetStep returns a NEW pattern with one cell of a lane set. on=true with vel<=0
// uses DefaultVelocity; on=false clears the velocity. A Locked step or Locked lane
// is left untouched. Out-of-range indices / unknown lane are no-ops.
func (p *Pattern) SetStep(lane string, idx int, on bool, vel int) *Pattern {
	out := p.Clone()
	i := out.laneIndex(lane)
	if i < 0 || out.Lanes[i].Locked {
		return out
	}
	ln := &out.Lanes[i]
	if idx < 0 || idx >= len(ln.Steps) || ln.Steps[idx].Locked {
		return out
	}
	st := &ln.Steps[idx]
	st.On = on
	if on {
		if vel <= 0 {
			vel = DefaultVelocity
		}
		st.Velocity = clampVelocity(vel)
		if st.Probability == 0 {
			st.Probability = MaxProbability
		}
		if st.Ratchet < MinRatchet {
			st.Ratchet = MinRatchet
		}
	} else {
		st.Velocity = 0
	}
	return out
}

// SetLaneLock returns a NEW pattern with the named lane's Locked flag set.
func (p *Pattern) SetLaneLock(lane string, locked bool) *Pattern {
	out := p.Clone()
	if i := out.laneIndex(lane); i >= 0 {
		out.Lanes[i].Locked = locked
	}
	return out
}

// SetStepLock returns a NEW pattern with one step's Locked flag set. Note: locking
// a step is itself always permitted (it is a lock operation, not a content edit),
// but it is ignored if the lane index/step index is invalid.
func (p *Pattern) SetStepLock(lane string, idx int, locked bool) *Pattern {
	out := p.Clone()
	i := out.laneIndex(lane)
	if i < 0 {
		return out
	}
	ln := &out.Lanes[i]
	if idx < 0 || idx >= len(ln.Steps) {
		return out
	}
	ln.Steps[idx].Locked = locked
	return out
}
