package canvas

import (
	"math"
	"testing"
)

func TestTickToPixel_table(t *testing.T) {
	cases := []struct {
		name   string
		vp     Viewport
		tick   int64
		wantPx float64
	}{
		{"origin", Viewport{PxPerTick: 0.1, ScrollTicks: 0}, 0, 0},
		{"linear", Viewport{PxPerTick: 0.1, ScrollTicks: 0}, 1000, 100},
		{"scrolled", Viewport{PxPerTick: 0.1, ScrollTicks: 500}, 1000, 50},
		{"left-of-window", Viewport{PxPerTick: 0.1, ScrollTicks: 2000}, 1000, -100},
		{"zoomed-in", Viewport{PxPerTick: 0.5, ScrollTicks: 0}, 480, 240},
	}
	for _, c := range cases {
		got := c.vp.TickToPixel(c.tick)
		if math.Abs(got-c.wantPx) > 1e-9 {
			t.Errorf("%s: TickToPixel(%d)=%v want %v", c.name, c.tick, got, c.wantPx)
		}
	}
}

func TestPixelToTick_roundTrips(t *testing.T) {
	vp := Viewport{PxPerTick: 0.1, ScrollTicks: 240}
	for _, tick := range []int64{0, 240, 480, 1000, 1920, 7680} {
		px := vp.TickToPixel(tick)
		back := vp.PixelToTick(px)
		if back != tick {
			t.Errorf("round trip tick=%d -> px=%v -> tick=%d", tick, px, back)
		}
	}
}

func TestPixelToTick_zeroZoomDegrades(t *testing.T) {
	// A zero/negative zoom must NOT divide by zero — it degrades to the scroll origin.
	vp := Viewport{PxPerTick: 0, ScrollTicks: 333}
	if got := vp.PixelToTick(9999); got != 333 {
		t.Errorf("zero-zoom PixelToTick=%d, want scroll origin 333", got)
	}
	neg := Viewport{PxPerTick: -1, ScrollTicks: 7}
	if got := neg.PixelToTick(100); got != 7 {
		t.Errorf("neg-zoom PixelToTick=%d, want scroll origin 7", got)
	}
}

func TestVisibleTicks_table(t *testing.T) {
	cases := []struct {
		name               string
		vp                 Viewport
		wantStart, wantEnd int64
	}{
		{"normal", Viewport{PxPerTick: 0.1, ScrollTicks: 0, WidthPx: 100}, 0, 1000},
		{"scrolled", Viewport{PxPerTick: 0.1, ScrollTicks: 500, WidthPx: 100}, 500, 1500},
		{"zero-zoom-collapses", Viewport{PxPerTick: 0, ScrollTicks: 50, WidthPx: 100}, 50, 50},
		{"zero-width-collapses", Viewport{PxPerTick: 0.1, ScrollTicks: 50, WidthPx: 0}, 50, 50},
	}
	for _, c := range cases {
		start, end := c.vp.VisibleTicks()
		if start != c.wantStart || end != c.wantEnd {
			t.Errorf("%s: VisibleTicks=(%d,%d) want (%d,%d)", c.name, start, end, c.wantStart, c.wantEnd)
		}
	}
}

func TestTransport_secondsMath(t *testing.T) {
	// 120 BPM at 480 PPQ => 960 ticks/sec; one quarter (480t) = 0.5s.
	tr := Transport{BPM: 120, PPQ: 480}
	if got := tr.TicksPerSecond(); math.Abs(got-960) > 1e-9 {
		t.Errorf("TicksPerSecond=%v want 960", got)
	}
	if got := tr.TickToSeconds(480); math.Abs(got-0.5) > 1e-9 {
		t.Errorf("TickToSeconds(480)=%v want 0.5", got)
	}
}

func TestTransport_unsetDegrades(t *testing.T) {
	// An unset transport must not divide by zero — seconds math returns 0.
	for _, tr := range []Transport{{}, {BPM: 0, PPQ: 480}, {BPM: 120, PPQ: 0}} {
		if got := tr.TicksPerSecond(); got != 0 {
			t.Errorf("unset TicksPerSecond=%v want 0 (%+v)", got, tr)
		}
		if got := tr.TickToSeconds(480); got != 0 {
			t.Errorf("unset TickToSeconds=%v want 0 (%+v)", got, tr)
		}
	}
}

func TestNewViewport_defaults(t *testing.T) {
	v := NewViewport()
	if v.PxPerTick <= 0 || v.WidthPx <= 0 || v.LaneHeightP <= 0 {
		t.Errorf("default viewport has non-positive metrics: %+v", v)
	}
	if v.ScrollTicks != 0 || v.LaneScroll != 0 {
		t.Errorf("default viewport should start at origin: %+v", v)
	}
}
