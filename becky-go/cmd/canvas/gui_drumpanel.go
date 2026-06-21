//go:build gui

// gui_drumpanel.go — the in-window DRUM MACHINE (CANVAS-BLUEPRINT.md panel 2a).
// Replaces the old 4×16 toy with a real grid bound to the arrangement's drum lane.
//
// CONTRACT (signatures must stay stable for other panels):
//   - type drumPanelState + func newDrumPanelState() *drumPanelState
//   - func (d *drumPanelState) layout(gtx, a *App) layout.Dimensions
//
// Reads a.arr's drum clip via DrumGridOf, renders steps×lanes, and on click calls
// SetStep → ApplyDrumGrid → applyArr (fully immutable). Registers its OWN pointer
// area with &d.tag — never touches a.canvasTag.
package main

import (
	"image"
	"image/color"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/unit"

	"becky-go/internal/dawmodel"
)

// drumPanelState owns the per-frame geometry cache used to map pointer events back
// to (lane, step) without recomputing.
type drumPanelState struct {
	tag                           int // own pointer event tag — never shares canvasTag
	gx, gy, cellW, cellH, cellGap int // geometry from the last drawn frame
	lastTrackID, lastClipName     string
}

func newDrumPanelState() *drumPanelState { return &drumPanelState{} }

// layout renders the drum machine panel for the active frame and handles its pointer
// events. It degrades to a placeholder when there is no arrangement or no drum clip.
func (d *drumPanelState) layout(gtx layout.Context, a *App) layout.Dimensions {
	trackID, clipName, grid := d.resolveGrid(a)
	if grid == nil {
		return panelPlaceholder(gtx, a, "drum machine — open a project.json or .mid with drum notes (channel 9)")
	}

	size := gtx.Constraints.Max
	if size.X <= 0 || size.Y <= 0 {
		return layout.Dimensions{Size: size}
	}

	// Header strip: caption with track + clip name.
	capH := gtx.Dp(unit.Dp(22))
	capRect := image.Rect(0, 0, size.X, capH)
	fillRect(gtx.Ops, capRect, colHeaderBg)
	nLanes := len(grid.Lanes)
	nSteps := grid.Steps * grid.Bars
	caption := trackID + " / " + clipName
	if nLanes > 0 {
		caption += "  •  " + plural(nLanes, "lane", "lanes") + " × " + plural(nSteps, "step", "steps")
	}
	a.drawCanvasCaption(gtx, a.th, caption)

	// Grid occupies the area below the header.
	gridAreaH := size.Y - capH
	if gridAreaH <= 0 || nSteps <= 0 || nLanes <= 0 {
		return layout.Dimensions{Size: size}
	}

	// Geometry: narrow label column on the left, step cells fill the rest.
	const labelWDp = 52
	labelW := gtx.Dp(unit.Dp(labelWDp))
	const margin = 12
	const minCell = 6
	gap := 4
	availW := size.X - labelW - 2*margin
	availH := gridAreaH - 2*margin
	cw := (availW - (nSteps-1)*gap) / nSteps
	ch := (availH - (nLanes-1)*gap) / nLanes
	if cw < minCell {
		cw = minCell
	}
	if ch < minCell {
		ch = minCell
	}
	gridW := nSteps*cw + (nSteps-1)*gap
	gridH := nLanes*ch + (nLanes-1)*gap

	// Vertically centre the grid within the area below the header.
	ox := labelW + margin
	oy := capH + (gridAreaH-gridH)/2
	if oy < capH+margin {
		oy = capH + margin
	}

	// Cache geometry for hit-testing in pointer events.
	d.gx = ox
	d.gy = oy
	d.cellW = cw
	d.cellH = ch
	d.cellGap = gap
	_ = gridW

	// Draw lane label strips.
	for li, ln := range grid.Lanes {
		y0 := oy + li*(ch+gap)
		// Dim label background.
		labelRect := image.Rect(margin, y0, ox-4, y0+ch)
		fillRect(gtx.Ops, labelRect, colLaneHeader)
		// Colored accent bar on the left edge so lanes read apart by color.
		accentBar := image.Rect(margin, y0, margin+4, y0+ch)
		fillRect(gtx.Ops, accentBar, dpLaneAccent(li))
		_ = ln // name used by drawLaneLabel below
		dpDrawLaneLabel(gtx, a, ln.Name, margin+6, y0, ch)
	}

	// Draw step cells.
	for li, ln := range grid.Lanes {
		on := dpLaneAccent(li)
		off := dpDimColor(on)
		for si := 0; si < nSteps; si++ {
			x0 := ox + si*(cw+gap)
			y0 := oy + li*(ch+gap)
			r := image.Rect(x0, y0, x0+cw, y0+ch)
			// Subtle downbeat accent behind every 4th step.
			if si%4 == 0 {
				band := image.Rect(x0-2, y0-2, x0+cw+2, y0+ch+2)
				fillRRect(gtx.Ops, band, 5, colHeaderBg)
			}
			if si < len(ln.On) && ln.On[si] {
				fillRRect(gtx.Ops, r, 4, on)
			} else {
				fillRRect(gtx.Ops, r, 4, off)
				strokeRect(gtx.Ops, r, on)
			}
		}
	}

	// Register OWN pointer area over the full panel (not canvasTag).
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	event.Op(gtx.Ops, &d.tag)
	area.Pop()

	// Drain Press events and toggle the clicked cell.
	for {
		ev, ok := gtx.Event(pointer.Filter{
			Target: &d.tag,
			Kinds:  pointer.Press,
		})
		if !ok {
			break
		}
		pe, ok := ev.(pointer.Event)
		if !ok || pe.Kind != pointer.Press {
			continue
		}
		if laneIdx, step, hit := d.hitTest(pe.Position, nLanes, nSteps); hit {
			d.toggleStep(a, grid, trackID, clipName, laneIdx, step)
		}
	}

	return layout.Dimensions{Size: size}
}

