package canvas

import "math"

// Viewport + transport: the pure, deterministic timeline math the GUI uses to draw
// the surface. Time is measured in MIDI ticks (PPQ-relative, the same unit
// becky-compose emits) so the model is sample-rate-independent and reproducible.
//
// No rendering happens here — these are the conversions a DrawList caller needs:
// tick<->pixel given a zoom, plus the visible-window helpers. Keeping the math in
// pure Go means the time-axis logic is unit-testable without a window.

// Transport carries the clock the whole canvas shares: tempo, resolution, and the
// playhead. PPQ + BPM let the GUI convert ticks to seconds for the time ruler; the
// canvas itself stays in ticks so it matches the source MIDI exactly.
type Transport struct {
	BPM      int   `json:"bpm"`      // beats per minute (tempo)
	PPQ      int   `json:"ppq"`      // ticks per quarter note (resolution)
	Playhead int64 `json:"playhead"` // current position, in ticks
	Loop     Loop  `json:"loop"`     // loop region, in ticks (zero = no loop)
}

// Loop is an optional play region in ticks. A zero Loop (End<=Start) means "off".
type Loop struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

// Enabled reports whether the loop region is a real, non-empty span.
func (l Loop) Enabled() bool { return l.End > l.Start }

// Viewport is the visible window onto the timeline + the vertical lane scroll. It is
// the deterministic camera the GUI reads each frame; the native step only multiplies
// these by device pixels. PxPerTick is the horizontal zoom (pixels per MIDI tick);
// ScrollTicks is the left edge of the visible window (horizontal scroll); LaneScroll
// is the index of the first visible track lane (vertical scroll).
type Viewport struct {
	PxPerTick   float64 `json:"pxPerTick"`    // horizontal zoom (pixels per tick)
	ScrollTicks int64   `json:"scrollTicks"`  // left edge of the window, in ticks
	WidthPx     int     `json:"widthPx"`      // visible canvas width, in pixels
	LaneScroll  int     `json:"laneScroll"`   // index of the first visible track lane
	LaneHeightP int     `json:"laneHeightPx"` // per-lane height, in pixels
}

// defaultViewport zoom/lane sizing — sane starting values the GUI can override.
const (
	defaultPxPerTick = 0.05 // ~96px per quarter note at 480 PPQ
	defaultWidthPx   = 1280
	defaultLanePx    = 72
)

// NewViewport returns a viewport with sane defaults (scrolled to the start). The GUI
// replaces WidthPx/LaneHeightP with real device metrics; the model stays valid
// without a window.
func NewViewport() Viewport {
	return Viewport{
		PxPerTick:   defaultPxPerTick,
		ScrollTicks: 0,
		WidthPx:     defaultWidthPx,
		LaneScroll:  0,
		LaneHeightP: defaultLanePx,
	}
}

// TickToPixel maps an absolute tick position to an x pixel within the viewport,
// accounting for the horizontal scroll and zoom. The result can be negative (left of
// the window) or beyond WidthPx (right of the window); the caller clips. Pure: same
// inputs always yield the same pixel.
func (v Viewport) TickToPixel(tick int64) float64 {
	return float64(tick-v.ScrollTicks) * v.PxPerTick
}

// PixelToTick is the inverse of TickToPixel: it maps an x pixel back to an absolute
// tick. With a zero/negative zoom it degrades to the scroll origin rather than
// dividing by zero (degrade, never crash). math.Round (not a +0.5 truncation) is
// used so negative offsets round symmetrically and round-trip exactly.
func (v Viewport) PixelToTick(px float64) int64 {
	if v.PxPerTick <= 0 {
		return v.ScrollTicks
	}
	return v.ScrollTicks + int64(math.Round(px/v.PxPerTick))
}

// VisibleTicks returns the tick span [start, end) currently visible in the window,
// derived from the scroll origin, width, and zoom. With a zero/negative zoom the
// window collapses to a single tick at the scroll origin.
func (v Viewport) VisibleTicks() (start, end int64) {
	start = v.ScrollTicks
	if v.PxPerTick <= 0 || v.WidthPx <= 0 {
		return start, start
	}
	span := int64(float64(v.WidthPx)/v.PxPerTick + 0.5)
	return start, start + span
}

// TicksPerSecond converts the transport's tempo+resolution into ticks/second, the
// bridge the GUI uses to label the time ruler in seconds. Returns 0 when the
// transport is unset (BPM or PPQ <= 0) so callers don't divide by it blindly.
func (t Transport) TicksPerSecond() float64 {
	if t.BPM <= 0 || t.PPQ <= 0 {
		return 0
	}
	return float64(t.BPM) * float64(t.PPQ) / 60.0
}

// TickToSeconds maps a tick to seconds on this transport (0 when tempo is unset).
func (t Transport) TickToSeconds(tick int64) float64 {
	tps := t.TicksPerSecond()
	if tps == 0 {
		return 0
	}
	return float64(tick) / tps
}
