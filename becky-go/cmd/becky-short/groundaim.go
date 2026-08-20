// groundaim.go — what to frame when the person tracker has nothing.
//
// THE RULE THIS ENFORCES. Jordan, 2026-08-20: "defaulting to center crop is not
// okay - it makes the video end up in the recycle bin. If there's a REASON to
// focus on center (like an inanimate object, or it's simply correctly framed
// already, etc) that's totally fine, but assuming center crop is correct is
// wrong - every frame needs to be meticulously approved."
//
// So a centre crop is now something becky must EARN, not something it falls
// into. There are exactly three outcomes here and no fourth:
//
//	AIMED    a grounded box says where the subject is; frame there.
//	CENTRED  a grounded box says the subject is ALREADY centred; frame centre
//	         and say WHY - that is the "it's simply correctly framed already"
//	         case he explicitly allowed.
//	REFUSED  nothing could be grounded. The span does not render.
//
// The thing that made this possible is not new capability, it is WIRING.
// internal/pyhelpers/ground.py — Reka Edge grounded detection, "Detect: <thing>"
// to a box — was written, verified on this machine, documented, and then never
// embedded or called by anything. focalaim.go's own comment had already written
// down what was missing: "What would make this shippable is the second signal: a
// VL naming what the viewer should be on, agreeing with where the motion is."
// That is what this file finally joins up.
package main

import (
	"fmt"
	"math"
	"sync"

	"becky-go/internal/config"
	"becky-go/internal/crop"
	"becky-go/internal/ground"
)

// The grounding server is a process-wide singleton, started LAZILY on the first
// span that actually needs it and shared by every span after.
//
// Lazy because the start costs ~45 seconds and most spans never reach the
// fallback — the pose tracker handles them. Paying 45s up front on every job,
// including the ones that never ask a question, is exactly the kind of cost
// that gets a feature turned off again.
//
// Package-level rather than threaded through render -> renderJumpcutShort ->
// resolveCrop because those already carry a dozen parameters and one process
// only ever wants one server. groundOnce makes the start race-free.
var (
	groundOnce   sync.Once
	groundRunner *ground.Runner
	groundErr    error
)

// grounder returns the shared grounding runner, starting it on first use.
// A failure is remembered, so a machine without the model pays the failed start
// once rather than once per span.
func grounder(cfg config.Config, logf func(string, ...any)) (*ground.Runner, error) {
	groundOnce.Do(func() {
		groundRunner, groundErr = ground.New(cfg, logf)
	})
	return groundRunner, groundErr
}

// closeGrounder shuts the shared server down. main defers it.
func closeGrounder() {
	if groundRunner != nil {
		groundRunner.Close()
		groundRunner = nil
	}
}

// centredEnough is how close a grounded subject's centre must sit to the
// frame's own centre before framing it dead centre is the SAME decision as
// framing the subject. Inside this, aiming and centring produce the same rect
// anyway, so the note says "already framed" rather than implying a choice.
const centredEnough = 0.06

// occlusionArea is the share of the frame a grounded box may cover before it
// stops being a SUBJECT and starts being an OCCLUSION.
//
// Measured on the BLINDFOLD master at 552-557s: someone's shirt swings into the
// lens and black-and-white fabric fills the whole frame for five seconds. Reka
// dutifully grounds it — it IS a shirt, and it IS right there — and a crop
// "aimed" at a box covering the entire frame is a centre crop with a confident
// note attached, which is the precise thing this file exists to prevent.
//
// Nothing a viewer is meant to look at fills 92% of a 16:9 frame. A box that
// big means the camera is blocked, and blocked footage has no framing, so the
// span is refused and the dead-tail trim takes it out.
const occlusionArea = 0.92

// occlusionFrames is how many of the sighted frames must be occluded before the
// span is refused. One blocked frame in eight is someone walking past the lens
// and the shot survives it; a third or more means the camera is covered for the
// span and there is no framing to find.
const occlusionFrames = 0.34

// groundUnstableMaxSpread caps how far an UNSTABLE grounded subject may wander
// before its mean position stops meaning anything. ground.py already reports
// stability; this is the second gate on the one number it exports, because a
// mean of two boxes at opposite edges points at the wall between them.
const groundUnstableMaxSpread = 0.30

// aimByGrounding is the replacement for the dead-centre fallback.
//
// It returns rects to render, a note for the report, and ok=false meaning
// REFUSE THIS SPAN. It never returns a centre crop with no reason attached —
// that is the entire point of the file.
func aimByGrounding(g *ground.Runner, src string, start, end float64, aspect float64,
	srcW, srcH int) (rects []crop.Rect, note string, ok bool) {

	if g == nil {
		return nil, "no subject to track and the grounding model is not available, so there is " +
			"nothing that can say where to point — REFUSED rather than centred", false
	}

	res, err := g.Run(ground.Options{Video: src, Start: start, End: end})
	if err != nil {
		return nil, "no subject to track and grounding failed (" + firstLineStr(err.Error()) +
			") — REFUSED rather than centred", false
	}
	if !res.OK {
		reason := res.Reason
		if reason == "" {
			reason = "the grounding pass returned nothing"
		}
		return nil, "no subject to track: " + reason, false
	}

	box, n, spread, have := ground.BestWithSpread(res)
	if !have || n == 0 {
		return nil, "no subject to track and nothing grounded in this span — REFUSED rather than centred", false
	}

	subject := res.Target
	if res.Named != "" {
		subject = res.Named
	}

	// A box that fills the frame is an occlusion, not a subject — see
	// occlusionArea. Counted PER FRAME, never averaged: see ground.OccludedFrac
	// for the measurement that shows why the mean hides exactly this case.
	if f := ground.OccludedFrac(res, occlusionArea); f >= occlusionFrames {
		return nil, fmt.Sprintf(
			"no person to track, and %.0f%% of the sampled frames are filled edge to edge by one "+
				"thing (%q) — the camera is blocked, not pointed at something; REFUSED rather than centred",
			f*100, subject), false
	}

	// The gate is on how far THE CHOSEN SUBJECT wanders, not on how widely
	// spread every detected thing is — see ground.BestWithSpread for why that
	// distinction refused a perfectly framable three-shot. A subject that
	// crosses more than this between samples has no single framing that holds
	// it, and a mean position between its extremes points at the wall.
	if spread > groundUnstableMaxSpread {
		return nil, fmt.Sprintf(
			"no person to track, and the grounded subject (%q) moves across %.0f%% of the frame "+
				"in this span, so no single framing holds it — REFUSED rather than centred",
			subject, spread*100), false
	}

	r := crop.StaticAt(srcW, srcH, aspect, box.CenterX())
	if math.Abs(box.CenterX()-0.5) <= centredEnough {
		return []crop.Rect{r}, fmt.Sprintf(
			"no person to track, but %q was grounded at x=%.2f — already centred in the source, "+
				"so a centre crop IS the subject (found in %d of %d sampled frames)",
			subject, box.CenterX(), n, res.Frames), true
	}
	return []crop.Rect{r}, fmt.Sprintf(
		"no person to track; grounded %q at x=%.2f of frame width and aimed the crop there, "+
			"not at centre (found in %d of %d sampled frames, stable=%v)",
		subject, box.CenterX(), n, res.Frames, res.Stable), true
}

// firstLineStr trims a message to its first line for a report note.
func firstLineStr(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
