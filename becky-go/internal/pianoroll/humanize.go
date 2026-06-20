package pianoroll

import "math/rand"

// humanize.go adds subtle, musical variation to a clip so a quantized line breathes
// like a played one. It is the ONE place in this package where pseudo-random is
// allowed, and — like internal/drumcmd's humanize — it MUST draw from a seeded
// math/rand source so the same (seed, clip, amounts) reproduces byte-for-byte.
//
// Unlike a step-quantized drum grid (where dawmodel can only wiggle velocity), the
// piano roll stores real per-note tick positions, so humanize nudges BOTH timing
// and velocity:
//   - timing  : +/- up to timingTicks ticks on each note's Start (never below 0).
//   - velocity: +/- up to velSpread on each note's Velocity (clamped 1..127).
//
// Determinism: notes are iterated in their stable sorted order and rng.Intn is
// called in a fixed (timing-then-velocity) sequence per note, so a given seed
// always produces the same offsets regardless of how the selection was passed.

// DefaultSeed is the fixed seed used when a caller wants reproducible humanization
// without choosing one. It matches internal/drumcmd.DefaultSeed (42) so becky's
// "humanize" feels the same across the drum grid and the piano roll.
const DefaultSeed int64 = 42

// Default humanize amounts. timingTicks is intentionally small (a 1/16 at 480 PPQ
// is 120t, so 12t is a tenth of a 16th) and velSpread mirrors drumcmd.
const (
	defaultTimingTicks = 12
	defaultVelSpread   = 12
)

// HumanizeOpts controls the amount of variation. A zero value means "use the
// defaults". A negative value disables variation on that axis.
type HumanizeOpts struct {
	TimingTicks int   // max +/- timing nudge in ticks (0 => defaultTimingTicks)
	VelSpread   int   // max +/- velocity nudge (0 => defaultVelSpread)
	Seed        int64 // RNG seed (0 => DefaultSeed)
}

// resolve fills zero fields with their defaults. A caller that genuinely wants an
// axis disabled passes a negative value, which resolve floors to 0.
func (o HumanizeOpts) resolve() HumanizeOpts {
	if o.TimingTicks == 0 {
		o.TimingTicks = defaultTimingTicks
	} else if o.TimingTicks < 0 {
		o.TimingTicks = 0
	}
	if o.VelSpread == 0 {
		o.VelSpread = defaultVelSpread
	} else if o.VelSpread < 0 {
		o.VelSpread = 0
	}
	if o.Seed == 0 {
		o.Seed = DefaultSeed
	}
	return o
}

// Humanize applies seeded timing+velocity variation to the selected notes (every
// note when indices is empty) and returns the new clip. Same seed + same clip =>
// identical result (becky's determinism rule). opts zero value uses the defaults.
//
// The RNG is advanced for every note in sorted order but the deltas are applied
// only to selected notes, so the random STREAM is anchored to the clip (not to the
// selection): humanizing note 3 alone draws the same offset it would in a full-clip
// humanize. This keeps results stable and predictable across selections.
func (c *Clip) Humanize(indices []int, opts HumanizeOpts) *Clip {
	o := opts.resolve()
	all := len(indices) == 0
	sel := c.selected(indices)
	r := rand.New(rand.NewSource(o.Seed))
	notes := append([]Note(nil), c.Notes...)
	for i := range notes {
		dt, dv := drawHumanize(r, o)
		if !all && !sel[i] {
			continue // stream advanced above; this note is untouched
		}
		notes[i].Start = maxInt(notes[i].Start+dt, 0)
		notes[i].Velocity = clampInt(notes[i].Velocity+dv, minVel, maxVel)
	}
	return c.withNotes(notes)
}

// drawHumanize pulls the timing and velocity deltas for one note from the RNG in a
// fixed order (timing first, velocity second). When an axis amount is 0 it returns
// 0 without consuming the stream for that axis.
func drawHumanize(r *rand.Rand, o HumanizeOpts) (dt, dv int) {
	if o.TimingTicks > 0 {
		dt = r.Intn(2*o.TimingTicks+1) - o.TimingTicks
	}
	if o.VelSpread > 0 {
		dv = r.Intn(2*o.VelSpread+1) - o.VelSpread
	}
	return dt, dv
}