// resolveGrid finds the drum clip in a.arr and derives a DrumGrid.
// Priority: first clip with Channel==9 (GM percussion), then any KindMIDI clip.
// Returns nil grid when no suitable clip is found (caller shows placeholder).
func (d *drumPanelState) resolveGrid(a *App) (trackID, clipName string, grid *dawmodel.DrumGrid) {
	if a.arr == nil {
		return "", "", nil
	}
	stepTicks := func() int {
		st := a.arr.PPQ / 4
		if st <= 0 {
			return 120 // 480/4 default
		}
		return st
	}

	// First pass: Channel==9 (canonical GM percussion).
	for _, t := range a.arr.Tracks {
		if t.Kind == dawmodel.KindAudio {
			continue
		}
		for _, c := range t.Clips {
			if c.Channel != 9 {
				continue
			}
			g, err := a.arr.DrumGridOf(t.ID, c.Name, stepTicks())
			if err != nil || g == nil {
				continue
			}
			d.lastTrackID = t.ID
			d.lastClipName = c.Name
			return t.ID, c.Name, g
		}
	}
	// Second pass: any MIDI clip as fallback.
	for _, t := range a.arr.Tracks {
		if t.Kind == dawmodel.KindAudio {
			continue
		}
		for _, c := range t.Clips {
			g, err := a.arr.DrumGridOf(t.ID, c.Name, stepTicks())
			if err != nil || g == nil {
				continue
			}
			d.lastTrackID = t.ID
			d.lastClipName = c.Name
			return t.ID, c.Name, g
		}
	}
	return "", "", nil
}

// hitTest maps a pointer position to (laneIdx, step) using the cached geometry.
// Returns ok=false when p is outside the grid or in a gap between cells.
func (d *drumPanelState) hitTest(p f32.Point, nLanes, nSteps int) (lane, step int, ok bool) {
	relX := int(p.X) - d.gx
	relY := int(p.Y) - d.gy
	if relX < 0 || relY < 0 {
		return 0, 0, false
	}
	strideW := d.cellW + d.cellGap
	strideH := d.cellH + d.cellGap
	if strideW <= 0 || strideH <= 0 {
		return 0, 0, false
	}
	col := relX / strideW
	row := relY / strideH
	if col < 0 || col >= nSteps || row < 0 || row >= nLanes {
		return 0, 0, false
	}
	// Reject clicks in the gap between cells (not on a cell body).
	if relX >= col*strideW+d.cellW || relY >= row*strideH+d.cellH {
		return 0, 0, false
	}
	return row, col, true
}

// toggleStep flips one cell immutably and commits via applyArr.
// All errors degrade silently — never crash.
func (d *drumPanelState) toggleStep(a *App, grid *dawmodel.DrumGrid, trackID, clipName string, laneIdx, step int) {
	if laneIdx < 0 || laneIdx >= len(grid.Lanes) {
		return
	}
	ln := grid.Lanes[laneIdx]
	wasOn := step < len(ln.On) && ln.On[step]
	g2 := grid.SetStep(laneIdx, step, !wasOn, 110)
	next, err := a.arr.ApplyDrumGrid(trackID, clipName, g2)
	if err != nil {
		return
	}
	a.applyArr(next)
}

// dpLaneAccent cycles neon colors per lane row (0=green, 1=blue, 2=yellow, 3=pink).
// Prefixed "dp" to avoid name collision with laneOn in gui_drum.go.
func dpLaneAccent(lane int) color.NRGBA {
	switch lane % 4 {
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

// dpDimColor returns the OFF/background variant of a cell color (darkened).
// Prefixed "dp" to avoid collision with any same-named helper in gui_drum.go.
func dpDimColor(c color.NRGBA) color.NRGBA {
	return color.NRGBA{R: c.R / 8, G: c.G / 8, B: c.B / 8, A: 0xff}
}

// dpDrawLaneLabel renders a small lane name inside the label strip.
// For cells below 12dp height the text would be unreadable so we skip it.
func dpDrawLaneLabel(gtx layout.Context, a *App, name string, _ int, _ int, cellH int) {
	if cellH < gtx.Dp(unit.Dp(12)) || name == "" {
		return
	}
	// The caption helper renders at inset (10,10) from the current clip origin.
	// Since we can't cheaply offset a sub-context here without a layout.Stack,
	// we call drawCanvasCaption which renders at the top-left of the active ops.
	// The visible result: a dim lane name appears at the panel's top-left.
	// TODO(follow-up): wrap each label row in a layout.Stack + op.Offset for
	// per-row positioning. For the MVP the accent bar color identifies the lane.
	_ = a
	_ = name
}
