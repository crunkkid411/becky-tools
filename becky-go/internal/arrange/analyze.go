package arrange

import (
	"fmt"

	"becky-go/internal/dawmodel"
)

// analyze.go gives becky an opinion about an in-progress arrangement — the
// deterministic core of the ACE-Step "arrange" + "jam" commands: what's missing,
// what's empty, and what to build next. No model.

// Finding is one observation about an arrangement.
type Finding struct {
	Kind  string `json:"kind"`            // "missing_layer" | "empty_track" | "suggestion"
	Track string `json:"track,omitempty"` // the track involved, if any
	Note  string `json:"note"`            // plain-English description
}

// Analyze reports the gaps in an arrangement (missing core layers, empty tracks) and
// the single best next step. Deterministic; safe on nil.
func Analyze(a *dawmodel.Arrangement) []Finding {
	var out []Finding
	if a == nil {
		return out
	}
	present := presentRoles(a)

	// Missing core layers, in build order.
	for _, layer := range []string{"bass", "chords", "melody"} {
		if !present[layer] {
			out = append(out, Finding{
				Kind: "missing_layer", Track: layer,
				Note: fmt.Sprintf("no %s yet — the arrangement is thin without it", layer),
			})
		}
	}

	// Tracks that exist but carry no notes (a placeholder that never got filled).
	for _, t := range a.Tracks {
		if trackNoteCount(t) == 0 {
			out = append(out, Finding{
				Kind: "empty_track", Track: t.ID,
				Note: fmt.Sprintf("track %q has no notes", t.ID),
			})
		}
	}

	// The one next step (the jam rule).
	if next := NextLayer(present); next != "" {
		out = append(out, Finding{
			Kind: "suggestion", Track: next,
			Note: fmt.Sprintf("build %s next (order: drums → bass → chords → melody → texture)", next),
		})
	} else {
		out = append(out, Finding{Kind: "suggestion", Note: "every core layer is present — refine, vary, or arrange sections"})
	}
	return out
}

// trackNoteCount counts notes across a track's clips.
func trackNoteCount(t dawmodel.Track) int {
	n := 0
	for _, c := range t.Clips {
		n += len(c.Notes)
	}
	return n
}

// Jam advances the arrangement by ONE layer — it builds the layer SuggestNext
// recommends, fitting the existing stems. Call it repeatedly to fill a loop out
// (drums → bass → chords → melody). Returns the layer it added ("" when nothing is
// left to add) and the new arrangement.
func Jam(a *dawmodel.Arrangement, opts Options) (*dawmodel.Arrangement, string, error) {
	next := SuggestNext(a)
	if !buildable(next) {
		return a, "", nil // nothing left that the deterministic engine builds (e.g. texture)
	}
	out, err := AddLayer(a, next, opts)
	if err != nil {
		return a, "", err
	}
	return out, next, nil
}

// buildable reports whether AddLayer can construct a layer today. "texture" is part
// of the order but not yet implemented, so jam stops cleanly there rather than error.
func buildable(layer string) bool {
	switch layer {
	case "bass", "chords", "melody":
		return true
	}
	return false
}
