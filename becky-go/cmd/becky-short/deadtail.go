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
	"becky-go/internal/crop"
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

	// deadTailMinTrim is the smallest trailing gap worth cutting off the end of
	// the final span. Below this it is a blink, not an ending on nothing, and
	// shaving a fifth of a second off every short would be noise.
	deadTailMinTrim = 0.4
)

// shortPayoff is the moment the watching model identified as THE PAYOFF, in
// source seconds, or 0 when it did not watch. Nothing may trim it away.
//
// THIS IS THE GUARD JORDAN'S PRANK NEEDED. The model chose 0-58.6s and put the
// payoff at 55.4s - correctly, it is the mouse trap snapping shut on Robby. The
// tail trim then removed NINETEEN SECONDS, ten spans, because Robby is across
// the room and the pose tracker cannot follow him: "no trackable subject" is
// true of the payoff and utterly irrelevant to whether it belongs in the clip.
//
// So "the short must not end on nothing" is now subordinate to "the short must
// contain the thing it is about". A payoff with no trackable person in it is
// still the payoff.
var shortPayoff float64

// shortWatched is true when the model actually WATCHED this short and returned a
// window. When it did, the out point is the model's decision and the pose
// tracker does not get to revise it.
//
// Jordan, 2026-08-21, on a clip that had already gone viral: "I've already said
// MULTIPLE times that tracking a subject does not determine if the clip is good
// or not... All these data points are to help becky conceptually understand what
// is happening in the video so it can make accurate decisions."
//
// He is right, and the tail trim was the one place a tracker still had a vote on
// CONTENT rather than on framing. "Nothing to look at" is a claim about the
// footage, and a pose model that cannot follow a person across a room is not
// entitled to make it — it is the same false negative that ate the mouse trap
// payoff. So: the model watched, the model chose the end, the end stands.
var shortWatched bool

// setShortPayoff records the watched payoff for this short. Reset per short
// alongside the framing memory.
func setShortPayoff(t float64) { shortPayoff = t }

// setShortWatched records that the model watched this short and its window is
// the model's own answer.
func setShortWatched(b bool) { shortWatched = b }

// payoffProtected reports whether a span may not be trimmed because the payoff
// lands inside it or after it. payoffGrace keeps the reaction that FOLLOWS the
// action - a payoff cut the instant it happens is a payoff with no landing.
func payoffProtected(sp keepSpan) bool {
	return shortPayoff > 0 && sp.Out > shortPayoff-payoffGrace
}

// payoffGrace is how much room before the payoff is also protected, so the
// build-up to it survives too.
const payoffGrace = 2.0

// trimDeadTail drops trailing spans that hold no trackable subject. It returns
// the spans to render, how many seconds it removed, and how many spans it cut.
//
// It re-resolves the crop for the trailing spans it inspects, which the main
// render loop will then do again for the ones that survive. That is one extra
// pose pass over at most a couple of seconds of video and it buys not having to
// restructure the render loop around a two-phase plan.
func trimDeadTail(cfg config.Config, j job, spans []keepSpan, aspectStr string,
	sampleFPS, minCov, maxGap float64, cuts []float64) (kept []keepSpan, droppedSec float64, droppedSpans int) {

	// THE MODEL'S OUT POINT IS THE OUT POINT (see shortWatched above). This trim
	// exists for the case where nobody watched and becky is guessing from cheap
	// signals; it is not a second opinion on a decision already made by the only
	// thing here that understood the clip.
	if shortWatched {
		return spans, 0, 0
	}
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
		// THE PAYOFF IS NOT DEAD AIR, whatever the tracker says about it.
		if payoffProtected(sp) {
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
	// COPIED, not resliced: the in-span trim below writes to the last element,
	// and spans[:end] shares its backing array with the caller's slice, so
	// writing through it would silently rewrite the plan the caller still holds.
	kept = append([]keepSpan(nil), spans[:end]...)

	// AND THEN TRIM INSIDE THE LAST SURVIVING SPAN.
	//
	// Dropping whole spans cannot fix the common case, because the last span
	// usually STARTS on the subject and only goes blank at its end — so it
	// passes coverage and longest-gap, and dropping it would throw away the good
	// seconds with the bad. Measured on the BLINDFOLD master: the short ended on
	// 2.0 seconds of a shirt swinging across the lens, inside a span whose first
	// half was fine, and the whole-span trim above removed 0.80s elsewhere and
	// left every frame of it.
	//
	// crop.Path.TrailingGap measures exactly that stretch, so the fix is to cut
	// it off. Same budget as the span trim — this cannot eat the short either.
	if len(kept) > 0 && !payoffProtected(kept[len(kept)-1]) {
		last := kept[len(kept)-1]
		p, err := crop.Run(cfg, crop.Options{Video: j.Src, Start: last.In, End: last.Out,
			Aspect: aspectStr, FPS: sampleFPS, Model: cfg.PoseModel,
			CutTimes: cutsWithinSpan(cuts, last.In, last.Out)})
		if err == nil && p.TrailingGap >= deadTailMinTrim {
			cut := p.TrailingGap
			if remaining := last.Out - last.In - cut; remaining < jumpcutMinSpan {
				cut = last.Out - last.In - jumpcutMinSpan
			}
			if over := droppedSec + cut - budget; over > 0 {
				cut -= over
			}
			if cut >= deadTailMinTrim {
				kept[len(kept)-1].Out = last.Out - cut
				droppedSec += cut
			}
		}
	}

	if droppedSpans == 0 && droppedSec == 0 {
		return spans, 0, 0
	}
	return kept, droppedSec, droppedSpans
}
