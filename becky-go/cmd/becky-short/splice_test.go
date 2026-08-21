package main

import (
	"testing"

	"becky-go/internal/crop"
)

// walkAndTalk is Jordan's own 33-second clip, as the tracker actually saw it:
// he is on camera and tracked for the first 9 seconds, then steps behind the
// camera and is gone for the remaining 24. 30fps, every frame sampled.
func walkAndTalk() []crop.Rect {
	var rects []crop.Rect
	for i := 0; i < 33*30; i++ {
		t := float64(i) / 30
		seen := t < 9.0
		x := 620 // where he actually is
		if !seen {
			x = 620 // the tracker HOLDS the last good framing, so the rect looks identical
		}
		rects = append(rects, crop.Rect{T: t, X: x, Y: 0, W: 607, H: 1080, Seen: seen})
	}
	return rects
}

// THE REGRESSION. 331 good tracked frames must not be thrown away because a
// later stretch is dead. Jordan: "tracking a subject does not determine if the
// clip is good or not."
func TestSpliceKeepsTheSecondsThatWereActuallyTracked(t *testing.T) {
	rects := walkAndTalk()
	ladder := []crop.Rect{{T: 0, X: 100, Y: 0, W: 607, H: 1080}} // a static crop elsewhere

	out, tracked, filled, ok := spliceTracked(rects, ladder, 30, 2.0)
	if !ok {
		t.Fatal("a span with 9 tracked seconds and 24 dead ones refused to splice")
	}
	if len(out) != len(rects) {
		t.Fatalf("the spliced path has %d rects, want %d (one per sample)", len(out), len(rects))
	}
	if tracked < 8.5 || tracked > 9.5 {
		t.Errorf("tracked seconds = %.2f, want ~9.0", tracked)
	}
	if filled < 23.5 || filled > 24.5 {
		t.Errorf("filled seconds = %.2f, want ~24.0", filled)
	}
	// The opening — the part the tracker got RIGHT — must still be his framing.
	if got := out[30].X; got != 620 {
		t.Errorf("at 1.0s the crop is x=%d, want the tracked x=620: the opening close-up was "+
			"overwritten by the ladder's crop of a different shot", got)
	}
	// ...and the dead stretch must be the ladder's.
	if got := out[len(out)-1].X; got != 100 {
		t.Errorf("at the end the crop is x=%d, want the ladder's x=100", got)
	}
	// Times must stay on one monotonic timeline or ffmpeg's sendcmd file breaks.
	for i := 1; i < len(out); i++ {
		if out[i].T < out[i-1].T {
			t.Fatalf("the spliced path goes backwards in time at %d: %.3f after %.3f",
				i, out[i].T, out[i-1].T)
		}
	}
}

// A glance away is not a dead stretch. maxGap always meant that; it now applies
// per-stretch instead of to the whole window.
func TestSpliceTreatsAShortMissAsAGlance(t *testing.T) {
	var rects []crop.Rect
	for i := 0; i < 10*30; i++ {
		t := float64(i) / 30
		rects = append(rects, crop.Rect{T: t, X: 620, W: 607, H: 1080, Seen: !(t >= 4 && t < 5)})
	}
	if _, _, _, ok := spliceTracked(rects, []crop.Rect{{X: 100}}, 30, 2.0); ok {
		t.Error("a 1.0s blink was treated as a dead stretch worth handing to the ladder")
	}
}

// Nothing tracked at all, or everything tracked, is not a splice — the caller's
// existing answer is already right and must not be second-guessed.
func TestSpliceDeclinesWhenThereIsNothingToSplice(t *testing.T) {
	var none, all []crop.Rect
	for i := 0; i < 300; i++ {
		tt := float64(i) / 30
		none = append(none, crop.Rect{T: tt, X: 620, Seen: false})
		all = append(all, crop.Rect{T: tt, X: 620, Seen: true})
	}
	if _, _, _, ok := spliceTracked(none, []crop.Rect{{X: 100}}, 30, 2.0); ok {
		t.Error("a span with no detections at all claimed it had something to splice")
	}
	if _, _, _, ok := spliceTracked(all, []crop.Rect{{X: 100}}, 30, 2.0); ok {
		t.Error("a fully tracked span was spliced; the tracker's own path should stand")
	}
}

// rectAtTime is what keeps a PANNING ladder answer on the same timeline as the
// tracked rects it is spliced against.
func TestRectAtTimeFollowsAPan(t *testing.T) {
	pan := []crop.Rect{{T: 0, X: 0}, {T: 1, X: 50}, {T: 2, X: 100}}
	for _, c := range []struct {
		at   float64
		want int
	}{{-1, 0}, {0, 0}, {0.9, 0}, {1, 50}, {1.5, 50}, {2, 100}, {99, 100}} {
		if got := rectAtTime(pan, c.at).X; got != c.want {
			t.Errorf("rectAtTime(%.1f) = %d, want %d", c.at, got, c.want)
		}
	}
	if got := rectAtTime(nil, 1).X; got != 0 {
		t.Errorf("an empty fill returned x=%d", got)
	}
}
