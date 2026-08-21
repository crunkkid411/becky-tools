// splice.go — keep the tracking that WORKED, replace only the part that didn't.
//
// Jordan, 2026-08-21, about a clip that had already gone viral:
//
//	"I've already said MULTIPLE times that tracking a subject does not determine
//	 if the clip is good or not. ... All these data points are to help becky
//	 conceptually understand what is happening in the video so it can make
//	 accurate decisions. If it's claiming an already viral video has nothing
//	 usable, it's clearly broken."
//
// THE BUG THIS FIXES, measured on his own footage. The pose tracker followed him
// perfectly for the first nine seconds of a 33-second walk-and-talk, then he
// stepped behind the camera and was gone for the rest. That is 331 tracked
// frames out of 985 — and becky threw away ALL 985 of them, because one dead
// stretch was 3.4s and the gate said 2.0s. The whole short then got ONE static
// crop chosen from four sampled frames of a completely different part of the
// clip, and the opening close-up of his face rendered as a dark door.
//
// A gate that turns 331 good frames into zero is not measuring quality, it is
// discarding evidence. So: the tracked stretches keep their tracked framing, and
// only the DEAD stretches are handed to the framing ladder. Nothing is refused,
// nothing good is thrown away, and the note says exactly which seconds came from
// which source.
package main

import (
	"fmt"

	"becky-go/internal/crop"
)

// spliceMinTracked is the shortest run of real detections worth keeping as its
// own tracked stretch. Below this it is a flicker of recognition inside a dead
// patch, not a shot the tracker actually held, and splicing it in would make the
// crop jump for a third of a second and jump back.
const spliceMinTracked = 1.0

// spliceRun is one contiguous stretch of the path, and whether the subject was
// really there for it.
type spliceRun struct {
	from, to int // indexes into the path, [from,to)
	tracked  bool
}

// spliceRuns groups a path into alternating tracked / dead stretches, absorbing
// runs too short to be worth a framing change of their own.
//
// A miss shorter than maxGap is a glance away and stays part of the tracked run
// around it — that is the behaviour maxGap always described, applied to a
// STRETCH instead of to the whole window.
func spliceRuns(rects []crop.Rect, fps, maxGap float64) []spliceRun {
	if len(rects) == 0 || fps <= 0 {
		return nil
	}
	minTracked := int(spliceMinTracked*fps + 0.5)
	maxMiss := int(maxGap*fps + 0.5)

	// 1. Raw runs of seen / not-seen.
	var runs []spliceRun
	cur := spliceRun{from: 0, to: 1, tracked: rects[0].Seen}
	for i := 1; i < len(rects); i++ {
		if rects[i].Seen == cur.tracked {
			cur.to = i + 1
			continue
		}
		runs = append(runs, cur)
		cur = spliceRun{from: i, to: i + 1, tracked: rects[i].Seen}
	}
	runs = append(runs, cur)

	// 2. A short miss is a glance away, and a short sighting is a flicker.
	//    Both get absorbed into their neighbours so the crop does not twitch.
	merged := runs[:0]
	for _, r := range runs {
		n := r.to - r.from
		short := (!r.tracked && n <= maxMiss) || (r.tracked && n < minTracked)
		if short && len(merged) > 0 {
			merged[len(merged)-1].to = r.to
			continue
		}
		if short && len(merged) == 0 && len(runs) > 1 {
			// A short opening run joins whatever follows it.
			r.tracked = !r.tracked
		}
		merged = append(merged, r)
	}
	// Re-join anything that ended up adjacent with the same verdict.
	out := merged[:0]
	for _, r := range merged {
		if len(out) > 0 && out[len(out)-1].tracked == r.tracked {
			out[len(out)-1].to = r.to
			continue
		}
		out = append(out, r)
	}
	return out
}

// spliceTracked rebuilds a camera path that keeps the tracker's own framing over
// every stretch it really tracked, and uses fill (the framing ladder's answer for
// this span) over every stretch it did not.
//
// It returns the spliced path, the seconds that came from real tracking, and the
// seconds that came from the ladder. ok is false when there is nothing tracked
// worth keeping, in which case the caller should just use the ladder as before.
func spliceTracked(rects []crop.Rect, fill []crop.Rect, fps, maxGap float64) (out []crop.Rect, trackedSec, filledSec float64, ok bool) {
	runs := spliceRuns(rects, fps, maxGap)
	if len(runs) == 0 {
		return nil, 0, 0, false
	}
	anyTracked, anyDead := false, false
	for _, r := range runs {
		if r.tracked {
			anyTracked = true
		} else {
			anyDead = true
		}
	}
	// Nothing to splice: the caller's existing all-or-nothing answer is correct.
	if !anyTracked || !anyDead {
		return nil, 0, 0, false
	}

	out = make([]crop.Rect, 0, len(rects))
	for _, r := range runs {
		secs := float64(r.to-r.from) / fps
		if r.tracked {
			trackedSec += secs
			out = append(out, rects[r.from:r.to]...)
			continue
		}
		filledSec += secs
		// The ladder answered with either one static rect or its own path. Hold
		// the static one across the dead stretch; sample a path at each frame so
		// the two sources stay on one timeline.
		for i := r.from; i < r.to; i++ {
			f := rectAtTime(fill, rects[i].T)
			f.T = rects[i].T
			out = append(out, f)
		}
	}
	return out, trackedSec, filledSec, true
}

// rectAtTime picks the fill rect in force at t. A single-rect fill is static and
// answers for every time; a multi-rect fill is a pan and answers with the last
// keyframe at or before t.
func rectAtTime(fill []crop.Rect, t float64) crop.Rect {
	if len(fill) == 0 {
		return crop.Rect{}
	}
	best := fill[0]
	for _, r := range fill[1:] {
		if r.T > t {
			break
		}
		best = r
	}
	return best
}

// spliceNote is what Jordan reads. It says which seconds are really tracked and
// which are the ladder's, because "followed: false" on its own told him nothing
// except that something had gone wrong.
func spliceNote(trackedSec, filledSec float64, ladder string) string {
	return fmt.Sprintf("the tracker held the subject for %.1fs of this span and lost him for %.1fs, so "+
		"the tracked seconds keep their tracked framing and only the rest came from the ladder (%s)",
		trackedSec, filledSec, ladder)
}
