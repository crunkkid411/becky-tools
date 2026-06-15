package dawmodel

import "strconv"

// corrections.go is the preference-learning substrate, the CLAUDE.md HARD
// REQUIREMENT for becky-canvas: "becky LEARNS Jordan's preferences from his manual
// corrections." Every time an edit overrides an AUTO-generated value with Jordan's
// CORRECTED value, the op appends a Correction capturing {kind, where, auto value,
// fixed value, context}. The log is append-only and deterministic; a later model
// (the "becky preference" model, SPEC-BECKY-VISION-MODELS) trains on it. This file
// only records — it never guesses.

// Correction is one {auto -> fixed} override with enough context to learn from.
//   - Kind  : which knob was overridden (velocity, quantize, gain, ...).
//   - Clip  : the track/clip the edit happened on (the "where").
//   - At    : a tick position so corrections can be grouped by song region.
//   - Auto  : becky's generated value (as text, so any param type fits).
//   - Fixed : Jordan's corrected value.
//   - Genre/BPM/Scale: the musical context, so a preference is conditioned on the
//     song it was made in (a louder snare in crunkcore, not everywhere).
type Correction struct {
	Kind  string `json:"kind"`
	Clip  string `json:"clip"`
	At    int    `json:"at"`
	Auto  string `json:"auto"`
	Fixed string `json:"fixed"`
	Genre string `json:"genre,omitempty"`
	BPM   int    `json:"bpm,omitempty"`
	Scale string `json:"scale,omitempty"`
}

// logCorrection appends one override to the arrangement's corrections log, stamping
// the current musical context. Called by edit ops only when auto != fixed, so the
// log is a clean signal of Jordan's taste (no no-op churn).
func (a *Arrangement) logCorrection(kind, clip string, at int, auto, fixed string) {
	a.Corrections = append(a.Corrections, Correction{
		Kind: kind, Clip: clip, At: at, Auto: auto, Fixed: fixed,
		Genre: a.Genre, BPM: a.BPM, Scale: a.Scale,
	})
}

// CorrectionCount returns how many overrides have been recorded (a quick probe for
// "has Jordan taught becky anything in this session?").
func (a *Arrangement) CorrectionCount() int { return len(a.Corrections) }

// CorrectionsByKind returns the recorded corrections of one kind (deterministic
// order — the order they were made). Used by the preference summarizer.
func (a *Arrangement) CorrectionsByKind(kind string) []Correction {
	var out []Correction
	for _, c := range a.Corrections {
		if c.Kind == kind {
			out = append(out, c)
		}
	}
	return out
}

// ftoa formats a float param compactly for the corrections log (no trailing zeros).
func ftoa(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}
