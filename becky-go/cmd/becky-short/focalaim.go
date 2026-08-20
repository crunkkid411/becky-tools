// focalaim.go — what to do with a span that has no trackable subject.
//
// Until now the answer was "a dead-centre crop", and on 16:9 source that is the
// "split the difference" framing Jordan's own edit specifically avoids. Measured
// on the BLINDFOLD render, five of seventeen spans took that fallback and landed
// on a green wall and a striped sleeve.
//
// His RULE 4 says the shot is not empty just because nobody is in it — on a POV
// shot "the clip itself is obviously meant to be the focal point", and the thing
// he points at is defined by its MOTION. internal/focal measures exactly that
// and refuses when it cannot defend an answer, so this file is the thin join:
// ask, and use it only when it says yes.
package main

import (
	"fmt"

	"becky-go/internal/config"
	"becky-go/internal/crop"
	"becky-go/internal/focal"
)

// OFF BY DEFAULT, and the measurement is why.
//
// Wired into the untracked-span fallback and rendered on the BLINDFOLD master
// over the window Jordan cut his own short from, four spans changed:
//
//	t=3.0s  a two-shot with BOTH heads clipped became one person, centred  BETTER
//	t=5.0s  same shot, same improvement                                    BETTER
//	t=26.0s framed a man's chest with his head cut off the top             WORSE
//	t=27.5s framed a COFFEE MACHINE while two people talked                WORSE
//
// Two better, two worse is not an improvement, it is a coin toss with a
// confident note attached. His own rule decides it: "a wrong focal point is
// worse than a centre crop", and "≥2 independent signals agreeing → state the
// conclusion; a lone weak signal → unknown". Motion alone IS the lone weak
// signal. Centre is the honest "unknown".
//
// The tempting pattern in those four is that aiming helped where the tracker
// had PARTIAL coverage (motion found the badly-tracked person) and hurt where
// it had none (motion found the background). That is four data points on one
// clip and it is not enough to ship a rule on, so it is written down rather
// than coded.
//
// What would make this shippable is the second signal: a VL naming what the
// viewer should be on, agreeing with where the motion is. becky-validate can
// already produce the first half on this exact footage ("FOCUS = the coiled
// yellow object, CHANGE = 46.0s"). Joining them is the open work.

// aimStaticCrop upgrades an untracked span's framing from dead centre to
// wherever the motion actually is, and returns the note explaining what it did.
//
// It returns nil rects when it cannot improve on centre, which leaves the
// caller's existing static-centre path exactly as it was. That is the whole
// safety property: a wrong focal point is worse than a centre crop, so this
// either does better or does nothing.
func aimStaticCrop(cfg config.Config, src string, start, end float64, aspect float64,
	srcW, srcH int, fps float64) ([]crop.Rect, string) {

	a, err := focal.Find(cfg.FFmpeg, src, start, end, fps)
	if err != nil {
		return nil, "no subject to track and the focal-point pass failed (" + firstLine(err) + "); STATIC CENTRE crop"
	}
	if !a.Stable {
		reason := a.Reason
		if reason == "" {
			reason = "no stable focal point"
		}
		return nil, "no subject to track and no stable focal point (" + reason + "); STATIC CENTRE crop"
	}
	r := crop.StaticAt(srcW, srcH, aspect, a.X)
	return []crop.Rect{r}, fmt.Sprintf(
		"no subject to track; aimed the static crop at the moving focal point "+
			"(x=%.2f of frame width, steady to %.3f over %d frame pairs) instead of dead centre",
		a.X, a.Spread, a.Pairs)
}
