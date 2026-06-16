//go:build gui

// gui_draw.go — DRAW MODE: capture pointer drags as freehand strokes painted on the
// canvas. This is how Jordan visually communicates — he picks up the pen and draws on
// top of whatever the canvas shows (a waveform, a beat, nothing). No text, no menus:
// press, drag, release = a neon stroke.
//
// Strokes are stored as canvas-local point lists on the App (a.strokes + the in-progress
// a.curStroke). The canvas pointer handler in gui.go feeds press/drag/release here; the
// renderer (drawStrokes) replays them as neon polylines. Clearing is one action.
package main

import (
	"image"
	"image/color"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// stroke is one freehand mark: an ordered list of canvas-local points. Drawn as a neon
// polyline; a single-point stroke (a tap) is drawn as a small dot.
type stroke struct {
	pts []f32.Point
}

// strokeWidthDp is the on-screen thickness of a pen stroke.
const strokeWidthDp = 2.5

// beginStroke starts a new in-progress stroke at p (pointer press in draw mode).
func (a *App) beginStroke(p f32.Point) {
	a.curStroke = stroke{pts: []f32.Point{p}}
	a.drawing = true
}

// extendStroke appends p to the in-progress stroke (pointer drag in draw mode). It skips
// points too close to the last one so the stroke stays light.
func (a *App) extendStroke(p f32.Point) {
	if !a.drawing {
		return
	}
	if n := len(a.curStroke.pts); n > 0 {
		last := a.curStroke.pts[n-1]
		dx, dy := p.X-last.X, p.Y-last.Y
		if dx*dx+dy*dy < 4 { // < 2px move: ignore
			return
		}
	}
	a.curStroke.pts = append(a.curStroke.pts, p)
}

// endStroke commits the in-progress stroke (pointer release / cancel in draw mode).
func (a *App) endStroke() {
	if a.drawing && len(a.curStroke.pts) > 0 {
		a.strokes = append(a.strokes, a.curStroke)
	}
	a.curStroke = stroke{}
	a.drawing = false
}

// clearStrokes removes every drawn stroke (the draw-mode "clear" action).
func (a *App) clearStrokes() {
	a.strokes = nil
	a.curStroke = stroke{}
	a.drawing = false
}

// drawStrokes paints every committed stroke plus the in-progress one as neon polylines
// over the current canvas. Pure replay of stored points — no new state allocated.
func (a *App) drawStrokes(gtx layout.Context) {
	w := float32(gtx.Dp(unit.Dp(strokeWidthDp)))
	for i := range a.strokes {
		drawPolyline(gtx, a.strokes[i].pts, w, colNeonGreen)
	}
	if a.drawing {
		drawPolyline(gtx, a.curStroke.pts, w, colNeonGreen)
	}
}

// drawPolyline strokes a connected line through pts. A single point draws a small dot so
// a tap is still visible.
func drawPolyline(gtx layout.Context, pts []f32.Point, width float32, col color.NRGBA) {
	if len(pts) == 0 {
		return
	}
	if len(pts) == 1 {
		r := int(width)
		if r < 1 {
			r = 1
		}
		p := pts[0]
		cx, cy := int(p.X), int(p.Y)
		e := clip.Ellipse{Min: image.Pt(cx-r, cy-r), Max: image.Pt(cx+r, cy+r)}
		paint.FillShape(gtx.Ops, col, e.Op(gtx.Ops))
		return
	}
	var path clip.Path
	path.Begin(gtx.Ops)
	path.MoveTo(pts[0])
	for _, p := range pts[1:] {
		path.LineTo(p)
	}
	spec := path.End()
	paint.FillShape(gtx.Ops, col, clip.Stroke{Path: spec, Width: width}.Op())
}
