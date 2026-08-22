// framing.go — the framing LADDER. It does not refuse.
//
// Jordan, 2026-08-20, after a whole clip came back empty:
//
//	"Lots of videos might not have perfect framing opportunities, but that
//	 doesn't mean to refuse the clip altogether. We have so many local tools in
//	 becky-tools, it should not be failing like this. ... I don't care if it has
//	 to use like 15 different models iteratively to understand it or get it
//	 right - in fact, that's almost certainly the ONLY way we're going to get
//	 anything worth using."
//
// He is right and the previous version over-corrected. Banning the SILENT centre
// crop was correct; turning that into "refuse the span, and with it the short"
// was not. Those are not the only two options — there is a whole ladder of
// signals this repo already owns and was not asking.
//
// THE LADDER. Each rung is tried in order and the first confident answer wins.
// Every rung says which one answered, so the output is never a mystery:
//
//	0 SPEAKER   Several faces on screen and LR-ASD says which one is TALKING
//	            (speakeraim.go). Only fires on a real multi-person shot with a
//	            clear winner; silent on everything else, which on POV and
//	            hidden-camera footage is most of it.
//	1 POSE      MediaPipe body tracking, per frame. The best answer when it works.
//	2 PAN       STEADY grounded boxes that MOVE become a camera path, not a
//	            refusal. This is the rung that was missing: a subject crossing
//	            46% of the frame is a pan, and becky called it unframeable.
//	3 AIM       STEADY grounded boxes that HOLD become a static crop aimed at them.
//	4 FALCON    A second, independent open-vocabulary detector (ONNX, no torch)
//	            for the shots Reka cannot see. Two architectures agreeing is real
//	            corroboration; either one alone is a candidate.
//	5 MOTION    internal/focal — where the movement is, when nothing is nameable.
//	6 HINT      An UNSTABLE grounded sighting, held still. Good enough to beat a
//	            centre crop, never good enough to steer a camera or to out-vote
//	            the person detector on rung 4 (see the stability note below).
//	7 INHERIT   What the REST OF THIS SHORT is framed on. A hidden-camera wide
//	            shot with nobody detectable is still the same scene as the shot
//	            before it, and continuity is a real signal, not a guess.
//	8 CENTRE    Only when no rung above answered ANYWHERE in the short. Labelled
//	            as the honest unknown it is — never silent, never a default.
//
// Rung 8 is reached when the footage genuinely offers nothing, and Jordan's rule
// still holds: it is allowed as a REASON, never as an assumption.
//
// NOTHING ON THIS LADDER DECIDES WHETHER THE CLIP IS ANY GOOD. Jordan, twice:
// "tracking a subject does not determine if the clip is good or not... All these
// data points are to help becky conceptually understand what is happening in the
// video so it can make accurate decisions." So every rung answers WHERE TO POINT
// and nothing else. What the clip IS, and where it starts and ends, is the
// watching model's call (watchpass.go) — never the tracker's.
package main

import (
	"fmt"

	"becky-go/internal/config"
	"becky-go/internal/crop"
	"becky-go/internal/focal"
	"becky-go/internal/ground"
)

// panMinTravel is how far a grounded subject must move before the crop pans
// rather than holding still. Below this the "movement" is detector jitter, and a
// crop that drifts a few pixels reads as a wobble.
const panMinTravel = 0.05

// panOutFPS / panSmoothSeconds resample the ~1fps grounded positions into a
// camera path. 12/s is well above what the eye reads as stepping, and a 1.0s
// moving average keeps the move slow enough to look operated rather than
// tracked.
const (
	panOutFPS        = 12.0
	panSmoothSeconds = 1.0
)

// framingMemory carries what the REST of this short settled on, so a span with
// no detectable subject can inherit the scene's framing instead of guessing
// (rung 6). It is filled in as spans resolve and read by later ones.
type framingMemory struct {
	sumX float64
	n    int
}

func (m *framingMemory) remember(x float64) {
	m.sumX += x
	m.n++
}

// known reports the framing the short has settled on so far, if any.
func (m *framingMemory) known() (float64, bool) {
	if m.n == 0 {
		return 0, false
	}
	return m.sumX / float64(m.n), true
}

