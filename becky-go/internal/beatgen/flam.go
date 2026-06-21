package beatgen

// flam.go implements the per-step flam: a single quiet grace hit placed just
// AHEAD of a main hit. A flam is a drummer's ornament — a soft note immediately
// before the accented note — and is deliberately DISTINCT from a ratchet (which
// is N evenly-spaced full-velocity repeats). Both can coexist on one Step:
// ratchet decides how many full sub-hits there are; flam adds one grace note in
// front of each.
//
// Everything here is pure and deterministic: the grace offset and velocity are a
// fixed function of the Step's Flam value (and the main hit's position), so the
// same Step always expands to the same Hits.

const (
	// flamGraceVelRatio is the grace hit's velocity as a fraction of the main
	// hit's velocity (a flam grace note is softer than the accent).
	flamGraceVelRatio = 0.6
	// flamMinGraceVel keeps an audible grace note even for very quiet main hits.
	flamMinGraceVel = 1
	// flamMaxLead is the largest gap (as a fraction of a step) between the grace
	// hit and its main hit, used at Flam == 1 (a loose, wide flam). Higher Flam
	// values tighten the grace toward the main hit.
	flamMaxLead = 0.12
)

// flamLead maps a Flam strength (1..MaxFlam) to the gap, as a fraction of a
// step, that the grace hit sits AHEAD of the main hit. Flam == 1 gives the widest
// lead (flamMaxLead); larger Flam values place the grace progressively closer to
// the main hit (a tighter flam). Flam <= 0 returns 0 (no grace hit).
//
// The mapping is linear over the [1, MaxFlam] range and fully deterministic:
//
//	lead(f) = flamMaxLead * (MaxFlam - f + 1) / MaxFlam
func flamLead(flam int) float64 {
	if flam < 1 {
		return 0
	}
	if flam > MaxFlam {
		flam = MaxFlam
	}
	return flamMaxLead * float64(MaxFlam-flam+1) / float64(MaxFlam)
}

// flamGraceVelocity returns the (clamped, audible) velocity for the grace hit of
// a step whose main velocity is mainVel.
func flamGraceVelocity(mainVel int) int {
	v := int(float64(mainVel) * flamGraceVelRatio)
	if v < flamMinGraceVel {
		v = flamMinGraceVel
	}
	return clampVelocity(v)
}

// flamHits returns the grace hit(s) for one sub-hit at offset `base` whose time
// slot spans `slot` fractions of a step (slot = 1/ratchet). When the step has no
// flam (Flam <= 0) it returns nil, so a zero-Flam step expands exactly as before.
//
// The grace hit is placed `lead` (bounded by the sub-hit's own slot so it never
// crosses into the previous sub-hit) ahead of `base`, clamped to >= 0 so it never
// lands before the step's own start. It carries the reduced grace velocity.
func flamHits(s Step, base, slot float64) []Hit {
	if s.Flam <= 0 {
		return nil
	}
	lead := flamLead(s.Flam)
	// Never let the grace cross into the previous sub-hit's slot.
	if slot > 0 && lead > slot*0.9 {
		lead = slot * 0.9
	}
	off := base - lead
	if off < 0 {
		off = 0
	}
	return []Hit{{
		Offset:   off,
		Velocity: flamGraceVelocity(s.Velocity),
	}}
}
