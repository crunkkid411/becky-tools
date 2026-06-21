package ctledit

import (
	"fmt"

	"becky-go/internal/beatgen"
	"becky-go/internal/dawmodel"
	"becky-go/internal/music"
)

// generative.go wires the canvas AI edit box to internal/beatgen — the
// Playbeat-class generative rhythm engine — so a plain-English request like
// "randomize the beat" or "make the kick four on the floor" becomes a concrete,
// previewable arrangement edit (the "show me, don't do it" loop). Both ops keep
// ctledit's contract: immutable, deterministic, degrade-with-a-reason (never a
// panic). They operate on a drum clip's existing lanes — regenerating the
// pattern while keeping the kit the user already loaded.

// applyGenerateBeat regenerates a drum clip's pattern via beatgen, biased by an
// optional genre or a flat density override, seeded for determinism.
func applyGenerateBeat(a *dawmodel.Arrangement, ed BeckyEdit) (*dawmodel.Arrangement, string) {
	trackID, reason := resolveTrack(a, drumRef(ed))
	if reason != "" {
		return a, reason
	}
	clipName, reason := resolveClip(a, trackID, ed.Clip)
	if reason != "" {
		return a, reason
	}
	grid, err := a.DrumGridOf(trackID, clipName, 0)
	if err != nil {
		return a, fmt.Sprintf("generate_beat: derive grid: %v", err)
	}
	if len(grid.Lanes) == 0 {
		return a, "generate_beat: drum clip has no lanes to regenerate"
	}

	pat := beatgen.FromDrumGrid(grid)
	switch {
	case ed.Density > 0:
		d := clamp01f(ed.Density)
		for _, ln := range pat.Lanes {
			pat = pat.SetDensity(ln.Name, d)
		}
		pat = pat.Generate(beatgen.DefaultGenerateOptions(), ed.Seed)
	default:
		// Genre (empty/unknown degrades to the neutral profile inside beatgen).
		pat = pat.GenerateGenre(ed.Genre, ed.Seed)
	}

	next, reason := applyPatternToClip(a, trackID, clipName, pat)
	if reason != "" {
		return a, reason
	}
	return next, ""
}

// applyEuclidLane sets one named drum lane to a euclidean rhythm (pulses spread
// evenly across the lane, optionally rotated).
func applyEuclidLane(a *dawmodel.Arrangement, ed BeckyEdit) (*dawmodel.Arrangement, string) {
	if ed.Lane == "" {
		return a, "euclid_lane: lane name is required (e.g. \"kick\")"
	}
	if ed.Pulses <= 0 {
		return a, fmt.Sprintf("euclid_lane: pulses %d must be > 0", ed.Pulses)
	}
	trackID, reason := resolveTrack(a, drumRef(ed))
	if reason != "" {
		return a, reason
	}
	clipName, reason := resolveClip(a, trackID, ed.Clip)
	if reason != "" {
		return a, reason
	}
	grid, err := a.DrumGridOf(trackID, clipName, 0)
	if err != nil {
		return a, fmt.Sprintf("euclid_lane: derive grid: %v", err)
	}

	pat := beatgen.FromDrumGrid(grid)
	if _, ok := pat.Lane(ed.Lane); !ok {
		return a, fmt.Sprintf("euclid_lane: lane %q not found in the drum clip", ed.Lane)
	}
	pat = pat.ApplyEuclidean(ed.Lane, ed.Pulses, ed.Rotation)

	next, reason := applyPatternToClip(a, trackID, clipName, pat)
	if reason != "" {
		return a, reason
	}
	return next, ""
}

// applyPatternToClip compiles a beatgen pattern back into the clip's notes. It
// pins the grid's step resolution (beatgen leaves it rate-agnostic) so notes do
// not collapse onto tick 0, then applies it through the immutable verb.
func applyPatternToClip(a *dawmodel.Arrangement, trackID, clipName string, pat *beatgen.Pattern) (*dawmodel.Arrangement, string) {
	g := beatgen.ToDrumGrid(pat)
	if g.StepTicks <= 0 {
		g.StepTicks = music.StepTicks
	}
	next, err := a.ApplyDrumGrid(trackID, clipName, g)
	if err != nil {
		return a, fmt.Sprintf("apply pattern: %v", err)
	}
	return next, ""
}

// drumRef returns the track reference for a generative op, accepting either the
// note-style Track field or the mixer-style Target field (whichever the model
// populated) so the op is forgiving about which name the proposal used.
func drumRef(ed BeckyEdit) string {
	if ed.Track != "" {
		return ed.Track
	}
	return ed.Target
}

// clamp01f bounds a float to [0,1].
func clamp01f(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}
