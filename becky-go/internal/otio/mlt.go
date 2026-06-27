// mlt.go — the Reel -> MLT/.kdenlive output (SPEC-BECKY-OTIO §8 "mlt"). It reuses
// the already-proven internal/kdenlive emitter (validated against melt 7.37 on
// real footage) rather than re-deriving the MLT shape: a becky Reel becomes a
// kdenlive.Project (one validating-avformat producer per source + a playlist of
// the ordered cuts) which marshals to a .kdenlive file that opens in the kdenlive
// GUI and renders headless via melt. Pure Go: no exec, no probe, no models.
package otio

import (
	"io"
	"strings"

	"becky-go/internal/edl"
	"becky-go/internal/kdenlive"
	"becky-go/internal/pathx"
)

// WriteMLT writes the Reel as a kdenlive/MLT XML project. Degenerate clips
// (out <= in) are skipped, matching the OTIO/vegas-list writers. It returns the
// number of clips placed on the timeline. A Reel with no playable clips is a
// returned error (kdenlive.Marshal validates a non-empty timeline), surfaced by
// the CLI as a per-format warning rather than a crash.
func WriteMLT(w io.Writer, r edl.Reel, opts Options) (int, error) {
	title := strings.TrimSpace(r.Name)
	if title == "" {
		title = "becky-review"
	}
	p := kdenlive.NewProject(title)

	n := 0
	for _, c := range r.Clips {
		if c.Dur() <= 0 {
			continue // skip out<=in, same rule as the other writers
		}
		name := strings.TrimSpace(c.Label)
		if name == "" {
			name = pathx.Base(c.Source)
		}
		p.AddClip(kdenlive.Clip{Source: c.Source, In: c.In, Out: c.Out, Name: name})
		// Record the clip's true fps so the producer's frame math (length/out) is
		// correct for mixed-rate sources. LengthSec stays 0 (unknown, no probe):
		// kdenlive falls back to the farthest cut + a 1s pad, which always covers
		// the placed cuts.
		p.SetSource(kdenlive.Source{Path: c.Source, FPS: clipFPS(c, opts)})
		n++
	}

	data, err := p.Marshal()
	if err != nil {
		return 0, err
	}
	if _, err := w.Write(data); err != nil {
		return n, err
	}
	return n, nil
}

// clipFPS resolves a clip's frame rate using the same fallback chain as the OTIO
// writer (clip Meta -> Options.FallbackFPS -> edl.DefaultFPS).
func clipFPS(c edl.Clip, opts Options) float64 {
	return fps(c, opts)
}
