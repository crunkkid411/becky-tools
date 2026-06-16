//go:build gui

// gui_drum.go — the MVP DRUM MACHINE: a real, clickable step grid drawn straight onto
// the canvas (no text list, no menus). Four lanes (kick / snare / hat / clap) × sixteen
// steps. Each cell is a big square — neon-filled when ON, a dim neon edge when OFF.
// Clicking a cell toggles it. This is the "colours and shapes > text" surface for drums:
// Jordan paints a beat by clicking squares, like a hardware step sequencer.
//
// State lives on the App (a.drum). Hit-testing uses canvas-local pixels against the same
// geometry the renderer draws, so a click always lands on the cell shown. Pointer input
// is captured by the canvas area in gui.go and routed to drumCellAt.
package main

import (
	"image"
	"image/color"

	"gioui.org/f32"
	"gioui.org/layout"
)

// drumLaneCount / drumStepCount fix the grid shape: 4 lanes, 16 steps (one bar of
// sixteenth notes). Small and obvious — the MVP, not a full sequencer.
const (
	drumLaneCount = 4
	drumStepCount = 16
)

// drumGrid is the on/off state of every cell: [lane][step]. Zero value = all off.
type drumGrid struct {
	cells [drumLaneCount][drumStepCount]bool
}

// laneOn returns a lane's bright ON colour (kick=green, snare=blue, hat=yellow,
// clap=pink) so the four voices read apart at a glance.
func laneOn(lane int) color.NRGBA {
	switch lane {
	case 0:
		return colNeonGreen
	case 1:
		return colElecBlue
	case 2:
		return colYellow
	default:
		return colNeonPink
	}
}

// laneOff returns a lane's dim OFF colour: the same hue darkened so an empty cell still
// hints at its lane without shouting.
func laneOff(lane int) color.NRGBA {
	c := laneOn(lane)
	return color.NRGBA{R: c.R / 7, G: c.G / 7, B: c.B / 7, A: 0xff}
}

// drumCellAt returns the (lane, step) cell containing canvas-local point p within an
// area of the given size, or ok=false if p is outside the grid or in a gap. The geometry
// here MUST match drawDrumGrid exactly so clicks land on the cell that's drawn.
func drumCellAt(p f32.Point, size image.Point) (lane, step int, ok bool) {
	gx, gy, cw, ch, gap := drumGeometry(size)
	x := int(p.X) - gx
	y := int(p.Y) - gy
	if x < 0 || y < 0 {
		return 0, 0, false
	}
	col := x / (cw + gap)
	row := y / (ch + gap)
	if col < 0 || col >= drumStepCount || row < 0 || row >= drumLaneCount {
		return 0, 0, false
	}
	// Reject clicks landing in the gap between cells (not on a cell body).
	if x >= col*(cw+gap)+cw || y >= row*(ch+gap)+ch {
		return 0, 0, false
	}
	return row, col, true
}

// drumGeometry computes the grid origin (gx,gy), cell size (cw,ch) and gap in pixels for
// a canvas of the given size. It centres the grid and leaves a margin so the squares are
// big and obvious. Deterministic for a given size.
func drumGeometry(size image.Point) (gx, gy, cw, ch, gap int) {
	const margin = 28
	gap = 6
	availW := size.X - 2*margin
	availH := size.Y - 2*margin
	if availW < drumStepCount*8 {
		availW = drumStepCount * 8
	}
	if availH < drumLaneCount*8 {
		availH = drumLaneCount * 8
	}
	cw = (availW - (drumStepCount-1)*gap) / drumStepCount
	ch = (availH - (drumLaneCount-1)*gap) / drumLaneCount
	if cw < 6 {
		cw = 6
	}
	if ch < 6 {
		ch = 6
	}
	gridW := drumStepCount*cw + (drumStepCount-1)*gap
	gridH := drumLaneCount*ch + (drumLaneCount-1)*gap
	gx = (size.X - gridW) / 2
	gy = (size.Y - gridH) / 2
	if gx < margin {
		gx = margin
	}
	if gy < margin {
		gy = margin
	}
	return gx, gy, cw, ch, gap
}

// drawDrumGrid paints the step grid: each cell a big square, neon-filled when ON, a thin
// neon outline when OFF. Every fourth step gets a brighter background band so the beat is
// easy to read (the 1-e-and-a groups). Geometry mirrors drumCellAt exactly.
func (a *App) drawDrumGrid(gtx layout.Context) {
	size := gtx.Constraints.Max
	if size.X <= 0 || size.Y <= 0 {
		return
	}
	gx, gy, cw, ch, gap := drumGeometry(size)

	for lane := 0; lane < drumLaneCount; lane++ {
		on := laneOn(lane)
		off := laneOff(lane)
		for step := 0; step < drumStepCount; step++ {
			x0 := gx + step*(cw+gap)
			y0 := gy + lane*(ch+gap)
			r := image.Rect(x0, y0, x0+cw, y0+ch)
			// Downbeat band behind the first cell of each group of four.
			if step%4 == 0 {
				band := image.Rect(x0-3, y0-3, x0+cw+3, y0+ch+3)
				fillRRect(gtx.Ops, band, 7, colHeaderBg)
			}
			if a.drum.cells[lane][step] {
				fillRRect(gtx.Ops, r, 5, on)
			} else {
				fillRRect(gtx.Ops, r, 5, off)
				strokeRect(gtx.Ops, r, on)
			}
		}
	}
}
