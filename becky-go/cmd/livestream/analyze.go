package main

import (
	"math"
	"os"
)

// analyzeWindow is how much of the stream the picture pass measures between saves (and progress lines).
const analyzeWindow = 180.0

// analyze measures the WHOLE stream once and keeps it in becky-edit: the sound labels (breaths,
// laughs, coughs, every 40 ms) and the picture signals (face, pose, gestures, objects, every
// 0.1 s). Later runs - another model, other guidance - read both back and measure nothing again.
// Before 2026-10-10 the picture was measured only where a model's edit needed it and the sound
// labels were never kept, so swapping one model re-ran the analysis (Jordan stopped tuning over it).
// A model that cannot run leaves a note; the checks that need it then measure their own stretches.
func (r *run) analyze(wav string) {
	if _, err := os.Stat(soundCache(r.work, r.stem)); err != nil {
		r.logf("analysis: labelling every sound in the stream once (breaths, laughs, coughs)...")
	}
	if _, err := soundFrames(r.cfg, wav, soundCache(r.work, r.stem)); err != nil {
		r.logf("  the sound labels were not made: %v", err)
	}
	// ponytail: stops half a second short of the end - a last grid frame past the final video frame
	// can never be measured, and would send every later run back to the helper for it.
	end := r.duration - 0.5
	for a := 0.0; a < end; a += analyzeWindow {
		b := math.Min(end, a+analyzeWindow)
		if _, err := loadPictures(r.cfg, r.media, picCache(r.work, r.stem), []span{{a, b}}); err != nil {
			r.logf("  the picture was not measured: %v", err)
			return
		}
		r.logf("analysis: picture measured and kept up to %s of %s", clock(b), clock(r.duration))
	}
}
