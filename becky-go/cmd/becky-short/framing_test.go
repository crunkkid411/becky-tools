package main

import (
	"strings"
	"testing"

	"becky-go/internal/config"
	"becky-go/internal/ground"
)

// posterSweep is the REAL sweep that broke Jordan's short on 2026-08-21: the
// grounding model named a "colorful poster" and saw it in 9 of 33 frames, in two
// completely different places (a wall on the left, then a wall on the right).
// becky treated that as a camera path and PANNED across 75% of the frame chasing
// a Pikachu poster, past the person sitting at the desk.
//
// ground.py's own contract calls this NOT stable and says of it: "treat as a
// HINT about which region matters, not as a camera path".
func posterSweep(stable bool) ground.Result {
	var dets []ground.Detection
	for i := 0; i < 33; i++ {
		d := ground.Detection{T: float64(i)}
		// Nine sightings, jumping between the two ends of the room.
		if i%4 == 0 && i < 36 && len(dets) < 33 {
			x := 0.08
			if i%8 == 0 {
				x = 0.78
			}
			d.Boxes = []ground.Box{{Label: "colorful poster", X: x, Y: 0.1, W: 0.14, H: 0.2}}
		}
		dets = append(dets, d)
	}
	return ground.Result{OK: true, Named: "colorful poster", FPS: 1, Frames: 33,
		FoundFrac: 9.0 / 33.0, MedianJump: 0.7, Stable: stable, Detections: dets}
}

// frameSpanOn runs the ladder against a pre-seeded sweep. cfg is empty and the
// source does not exist, so the rungs below grounding (Falcon, then motion) fail
// immediately instead of shelling out — which is the point: this test is about
// what the GROUNDING rung is allowed to decide, on its own.
func frameSpanOn(t *testing.T, res ground.Result) ([]int, string, bool) {
	t.Helper()
	resetShortFraming(0, 33)
	shortGround = groundCache{done: true, winStart: 0, winEnd: 33, res: res}
	var mem framingMemory
	rects, note, located := frameSpan(config.Config{}, &ground.Runner{}, &mem,
		"no-such-file.mp4", 0, 33, 9.0/16.0, 30, 1920, 1080, nil)
	xs := make([]int, 0, len(rects))
	for _, r := range rects {
		xs = append(xs, r.X)
	}
	return xs, note, located
}

// THE REGRESSION. An unstable sighting must never steer the camera.
func TestUnstableGroundingNeverPans(t *testing.T) {
	rects, note, located := frameSpanOn(t, posterSweep(false))

	if len(rects) != 1 {
		t.Fatalf("an unstable sighting produced a %d-keyframe camera path; it must HOLD STILL.\nnote: %s",
			len(rects), note)
	}
	if !located {
		t.Errorf("the hint was thrown away entirely; it should still beat a centre crop.\nnote: %s", note)
	}
	// And it must SAY so, because Jordan reads these notes.
	if !strings.Contains(note, "hint") {
		t.Errorf("the note does not tell Jordan the framing came from a weak hint: %s", note)
	}
}

// The other half of the contract: a STEADY sighting that moves is still a pan.
// Gating on stability must not have turned the pan rung off altogether.
func TestSteadyGroundingStillPans(t *testing.T) {
	rects, note, _ := frameSpanOn(t, posterSweep(true))
	if len(rects) < 2 {
		t.Fatalf("a steady moving subject produced %d keyframe(s); the PAN rung is dead.\nnote: %s",
			len(rects), note)
	}
	if !strings.Contains(note, "PANNED") {
		t.Errorf("a pan did not report itself as one: %s", note)
	}
}

// A hold and a pan must not resolve to the same crop, or the test above proves
// nothing about where the camera actually points.
func TestHoldAndPanAreDifferentCrops(t *testing.T) {
	hold, _, _ := frameSpanOn(t, posterSweep(false))
	pan, _, _ := frameSpanOn(t, posterSweep(true))
	if len(hold) == 1 && len(pan) > 1 && hold[0] == pan[0] && hold[0] == pan[len(pan)-1] {
		t.Errorf("the pan never moved (x stayed %d), so this fixture cannot detect a regression", hold[0])
	}
}
