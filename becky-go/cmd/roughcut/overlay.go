package main

// overlay.go — burns becky-review-3's forensic lower-third overlay into each
// quote clip before it's spliced into the timeline. Jordan, 2026-08-25: the
// quote clips need "the overlay feature burned in from becky-review-3 (no
// captions, no 'name', just the overlay feature)". Reuses internal/reel's
// existing, already-tested Render (edl.Overlay toggles + lowerThirdFilter) -
// the same mechanism becky-reel already burns lower-thirds with - rather than
// hand-rolling a new ffmpeg filter. ShowFilename is deliberately off (that IS
// becky-review-3's "Name" toggle, per its own main.cpp comment); everything
// else in the overlay stays on and simply omits any line it has no real data
// for (lowerThirdFilter never fabricates a value - an empty Meta field
// produces no line at all, never placeholder text on a real evidence video).
//
// Degrades, never crashes: a render failure (no ffmpeg, a bad source) leaves
// that ONE quote pointed at its original, un-overlaid file and logs why -
// losing the overlay on one quote is not worth losing the quote itself.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"becky-go/internal/beckyio"
	"becky-go/internal/edl"
	"becky-go/internal/reel"
)

// quoteOverlay is the fixed toggle set for a quote clip's forensic lower
// third: everything on except the filename line (Jordan's "no ... name").
var quoteOverlay = edl.Overlay{
	Enabled:      true,
	ShowFilename: false,
	ShowTimecode: true,
	ShowDate:     true,
	ShowLink:     true,
	ShowPerson:   true,
	ShowLocation: true,
	Position:     "bottom",
}

// burnQuoteOverlays renders one overlay-burned copy of each quote clip
// (trimmed to its own [In,Out), same span it would have been spliced in
// unmodified) and returns an updated quote list pointing at the burned
// files. A quote whose render fails keeps its ORIGINAL source untouched.
func burnQuoteOverlays(quotes []quoteIn, outDir string, verbose bool) []quoteIn {
	burnedDir := filepath.Join(outDir, "quotes_overlay")
	out := make([]quoteIn, len(quotes))
	for i, q := range quotes {
		out[i] = q
		dest := filepath.Join(burnedDir, fmt.Sprintf("q%02d_overlay.mp4", i))
		r := edl.Reel{
			Name: fmt.Sprintf("q%02d_overlay", i),
			Clips: []edl.Clip{
				{ID: fmt.Sprintf("q%02d", i), Source: q.Source, In: q.In, Out: q.Out},
			},
			Overlay: quoteOverlay,
		}
		res, err := reel.Render(r, reel.Options{Output: dest, Verbose: verbose})
		if err != nil {
			beckyio.Logf(true, "quote overlay burn-in failed for %s (keeping the plain quote): %v", filepath.Base(q.Source), err)
			continue
		}
		out[i].Source = res.Output
		out[i].In = 0
		out[i].Out = res.DurationSec
		beckyio.Logf(verbose, "quote %d overlay burned: %s (%.2fs)", i, res.Output, res.DurationSec)
	}
	return out
}

// runBurnOverlaysPass is becky-roughcut's --burn-quote-overlays entry point:
// STANDALONE, same shape as --triage-markers/--vegas-only - reads an EXISTING
// vegas_cut.json, burns each already-placed quote's overlay, and rewrites
// just the quotes array in place. Never touches events/markers/regions and
// never re-runs detection, so it is safe to run after a triage pass without
// undoing it. TL positions are left untouched: the render trims to exactly
// [In,Out), so each burned quote's duration matches the original and nothing
// downstream needs to shift.
func runBurnOverlaysPass(out string, verbose bool) error {
	vb, err := os.ReadFile(filepath.Join(out, "vegas_cut.json"))
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(vb, &doc); err != nil {
		return err
	}
	var typed struct {
		Quotes []quoteOut `json:"quotes"`
	}
	if err := json.Unmarshal(vb, &typed); err != nil {
		return err
	}

	asIn := make([]quoteIn, len(typed.Quotes))
	for i, q := range typed.Quotes {
		asIn[i] = quoteIn{Source: q.Source, In: q.In, Out: q.Out}
	}
	burned := burnQuoteOverlays(asIn, out, verbose)

	newQuotes := make([]quoteOut, len(typed.Quotes))
	for i, q := range typed.Quotes {
		newQuotes[i] = quoteOut{Source: burned[i].Source, In: burned[i].In, Out: burned[i].Out, TL: q.TL}
	}
	doc["quotes"] = newQuotes

	nb, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "vegas_cut.json"), nb, 0o644); err != nil {
		return err
	}
	beckyio.Logf(true, "quote overlays: %d burned", len(newQuotes))
	return nil
}
