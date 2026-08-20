// deadtail.go — a short must not END on footage with nothing to look at.
//
// becky's own --review pass caught this on the first two shorts the one-click
// chain produced from Jordan's long-form:
//
//	01: the subject is off screen for 7.7s in a row at 20.5-28.2s (limit 2.0s)
//	02: the subject is off screen for 2.3s in a row at 13.2-15.6s (limit 2.0s)
//
// Both at the END. Looking at short 01's last five seconds, the camera is right
// up against a shoulder and a blindfold: a purple blur with nothing in it, more
// than a quarter of the running time, and it is what the viewer is left on.
//
// A face-less shot in the MIDDLE is legitimate and deliberate — it is Jordan's
// RULE 4, the POV shot where the prop is the subject. A face-less shot at the
// END is different: a short ends on its payoff, and a payoff has something to
// look at. So this trims only the TAIL, and only while every span it removes
// failed to track.
package main

import (
	"becky-go/internal/config"
)

const (
	// deadTailMaxFrac is how much of a short's planned time the tail trim may
	// remove. A short that is mostly untrackable is not a short with a bad
	// ending, it is a bad pick, and quietly cutting it to a third of its length
	// would hide that from the moment ranker rather than fix it.
	deadTailMaxFrac = 0.35

	// deadTailMinKeep is the shortest short worth rendering, in seconds. Below
	// this there is nothing left to post, so the tail stays and --review says so.
	deadTailMinKeep = 5.0
)

// trimDeadTail drops trailing spans that hold no trackable subject. It returns
// the spans to render, how many seconds it removed, and how many spans it cut.
//
// It re-resolves the crop for the trailing spans it inspects, which the main
// render loop will then do again for the ones that survive. That is one extra
// pose pass over at most a couple of seconds of video and it buys not having to
// restructure the render loop around a two-phase plan.
func trimDeadTail(cfg config.Config, j job, spans []keepSpan, aspectStr string,
	sampleFPS, minCov, maxGap float64, cuts []float64) (kept []keepSpan, droppedSec float64, droppedSpans int) {

	if len(spans) < 2 {
		return spans, 0, 0
	}
	total := spansDuration(spans)
	budget := total * deadTailMaxFrac

	end := len(spans)
	for end > 1 {
		sp := spans[end-1]
		d := sp.Out - sp.In
		if droppedSec+d > budget || total-(droppedSec+d) < deadTailMinKeep {
			break
		}
		cr, err := resolveCrop(cfg, j.Src, sp.In, sp.Out, aspectStr, sampleFPS, minCov, maxGap, false,
			cutsWithinSpan(cuts, sp.In, sp.Out))
		// A span that resolved with a followed path is a real ending — stop.
		// err != nil means resolveCrop REFUSED it (no subject, or a long gap),
		// which is exactly the case this trims.
		if err == nil && cr.Followed {
			break
		}
		end--
		droppedSec += d
		droppedSpans++
	}
	if droppedSpans == 0 {
		return spans, 0, 0
	}
	return spans[:end], droppedSec, droppedSpans
}
