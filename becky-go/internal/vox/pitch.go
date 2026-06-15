package vox

import (
	"math"

	"becky-go/internal/music"
)

// pitch.go computes the per-note pitch DECISION (SPEC §3): for each alt note, the
// target pitch (the guide's note in stack mode, or the nearest scale tone in tune
// mode) and the proposed cents move, clamped to MaxShiftSemi. The actual
// formant-preserving pitch shift is the renderer STUB (WORLD/TD-PSOLA/Rubber Band on
// the local box); here we only DECIDE and label which engine WOULD run, and flag
// notes where the F0 is uncertain (cross-check disagreement) instead of moving them
// silently — corroborate-then-conclude applied to vocals (SPEC §3.1.3).

// engineForShift picks the formant-preserving engine label per the SPEC §3.3
// decision: TD-PSOLA for small shifts (the common double case), WORLD as the
// permissive default for larger ones. Rubber Band is the GPL opt-in (not auto-picked
// here). The chosen engine is recorded so the result is auditable.
func engineForShift(absCents float64) string {
	if absCents <= 100 { // <= 1 semitone: TD-PSOLA preserves formants by construction
		return "psola"
	}
	return "world"
}

// MatchPitch builds the per-note pitch decisions. In stack mode each alt note is
// matched to the time-overlapping guide note (glue a double to the lead); in tune
// mode it snaps to the nearest tone of the AlignOptions.Key scale. crossCheck (a
// second F0 estimate per alt note, e.g. Praat) flags notes where the two estimates
// disagree by more than a semitone — those are NOT moved silently.
func MatchPitch(guideNotes, altNotes []DetectedNote, crossCheck []float64, opt AlignOptions) []VoxNote {
	out := make([]VoxNote, 0, len(altNotes))
	scalePCs := tuneScalePCs(opt)
	for i, an := range altNotes {
		target := pitchTarget(an, guideNotes, scalePCs, opt.Mode)
		move := clampCents(centsBetweenHz(an.Hz, target), opt.MaxShiftSemi)
		cross := crossCheckAt(crossCheck, i)
		flagged := !confidentF0(an, cross) || math.Abs(move) > opt.FlagCents
		out = append(out, VoxNote{
			StartMs: round2(an.StartMs), EndMs: round2(an.EndMs),
			DetectedHz: round2(an.Hz), TargetHz: round2(target),
			MoveCents: round2(move), EngineUsed: engineForShift(math.Abs(move)),
			Confidence: round2(noteConfidence(an, cross)), Flagged: flagged,
		})
	}
	return out
}

// pitchTarget returns the target Hz for an alt note: the overlapping guide note's
// Hz (stack mode) or the nearest scale-tone Hz (tune mode). Falls back to the alt's
// own pitch (no move) when no target is available.
func pitchTarget(an DetectedNote, guideNotes []DetectedNote, scalePCs []int, mode string) float64 {
	if mode == "tune" && len(scalePCs) > 0 {
		return snapHzToScale(an.Hz, scalePCs)
	}
	if g := overlappingGuide(an, guideNotes); g != nil {
		return g.Hz
	}
	return an.Hz // no guide overlap: leave it (no silent move)
}

// overlappingGuide finds the guide note whose time span overlaps the alt note most
// (deterministic: first max-overlap wins). Returns nil when none overlap.
func overlappingGuide(an DetectedNote, guideNotes []DetectedNote) *DetectedNote {
	bestOverlap, bestIdx := 0.0, -1
	for i := range guideNotes {
		ov := overlapMs(an.StartMs, an.EndMs, guideNotes[i].StartMs, guideNotes[i].EndMs)
		if ov > bestOverlap {
			bestOverlap, bestIdx = ov, i
		}
	}
	if bestIdx < 0 {
		return nil
	}
	return &guideNotes[bestIdx]
}

// snapHzToScale returns the Hz of the nearest in-scale MIDI note to f.
func snapHzToScale(f float64, scalePCs []int) float64 {
	if f <= 0 {
		return f
	}
	midi := int(math.Round(hzToMidiF(f)))
	bestMidi, bestDist := midi, math.MaxFloat64
	for d := -2; d <= 2; d++ { // search a small neighborhood for the nearest scale tone
		cand := midi + d
		pc := ((cand % 12) + 12) % 12
		if !inSet(pc, scalePCs) {
			continue
		}
		if c := math.Abs(float64(cand) - hzToMidiF(f)); c < bestDist {
			bestDist, bestMidi = c, cand
		}
	}
	return midiToHz(float64(bestMidi))
}

// tuneScalePCs returns the scale pitch classes for the target key (tune mode only).
func tuneScalePCs(opt AlignOptions) []int {
	if opt.Mode != "tune" || opt.Key == "" {
		return nil
	}
	rootPC, scale := music.ParseKey(opt.Key)
	iv := music.ScaleIntervals(scale)
	out := make([]int, 0, len(iv))
	for _, semi := range iv {
		out = append(out, (rootPC+semi)%12)
	}
	return out
}

// confidentF0 is true when the tracker confidence is solid AND (if a cross-check
// estimate is present) the two estimates agree within ~a semitone. A divergence is
// flagged, not tuned (SPEC §3.1.3 — guards WORLD's V/UV weakness).
func confidentF0(an DetectedNote, crossHz float64) bool {
	if an.Confidence < 0.6 {
		return false
	}
	if crossHz > 0 && an.Hz > 0 {
		if math.Abs(centsBetweenHz(an.Hz, crossHz)) > 100 { // > 1 semitone apart
			return false
		}
	}
	return true
}

// noteConfidence fuses tracker confidence with cross-check agreement into 0..1.
func noteConfidence(an DetectedNote, crossHz float64) float64 {
	c := clamp01(an.Confidence)
	if crossHz > 0 && an.Hz > 0 {
		agree := 1 - clamp01(math.Abs(centsBetweenHz(an.Hz, crossHz))/100)
		c = 0.7*c + 0.3*agree
	}
	return clamp01(c)
}

func crossCheckAt(cc []float64, i int) float64 {
	if i < len(cc) {
		return cc[i]
	}
	return 0
}

// clampCents limits a proposed move to +/- maxSemi semitones (SPEC §3.2).
func clampCents(cents, maxSemi float64) float64 {
	lim := maxSemi * 100
	if cents > lim {
		return lim
	}
	if cents < -lim {
		return -lim
	}
	return cents
}
