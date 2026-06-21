// Package beatgen is the deterministic generative-rhythm engine that powers the
// Playbeat-style beat panel inside becky-canvas. It is the BRAIN only: it has no
// GUI, no audio, no cgo, and no network. Every operation is a pure function that
// takes a Pattern (plus params and, where randomness is involved, an explicit
// seed) and returns a NEW Pattern — the input is never mutated. Same inputs +
// same seed => byte-identical output, so the canvas (and any future audio player)
// stays reproducible.
//
// # Model
//
// A Pattern is a set of Lanes over a global step length, plus swing and a seed.
// Each Lane is one percussion voice (kick/snare/hat/...) carrying its own Steps,
// an independent Length (for polymeter — a lane can loop at a different length
// than the global pattern), a playback Direction, and per-lane flags (Mute, Solo,
// Locked) and a generative Density.
//
// Each Step is rich, Playbeat-class data:
//
//	On          whether the step fires
//	Velocity    0..127 loudness
//	Pitch       semitone offset applied at play time
//	Pan         -100..100 stereo position
//	Probability 0..100 chance % the step actually fires on a given cycle
//	Ratchet     1..8 sub-hits (repeats) within the step
//	Flam        0..N a single delayed grace hit (distinct from Ratchet)
//	Locked      protected from Generate/Mutate/Density edits
//
// Conditional probability and ratcheting are DATA on the Step. The engine sets
// them; ExpandStep turns one step into the concrete []Hit a player would sound,
// deterministically per (seed, globalStep), so the audio side can be reproducible
// too.
//
// # Conventions
//
// All operations respect Locked on both lanes and steps: a locked element is
// never changed by Generate, Mutate, SetDensity, Busier/Sparser, Rotate, or
// ApplyEuclidean. Invalid inputs degrade — they return a clamped sane value or a
// typed error and never panic.
package beatgen

import "fmt"

// Direction is a lane's playback traversal order over one cycle of its Length.
type Direction int

const (
	// Forward plays step indices 0..n-1.
	Forward Direction = iota
	// Reverse plays step indices n-1..0.
	Reverse
	// PingPong plays forward then backward without repeating the endpoints.
	PingPong
	// Random plays a seeded permutation of the indices (deterministic per cycle).
	Random
)

// String renders a Direction for debugging/JSON-adjacent display.
func (d Direction) String() string {
	switch d {
	case Forward:
		return "forward"
	case Reverse:
		return "reverse"
	case PingPong:
		return "pingpong"
	case Random:
		return "random"
	default:
		return fmt.Sprintf("direction(%d)", int(d))
	}
}

// Velocity bounds and defaults used across the engine.
const (
	// MinVelocity is the lowest stored velocity for an ON step (0 means silent).
	MinVelocity = 0
	// MaxVelocity is the MIDI ceiling.
	MaxVelocity = 127
	// DefaultVelocity is the velocity a freshly turned-on step gets when none
	// is supplied.
	DefaultVelocity = 100

	// MinRatchet / MaxRatchet bound sub-hits per step.
	MinRatchet = 1
	MaxRatchet = 8

	// MinFlam / MaxFlam bound a step's flam strength. 0 = no flam (no grace
	// hit). 1..MaxFlam selects how far the single grace hit sits ahead of the
	// main hit; larger = closer to the main hit (a tighter flam). See flam.go
	// for the offset mapping used by ExpandStep.
	MinFlam = 0
	MaxFlam = 8

	// MinProbability / MaxProbability bound the per-step fire chance.
	MinProbability = 0
	MaxProbability = 100

	// MinPan / MaxPan bound the stereo position.
	MinPan = -100
	MaxPan = 100

	// MaxSteps caps a pattern/lane length to keep operations bounded.
	MaxSteps = 1024
)

// Step is one cell of a lane. The zero value is a valid OFF step (Probability 0
// here means "use default" only at construction time via NewStep; a stored ON
// step should carry an explicit Probability — Generate/SetStep set 100).
type Step struct {
	On          bool `json:"on"`
	Velocity    int  `json:"velocity"`    // 0..127
	Pitch       int  `json:"pitch"`       // semitone offset
	Pan         int  `json:"pan"`         // -100..100
	Probability int  `json:"probability"` // 0..100 chance %
	Ratchet     int  `json:"ratchet"`     // 1..8 sub-hits
	Flam        int  `json:"flam"`        // 0 = none; 1..8 = single grace hit ahead of the main hit
	Locked      bool `json:"locked"`
}

// NewStep returns an ON step with sane defaults (full probability, single hit,
// centered, default velocity).
func NewStep() Step {
	return Step{
		On:          true,
		Velocity:    DefaultVelocity,
		Pitch:       0,
		Pan:         0,
		Probability: MaxProbability,
		Ratchet:     MinRatchet,
		Locked:      false,
	}
}

// clamp returns v constrained to [lo, hi].
func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// clampVelocity constrains a velocity to [0,127].
func clampVelocity(v int) int { return clamp(v, MinVelocity, MaxVelocity) }

// normalize returns a copy of the step with every field forced into range. It is
// the degrade-never-crash guard applied whenever a step enters the engine.
func (s Step) normalize() Step {
	s.Velocity = clampVelocity(s.Velocity)
	s.Pan = clamp(s.Pan, MinPan, MaxPan)
	s.Probability = clamp(s.Probability, MinProbability, MaxProbability)
	if s.Ratchet < MinRatchet {
		s.Ratchet = MinRatchet
	}
	if s.Ratchet > MaxRatchet {
		s.Ratchet = MaxRatchet
	}
	if s.Flam < MinFlam {
		s.Flam = MinFlam
	}
	if s.Flam > MaxFlam {
		s.Flam = MaxFlam
	}
	return s
}

