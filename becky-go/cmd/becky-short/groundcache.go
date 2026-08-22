// groundcache.go — ground the WHOLE window once, then slice it per span.
//
// THE COST THIS FIXES. A short is cut into spans at the shot boundaries the
// footage already has, and Jordan's Mouse Trap prank has EIGHTEEN of them in
// forty seconds. Grounding each span separately meant eighteen ffmpeg decodes
// and up to eight model calls each — the render passed ten minutes and was still
// going. The spans are contiguous slices of ONE window, so the frames are the
// same frames; the only thing per-span about them is which ones you look at.
//
// So: one decode, one sweep at a steady rate across the whole window, and every
// span reads the samples that fall inside it. Eighteen spans cost what one does.
//
// This mirrors cutCache (becky-cut's whole-file decisions) and audioSigCache
// (audiosig's whole-file analysis) — the same shape for the same reason, which
// is that these passes are inherently whole-file and becky-short renders many
// windows out of one source.
package main

import (
	"errors"

	"becky-go/internal/ground"
)

// errNoWindow means resetShortFraming was never called for this short, which is
// a wiring mistake rather than a footage problem.
var errNoWindow = errors.New("the short's window was never recorded")

// groundSweepFPS is how often the whole-window sweep samples. One a second is
// enough to place a subject inside a shot and to notice it move across one;
// finer costs a model call per extra frame and buys a smoother pan than the
// smoothing pass already produces.
const groundSweepFPS = 1.0

// groundSweepMaxFrames caps the sweep so a long window cannot cost minutes. A
// 40-second window at 1fps is 40 calls; past this the rate drops instead.
const groundSweepMaxFrames = 60.0

// groundCache holds ONE whole-window grounding sweep, keyed by nothing: a
// becky-short process renders one window at a time (--reel loops over them), so
// the cache is reset per short alongside the framing memory.
type groundCache struct {
	done bool
	// target is what the CRITIC told us to look for after watching the last
	// render ("the man in the pink shirt"). Empty on the first pass, which is
	// ground.py's normal mode: find a person, else name whatever it sees. On a
	// re-frame it is passed as --target, so Reka stops volunteering a Pikachu
	// poster and goes looking for the thing the critic actually named.
	target string
	// winStart/winEnd are the SHORT's whole window, recorded when the short
	// begins so a span deep in the call stack does not have to carry it:
	// resolveCrop already takes ten parameters and knows only its own slice.
	winStart, winEnd float64
	res              ground.Result
	err              error
}

var shortGround groundCache

// resetShortGround clears the sweep between shorts. A different window is
// different frames.
func resetShortGround(winStart, winEnd float64) {
	shortGround = groundCache{winStart: winStart, winEnd: winEnd, target: shortTarget}
}

// shortTarget is the critic's named subject for the NEXT framing pass. It
// survives resetShortFraming (which runs per pass) precisely because the whole
// point is to carry one pass's conclusion into the next one; the critic loop
// clears it between shorts.
var shortTarget string

// setShortTarget aims every grounding sweep of the next pass at one named thing.
func setShortTarget(s string) { shortTarget = s }

// sweep grounds [start,end] once and returns the result for every later caller.
// The FIRST span to ask pays for the whole window; every other span is free.
func (c *groundCache) sweep(g *ground.Runner, src string) (ground.Result, error) {
	if c.done {
		return c.res, c.err
	}
	c.done = true
	start, end := c.winStart, c.winEnd
	if end <= start {
		c.err = errNoWindow
		return c.res, c.err
	}
	c.res, c.err = g.Run(ground.Options{Video: src, Start: start, End: end,
		FPS: groundSweepFPS, MaxFrames: groundSweepMaxFrames, Target: c.target})
	return c.res, c.err
}

// samplesIn returns the sweep's subject positions falling inside [start,end],
// which is what one span needs.
//
// A span too short to contain a sample of its own borrows the NEAREST one
// rather than reporting nothing: at one sample a second a 0.4s span can easily
// fall between two, and "no sample landed in this slice" is not the same fact as
// "there is nothing here". The nearest sighting is a shot away at most, because
// the pan path treats cuts as walls anyway.
func (c *groundCache) samplesIn(start, end, maxArea float64) []ground.Sample {
	all := ground.Samples(c.res, maxArea)
	var in []ground.Sample
	for _, s := range all {
		if s.T >= start && s.T <= end {
			in = append(in, s)
		}
	}
	if len(in) > 0 || len(all) == 0 {
		return in
	}
	mid := (start + end) / 2
	best := all[0]
	for _, s := range all[1:] {
		if absf(s.T-mid) < absf(best.T-mid) {
			best = s
		}
	}
	// Re-timed to this span's midpoint: its POSITION is the useful part, its
	// original timestamp belongs to a neighbouring span.
	return []ground.Sample{{T: mid, X: best.X}}
}
