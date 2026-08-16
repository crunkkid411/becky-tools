package main

// cues.go writes the finished captions as JSON for a caller that is going to
// PLACE them itself rather than burn them — today that is the VEGAS Pro script
// (vegas/BeckyCaptions.cs), which turns each cue into a Titles & Text event.
//
// The .srt stays the primary artifact; this is an additional, machine-placed
// view of the same cues. When the run came from a live Vegas timeline
// (--timeline) the times here are VEGAS RULER SECONDS, already mapped back
// through the gaps in the edit, so the script can drop each event at
// cue.start with no arithmetic of its own. Otherwise they are the rendered
// programme's times, identical to the .srt.
//
// The shape is deliberately flat and regular: a Vegas script runs on .NET
// Framework with no JSON parser it can rely on, so it reads this with one
// regular expression. Keep "start", "end", "text" in that order, one object per
// cue, and keep the numbers plain decimals.

import (
	"encoding/json"
	"fmt"
	"os"

	"becky-go/internal/edl"
	"becky-go/internal/subs"
)

// cueOut is one caption as the placing caller sees it. Field order is part of
// the contract — see the package comment.
type cueOut struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// cuesFile is the whole document.
type cuesFile struct {
	Version string   `json:"version"`
	Base    string   `json:"base"` // "vegas-timeline" or "programme" — which clock Start/End are on
	FPS     float64  `json:"fps"`
	SRT     string   `json:"srt"`
	Count   int      `json:"count"`
	Cues    []cueOut `json:"cues"`
}

// buildCues converts finished cues to the output form. When tl is non-nil the
// times are mapped onto the Vegas ruler; a cue that maps nowhere (past the end
// of the edit) is dropped rather than placed at a wrong position, and reported.
func buildCues(cues []subs.Cue, tl *edl.VegasTimeline, fps float64, srtPath string) (cuesFile, []string) {
	var warnings []string
	out := cuesFile{Version: "1", Base: "programme", FPS: fps, SRT: srtPath}
	out.Cues = make([]cueOut, 0, len(cues))

	dropped := 0
	for _, c := range cues {
		start, end := c.Start, c.End
		if tl != nil {
			s, e, ok := tl.MapSpan(c.Start, c.End)
			if !ok {
				dropped++
				continue
			}
			start, end = s, e
		}
		out.Cues = append(out.Cues, cueOut{Start: round3(start), End: round3(end), Text: c.Text})
	}
	if tl != nil {
		out.Base = "vegas-timeline"
	}
	if dropped > 0 {
		warnings = append(warnings, fmt.Sprintf("%d caption(s) fell outside the timeline and were not placed", dropped))
	}
	out.Count = len(out.Cues)
	return out, warnings
}

// writeCues serialises the cues document to path, indented so a human can read
// it when a placement looks wrong.
func writeCues(path string, doc cuesFile) error {
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}
