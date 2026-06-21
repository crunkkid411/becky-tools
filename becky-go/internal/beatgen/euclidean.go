package beatgen

// euclidean.go implements the Bjorklund / Euclidean rhythm generator: distribute
// `pulses` onsets as evenly as possible across `steps` slots, then rotate. This is
// the classic algorithm behind E(3,8) = x..x..x. and friends.

// Euclidean returns a length-`steps` boolean rhythm with `pulses` onsets spread as
// evenly as possible, then rotated left by `rotation` positions. Degrade rules:
//   - steps <= 0            => empty slice
//   - pulses <= 0           => all false
//   - pulses >= steps       => all true
//   - rotation is taken modulo steps (negative rotations wrap)
//
// The distribution is the true Bjorklund algorithm (recursive grouping of "fill"
// and "remainder" runs), which yields the canonical Euclidean patterns:
// E(3,8)=x..x..x., E(5,8)=x.x.x.xx is the rotation family; this implementation
// returns the standard onset-first form (E(3,8)=x..x..x., E(5,8)=x.xx.xx.).
func Euclidean(steps, pulses, rotation int) []bool {
	if steps <= 0 {
		return []bool{}
	}
	if steps > MaxSteps {
		steps = MaxSteps
	}
	if pulses <= 0 {
		return make([]bool, steps)
	}
	if pulses >= steps {
		out := make([]bool, steps)
		for i := range out {
			out[i] = true
		}
		return rotateBools(out, rotation)
	}
	return rotateBools(bjorklund(steps, pulses), rotation)
}

// bjorklund builds the canonical Euclidean onset pattern via the standard
// remainder-folding construction. It starts with `pulses` groups of [true] and
// `steps-pulses` groups of [false], then repeatedly distributes the smaller set of
// groups onto the larger until at most one remainder group is left. The
// concatenated groups are the rhythm, beginning on an onset.
func bjorklund(steps, pulses int) []bool {
	// a = the "onset" groups, b = the "rest" groups.
	a := make([][]bool, pulses)
	for i := range a {
		a[i] = []bool{true}
	}
	b := make([][]bool, steps-pulses)
	for i := range b {
		b[i] = []bool{false}
	}
	for len(b) > 1 {
		n := len(a)
		if len(b) < n {
			n = len(b)
		}
		var newA, newB [][]bool
		for i := 0; i < n; i++ {
			newA = append(newA, append(append([]bool(nil), a[i]...), b[i]...))
		}
		// Whichever side had leftovers becomes the new remainder set.
		if len(a) > n {
			newB = a[n:]
		} else {
			newB = b[n:]
		}
		a, b = newA, newB
	}
	out := make([]bool, 0, steps)
	for _, g := range a {
		out = append(out, g...)
	}
	for _, g := range b {
		out = append(out, g...)
	}
	return out
}

// rotateBools rotates a slice left by n positions, wrapping; negative n rotates
// right. The input is not mutated.
func rotateBools(in []bool, n int) []bool {
	l := len(in)
	if l == 0 {
		return []bool{}
	}
	n = ((n % l) + l) % l
	out := make([]bool, l)
	for i := 0; i < l; i++ {
		out[i] = in[(i+n)%l]
	}
	return out
}

// ApplyEuclidean returns a NEW pattern in which the named lane's ON pattern is
// replaced by a Euclidean rhythm of `pulses` onsets (rotated by `rotation`) across
// the lane's current step count. Newly-turned-on steps get default velocity/full
// probability/single ratchet; turned-off steps are cleared. Locked steps and a
// Locked lane are left untouched. An unknown lane name is a no-op (returns a copy).
func (p *Pattern) ApplyEuclidean(lane string, pulses, rotation int) *Pattern {
	out := p.Clone()
	i := out.laneIndex(lane)
	if i < 0 {
		return out
	}
	ln := &out.Lanes[i]
	if ln.Locked {
		return out
	}
	n := len(ln.Steps)
	mask := Euclidean(n, pulses, rotation)
	for s := 0; s < n; s++ {
		if ln.Steps[s].Locked {
			continue
		}
		setOn(&ln.Steps[s], mask[s])
	}
	return out
}

// setOn turns a step on (with sensible defaults when it was previously off/empty)
// or off (clearing velocity). It preserves an already-ON step's existing
// velocity/pitch/pan/probability/ratchet so re-deriving a mask is non-destructive.
func setOn(s *Step, on bool) {
	if on {
		if !s.On {
			defaults := NewStep()
			s.On = true
			s.Velocity = defaults.Velocity
			s.Probability = defaults.Probability
			s.Ratchet = defaults.Ratchet
		}
		return
	}
	s.On = false
	s.Velocity = 0
}
