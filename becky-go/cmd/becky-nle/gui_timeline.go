//go:build gui

// gui_timeline.go — THE TIMELINE: a duration-proportional clip block on a ruler with
// hours-aware timecodes, a translucent marked-range band (in -> out), and a playhead.
// Dragging anywhere on it SCRUBS: the playhead follows the pointer and a new frame is
// requested (off the UI thread, in gui.go). This is the foundation of the NLE surface.
//
// Layout (a fixed-height strip): a thin RULER row of timecode ticks on top, then the
// CLIP LANE beneath it. The whole strip is one pointer target; press/drag map the x
// position to a time and seek there.
package main

import (
	"image"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// timelineHeight is the fixed height of the whole timeline strip (ruler + lane).
const timelineHeight = 96

// rulerHeight is the height of the timecode tick row at the top of the strip.
const rulerHeight = 22

// layoutTimeline draws the timeline strip and captures scrub pointer input over it.
func (a *App) layoutTimeline(gtx layout.Context) layout.Dimensions {
	h := gtx.Dp(unit.Dp(timelineHeight))
	gtx.Constraints.Min.Y = h
	gtx.Constraints.Max.Y = h
	size := image.Pt(gtx.Constraints.Max.X, h)

	return borderBox(gtx, colGridLine, func(gtx layout.Context) layout.Dimensions {
		// Background.
		fillRect(gtx.Ops, image.Rect(0, 0, size.X, size.Y), colPanelBg)

		dur := a.project.Duration()
		if !a.project.IsOpen() || dur <= 0 {
			a.drawTimelineEmpty(gtx, size)
			a.captureScrub(gtx, size) // still register the area (no-op until open)
			return layout.Dimensions{Size: size}
		}

		rh := gtx.Dp(unit.Dp(rulerHeight))
		a.drawRuler(gtx, size, rh, dur)
		a.drawClipLane(gtx, size, rh, dur)
		a.captureScrub(gtx, size)
		return layout.Dimensions{Size: size}
	})
}

// drawRuler draws evenly-spaced timecode ticks across the top of the strip. The tick
// interval is chosen so labels don't crowd (a "nice" step in seconds). Hours-aware.
func (a *App) drawRuler(gtx layout.Context, size image.Point, rulerH int, dur float64) {
	fillRect(gtx.Ops, image.Rect(0, 0, size.X, rulerH), colHeaderBg)
	if size.X <= 0 {
		return
	}
	step := niceTickStep(dur, size.X)
	if step <= 0 {
		return
	}
	for t := 0.0; t <= dur+1e-6; t += step {
		x := int(t / dur * float64(size.X))
		if x >= size.X {
			x = size.X - 1
		}
		// Tick line down the whole strip (faint), brighter in the ruler.
		fillRect(gtx.Ops, image.Rect(x, 0, x+1, size.Y), colGridLine)
		fillRect(gtx.Ops, image.Rect(x, 0, x+1, rulerH), colTextDim)
		// Label just right of the tick.
		a.drawTickLabel(gtx, x+3, formatTCShort(t))
	}
}

// drawTickLabel renders a small dim timecode label at (x, ~3px) in the ruler.
func (a *App) drawTickLabel(gtx layout.Context, x int, txt string) {
	defer op.Offset(image.Pt(x, 3)).Push(gtx.Ops).Pop()
	lbl := material.Caption(a.th, txt)
	lbl.Color = colTextDim
	lbl.TextSize = unit.Sp(10)
	lbl.Layout(gtx)
}

// drawClipLane draws the duration-proportional clip block (the whole source spans the
// lane), the translucent marked-range band, and the playhead line.
func (a *App) drawClipLane(gtx layout.Context, size image.Point, rulerH int, dur float64) {
	laneTop := rulerH + 4
	laneBot := size.Y - 4
	if laneBot <= laneTop {
		return
	}
	// The clip block spans the full width (one source on the timeline).
	fillRRect(gtx.Ops, image.Rect(2, laneTop, size.X-2, laneBot), 4, colClipBlock)
	strokeRect(gtx.Ops, image.Rect(2, laneTop, size.X-2, laneBot), colGridLine)

	// Marked range band (in -> out), translucent green.
	inX := timeToX(a.project.In, dur, size.X)
	outX := timeToX(a.project.Out, dur, size.X)
	if outX > inX {
		fillRect(gtx.Ops, image.Rect(inX, laneTop, outX, laneBot), colInOut)
	}
	// In / out mark lines.
	fillRect(gtx.Ops, image.Rect(inX, laneTop, inX+2, laneBot), colMarkLine)
	fillRect(gtx.Ops, image.Rect(outX-2, laneTop, outX, laneBot), colMarkLine)

	// Playhead (yellow), full strip height so it reads against the ruler too.
	px := timeToX(a.project.Play, dur, size.X)
	fillRect(gtx.Ops, image.Rect(px-1, 0, px+1, size.Y), colPlayhead)
}

// drawTimelineEmpty draws the empty-state lane hint.
func (a *App) drawTimelineEmpty(gtx layout.Context, size image.Point) {
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		lbl := material.Caption(a.th, "timeline — open a video, then drag here to scrub")
		lbl.Color = colTextDim
		return lbl.Layout(gtx)
	})
}

// captureScrub registers the timeline strip as a pointer target and maps press/drag x to
// a seek time. Scrubbing requests the frame at the new playhead OFF the UI thread, so a
// fast drag never blocks the window (GUI-RULES.md §2.4). No-op until a video is open.
func (a *App) captureScrub(gtx layout.Context, size image.Point) {
	if size.X <= 0 || size.Y <= 0 {
		return
	}
	tag := &a.timelineTag
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	event.Op(gtx.Ops, tag)
	area.Pop()

	for {
		ev, ok := gtx.Event(pointer.Filter{
			Target: tag,
			Kinds:  pointer.Press | pointer.Drag,
		})
		if !ok {
			break
		}
		pe, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		if !a.project.IsOpen() {
			continue
		}
		if pe.Kind == pointer.Press || pe.Kind == pointer.Drag {
			t := xToTime(int(pe.Position.X), a.project.Duration(), size.X)
			a.project.SetPlayhead(t)
			a.requestFrame(a.project.Play)
			a.window.Invalidate()
		}
	}
}

// --- mapping helpers (pure) ------------------------------------------------------

// timeToX maps a time in seconds to a pixel x within [0,width].
func timeToX(t, dur float64, width int) int {
	if dur <= 0 || width <= 0 {
		return 0
	}
	x := int(t / dur * float64(width))
	if x < 0 {
		x = 0
	}
	if x > width {
		x = width
	}
	return x
}

// xToTime maps a pixel x within [0,width] back to a time in seconds, clamped to [0,dur].
func xToTime(x int, dur float64, width int) float64 {
	if width <= 0 || dur <= 0 {
		return 0
	}
	if x < 0 {
		x = 0
	}
	if x > width {
		x = width
	}
	t := float64(x) / float64(width) * dur
	if t < 0 {
		t = 0
	}
	if t > dur {
		t = dur
	}
	return t
}

// niceTickStep picks a human-friendly ruler tick interval (seconds) so that labels are
// ~90px apart and land on round values (1,2,5,10,15,30,60,...). Hours-aware for long clips.
func niceTickStep(dur float64, width int) float64 {
	if dur <= 0 || width <= 0 {
		return 0
	}
	const targetPx = 90.0
	approx := dur / (float64(width) / targetPx) // seconds per ~targetPx
	steps := []float64{1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200}
	for _, s := range steps {
		if s >= approx {
			return s
		}
	}
	return steps[len(steps)-1]
}