// Lane is one percussion voice: a row of Steps with its own loop Length and
// traversal Direction (for polymeter and playback variety).
type Lane struct {
	Name      string    `json:"name"`
	Role      string    `json:"role"` // kick/snare/hat/... (drives generative weighting)
	Steps     []Step    `json:"steps"`
	Length    int       `json:"length"` // cycle length; <=0 or >len(Steps) means len(Steps)
	Direction Direction `json:"direction"`
	Mute      bool      `json:"mute"`
	Solo      bool      `json:"solo"`
	Locked    bool      `json:"locked"`
	Density   float64   `json:"density"` // 0..1 generative fill probability

	// Rate is a per-lane subdivision multiplier relative to the global step
	// rate: 1.0 (or 0, meaning "default") = the lane runs at the global rate,
	// 2.0 = double-time, 0.5 = half-time. It is a timing hint consumed by the
	// playback helpers; it does not change how many Steps the lane stores.
	Rate float64 `json:"rate"`
	// Swing is a per-lane swing override in [0,1]. When 0 the pattern's global
	// Swing applies to this lane; when >0 it overrides the global value for this
	// lane only. Same convention as SwingOffset (odd steps delayed up to 0.5).
	Swing float64 `json:"swing"`
	// TrackDelay is a constant per-lane micro-timing offset in fractions of a
	// step (e.g. 0.02 nudges the whole lane slightly late; -0.02 slightly early).
	// Applied on top of swing by the playback helpers. Clamped to [-1,1].
	TrackDelay float64 `json:"track_delay"`
}

// effRate returns the lane's effective subdivision multiplier: Rate when it is
// positive, otherwise 1.0 (the global step rate). A zero or negative Rate is the
// degrade default so existing zero-valued lanes behave exactly as before.
func (l Lane) effRate() float64 {
	if l.Rate > 0 {
		return l.Rate
	}
	return 1.0
}

// effSwing returns the swing this lane should use given the pattern's global
// swing: the lane's own Swing when it is set (>0), otherwise the global value.
// A zero lane Swing means "inherit", so a freshly constructed lane is unchanged.
func (l Lane) effSwing(global float64) float64 {
	if l.Swing > 0 {
		return l.Swing
	}
	return global
}

// effTrackDelay returns the lane's TrackDelay clamped to [-1,1] (a step's worth
// either side). Zero stays zero, preserving the prior no-offset behavior.
func (l Lane) effTrackDelay() float64 {
	d := l.TrackDelay
	if d < -1 {
		return -1
	}
	if d > 1 {
		return 1
	}
	return d
}

// effLength returns the effective loop length of the lane: its Length clamped to
// the number of steps it has, defaulting to the full row when Length is unset or
// out of range.
func (l Lane) effLength() int {
	n := len(l.Steps)
	if n == 0 {
		return 0
	}
	if l.Length <= 0 || l.Length > n {
		return n
	}
	return l.Length
}

// clone returns a deep copy of the lane (independent Steps slice).
func (l Lane) clone() Lane {
	out := l
	out.Steps = append([]Step(nil), l.Steps...)
	return out
}

// Pattern is the full beat: a set of lanes over a global step length, plus swing
// and the seed used for any subsequent generative call that does not override it.
type Pattern struct {
	Lanes []Lane  `json:"lanes"`
	Steps int     `json:"steps"` // global pattern length
	Swing float64 `json:"swing"` // 0 = straight; (0,1] = increasing offbeat delay
	Seed  int64   `json:"seed"`
}

// NewPattern builds an empty pattern with the given global step count (clamped to
// [0, MaxSteps]) and the named/roled lanes. Each lane is sized to steps with all
// cells OFF.
func NewPattern(steps int, lanes ...Lane) *Pattern {
	steps = clamp(steps, 0, MaxSteps)
	p := &Pattern{Steps: steps}
	for _, ln := range lanes {
		ln.Steps = resizeSteps(ln.Steps, laneSize(ln, steps))
		p.Lanes = append(p.Lanes, ln)
	}
	return p
}

// laneSize picks how many steps a lane gets at construction: its own Length if a
// positive polymeter length was requested, otherwise the global step count.
func laneSize(ln Lane, global int) int {
	if ln.Length > 0 {
		return clamp(ln.Length, 0, MaxSteps)
	}
	if len(ln.Steps) > 0 {
		return len(ln.Steps)
	}
	return global
}

// resizeSteps grows/shrinks a step slice to n, preserving existing cells and
// normalizing every retained step.
func resizeSteps(in []Step, n int) []Step {
	if n < 0 {
		n = 0
	}
	out := make([]Step, n)
	for i := 0; i < n && i < len(in); i++ {
		out[i] = in[i].normalize()
	}
	return out
}

// Clone returns a deep copy of the pattern; the result shares no slices with the
// receiver, so callers can mutate it freely.
func (p *Pattern) Clone() *Pattern {
	if p == nil {
		return nil
	}
	out := *p
	out.Lanes = make([]Lane, len(p.Lanes))
	for i, ln := range p.Lanes {
		out.Lanes[i] = ln.clone()
	}
	return &out
}

// laneIndex returns the index of the first lane with the given name, or -1.
func (p *Pattern) laneIndex(name string) int {
	for i := range p.Lanes {
		if p.Lanes[i].Name == name {
			return i
		}
	}
	return -1
}

// Lane returns a pointer-free copy of the named lane and whether it was found.
func (p *Pattern) Lane(name string) (Lane, bool) {
	if i := p.laneIndex(name); i >= 0 {
		return p.Lanes[i].clone(), true
	}
	return Lane{}, false
}