// shortFraming is what THIS SHORT has settled on, shared by every span of it so
// rung 6 (INHERIT) can work. Package-level for the same reason the grounding
// runner is: one process renders one job at a time, and threading a pointer
// through render -> renderJumpcutShort -> resolveCrop -> fallback would touch
// four signatures to carry two floats.
var shortFraming framingMemory

// resetShortFraming clears the memory between shorts in a --reel run. Without
// it, short 3 would inherit short 1's framing, which is a different scene.
func resetShortFraming(winStart, winEnd float64) {
	shortFraming = framingMemory{}
	resetShortGround(winStart, winEnd)
	resetShortSpeaker(winStart, winEnd)
	setShortPayoff(0)
	setShortWatched(false)
}

// frameSpan walks the ladder for ONE span and always returns something to
// render. ok is false only when the source could not even be measured.
// start/end are ONE span. The whole short's window is grounded in a single
// cached sweep (groundcache.go) and this span reads the slice of it it needs.
func frameSpan(cfg config.Config, g *ground.Runner, mem *framingMemory, src string,
	start, end, aspect, fps float64, srcW, srcH int, cuts []float64) (rects []crop.Rect, note string, grounded bool) {

	// --- rung 0: WHICH PERSON. Only when there is a choice to make ---
	//
	// This sits above everything else because "which of these people" is a
	// different and prior question to "where is a person". Pose answers the
	// second one and, on a two-shot, answers it with whoever is nearest the
	// lens — which is how a reaction shot ends up framed on the back of the
	// prankster's head. It stays silent unless several faces are tracked AND
	// LR-ASD picks a clear winner, so a POV shot pays only the detection.
	if sr, snote, sok := speakerAim(cfg, src, start, end, aspect, srcW, srcH, cuts); sok {
		if len(sr) > 0 && srcW > 0 {
			mid := sr[len(sr)/2]
			mem.remember((float64(mid.X) + float64(mid.W)/2) / float64(srcW))
		}
		return sr, snote, true
	}

	// --- rungs 2-3: what is in this shot, and where ---
	//
	// STABILITY IS THE GATE HERE, and it is not a new rule invented in this
	// file — it is ground.py's own contract, which this caller was ignoring.
	// `stable` means the boxes agreed with each other across the window: seen in
	// at least half the frames, and never jumping more than a quarter of the
	// frame between sightings. Of an UNSTABLE result ground.py says, in its own
	// words, "treat as a HINT about which region matters, not as a camera path".
	//
	// Ignoring that is what put a Pikachu poster in charge of Jordan's short on
	// 2026-08-21: the model saw a "colorful poster" in 9 of 33 frames (27%, so
	// not stable by any reading) and becky PANNED across 75% of the frame
	// chasing it, straight past the person sitting at the desk. An unstable
	// sighting is now kept as a HINT and used at the BOTTOM of the ladder if
	// nothing better answers — it never steers the camera, and it never
	// pre-empts the person detector on the rung below.
	var (
		hintX    float64
		haveHint bool
	)
	if g != nil {
		res, err := shortGround.sweep(g, src)
		switch {
		case err != nil:
			note = "grounding failed (" + firstLineStr(err.Error()) + ")"
		case !res.OK:
			note = "nothing grounded: " + res.Reason
		default:
			subject := res.Target
			if res.Named != "" {
				subject = res.Named
			}
			// An occlusion carries no position, so the sweep drops those frames
			// outright rather than letting a full-frame box vote on where to aim.
			samples := shortGround.samplesIn(start, end, occlusionArea)
			switch {
			case len(samples) == 0:
				note = fmt.Sprintf("%q could not be located in any sampled frame", subject)

			case !res.Stable:
				hintX, haveHint = meanX(samples), true
				note = fmt.Sprintf("grounded %q, but only in %.0f%% of frames and jumping between "+
					"them, so it is a hint about where to look rather than something to point a "+
					"camera at (%d sightings in %d frames)",
					subject, res.FoundFrac*100, len(samples), res.Frames)

			default:
				// Shot boundaries inside this span are HARD WALLS for the pan —
				// a multi-camera cut is not a camera move (ground.PanPath).
				path := ground.PanPath(samples, start, end, panOutFPS, panSmoothSeconds, cuts)
				travel := ground.Travel(path)
				if travel >= panMinTravel && len(path) > 1 {
					// RUNG 2 — PAN.
					rects = make([]crop.Rect, 0, len(path))
					for _, p := range path {
						r := crop.StaticAt(srcW, srcH, aspect, p.X)
						r.T = p.T
						rects = append(rects, r)
					}
					mid := path[len(path)/2].X
					mem.remember(mid)
					return rects, fmt.Sprintf(
						"no person to track; grounded %q steadily and PANNED with it across %.0f%% of the "+
							"frame (%d sightings in %d frames)", subject, travel*100, len(samples), res.Frames), true
				}
				// RUNG 3 — AIM. It holds still, so hold the crop still too.
				x := meanX(samples)
				mem.remember(x)
				where := fmt.Sprintf("aimed the crop at x=%.2f", x)
				if absf(x-0.5) <= centredEnough {
					where = "already centred in the source, so a centre crop IS the subject"
				}
				return []crop.Rect{crop.StaticAt(srcW, srcH, aspect, x)},
					fmt.Sprintf("no person to track; grounded %q steadily and %s (%d sightings in %d frames)",
						subject, where, len(samples), res.Frames), true
			}
		}
	} else {
		note = "the grounding model is not available"
	}

	// --- rung 4: a SECOND detector, different architecture ---
	if x, n, ok := falconAimX(cfg, src, start, end); ok {
		mem.remember(x)
		return []crop.Rect{crop.StaticAt(srcW, srcH, aspect, x)},
			fmt.Sprintf("%s; a second detector (Falcon-Perception) found a person in %d frame(s) "+
				"and the crop is aimed at x=%.2f", note, n, x), true
	}

	// --- rung 5: where the movement is ---
	if a, err := focal.Find(cfg.FFmpeg, src, start, end, fps); err == nil && a.Stable {
		mem.remember(a.X)
		return []crop.Rect{crop.StaticAt(srcW, srcH, aspect, a.X)},
			fmt.Sprintf("%s; nothing nameable here, so the crop follows the MOTION instead "+
				"(x=%.2f, steady to %.3f over %d frame pairs)", note, a.X, a.Spread, a.Pairs), true
	}

	// --- rung 6: the unstable grounding hint, now that nothing better answered ---
	// It was not good enough to steer a camera and it was not good enough to
	// out-vote a person detector, but it IS better than centring on nothing.
	if haveHint {
		mem.remember(hintX)
		return []crop.Rect{crop.StaticAt(srcW, srcH, aspect, hintX)},
			fmt.Sprintf("%s; nothing steadier answered either, so the crop HOLDS STILL aimed at that "+
				"hint (x=%.2f) instead of panning around after it", note, hintX), true
	}

	// --- rung 7: what the rest of this short is framed on ---
	if x, ok := mem.known(); ok {
		return []crop.Rect{crop.StaticAt(srcW, srcH, aspect, x)},
			fmt.Sprintf("%s; nothing could be located in this shot, so it INHERITS the framing the "+
				"rest of this short settled on (x=%.2f) rather than snapping to centre", note, x), true
	}

	// --- rung 8: the honest unknown, said out loud ---
	return []crop.Rect{crop.StaticCenter(srcW, srcH, aspect)},
		note + "; nothing in this entire short could be located, so this is a CENTRE crop and " +
			"that is a guess, not a decision — the framing here is unverified", false
}

// meanX is the average horizontal position of a set of sightings, which is
// where a static crop aimed at them belongs.
func meanX(samples []ground.Sample) float64 {
	if len(samples) == 0 {
		return 0.5
	}
	var sum float64
	for _, s := range samples {
		sum += s.X
	}
	return sum / float64(len(samples))
}

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// maxSpanNotes is how many DISTINCT per-span framing notes reach the JSON
// report. The rest are counted and summarised, and every one of them is still
// printed by --verbose.
//
// Jordan reads this output and reading costs him physically (ACCESSIBILITY.md).
// One 30-span short emitted a single ~700-word note that was the same two
// sentences thirty times with different decimals - which buries the one line
// that actually mattered (which window the model chose, and why).
const maxSpanNotes = 4
