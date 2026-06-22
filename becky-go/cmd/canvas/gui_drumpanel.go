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

	"becky-go/internal/ctledit"
	"becky-go/internal/dawmodel"
)

// drumPanelState owns the per-frame geometry cache used to map pointer events back
// to (lane, step) without recomputing.
type drumPanelState struct {
	tag                           int // own pointer event tag — never shares canvasTag
	gx, gy, cellW, cellH, cellGap int // geometry from the last drawn frame
	lastTrackID, lastClipName     string

	buttons   []dpButton // clickable buttons (generative + bar nav) from the last frame
	genCount  int        // bumps the seed so repeated [Random] clicks vary
	barOffset int        // first visible bar when a beat is too wide to show at once

	// kitDir is the folder chosen via [Load Kit]; empty = use the built-in default.
	// TODO: per-pad sample assignment (drag a sample onto a pad) is a follow-up slice.
	kitDir string
}

// dpButton is one clickable button in the drum panel's top strip: a generative
// action or a bar-nav arrow. action runs the click (apply a batch, or page).
type dpButton struct {
	label  string
	rect   image.Rectangle
	accent color.NRGBA
	action func()
}

// genBatch applies a generative BeckyEditBatch through the deterministic engine.
func (d *drumPanelState) genBatch(a *App, b ctledit.BeckyEditBatch) {
	a.outExpanded = true
	a.applyBatch(b)
}

// itoaPanel renders a non-negative int without importing strconv into this file.
func itoaPanel(n int) string {
	if n <= 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func newDrumPanelState() *drumPanelState { return &drumPanelState{} }

// kitInstrument is one row of the standard drum kit shown in the panel.
type kitInstrument struct {
	name string
	note int // GM percussion note number (channel-9 key)
}

// standardKit is the fixed 16-channel kit the panel ALWAYS shows (Hydrogen-style:
// the whole kit is visible as named rows, lit where the clip has hits, empty
// otherwise — so the machine reads like a real drum machine, not "however many
// notes happen to exist"). Order is the conventional top-to-bottom kit layout.
var standardKit = []kitInstrument{
	{"Kick", 36}, {"Rim", 37}, {"Snare", 38}, {"Clap", 39},
	{"HiHat", 42}, {"OpenHat", 46}, {"LoTom", 45}, {"MidTom", 47},
	{"HiTom", 50}, {"Crash", 49}, {"Ride", 51}, {"Cowbell", 56},
	{"Tamb", 54}, {"Shaker", 70}, {"CongaHi", 62}, {"CongaLo", 63},
}

// mergeStandardKit returns a grid that always contains the 16 standard kit lanes
// (in kit order), with the clip's existing hits merged in by note number. Any
// non-standard percussion already in the clip is preserved after the kit rows.
// Display-only: the clip still stores just the real hits; toggling an empty row
// writes a note at that instrument's GM pitch via the normal SetStep/ApplyDrumGrid.
func mergeStandardKit(g *dawmodel.DrumGrid) *dawmodel.DrumGrid {
	cells := g.Steps * g.Bars
	if cells <= 0 {
		cells = dawmodel.DefaultSteps
	}
	have := map[int]dawmodel.Lane{}
	for _, ln := range g.Lanes {
		have[ln.Note] = ln
	}
	out := *g
	if out.Steps <= 0 {
		out.Steps = dawmodel.DefaultSteps
	}
	if out.Bars <= 0 {
		out.Bars = 1
	}
	std := map[int]bool{}
	out.Lanes = make([]dawmodel.Lane, 0, len(standardKit)+len(g.Lanes))
	for _, ins := range standardKit {
		std[ins.note] = true
		if ex, ok := have[ins.note]; ok {
			ex.Name = ins.name // friendlier display name
			out.Lanes = append(out.Lanes, ex)
		} else {
			out.Lanes = append(out.Lanes, dawmodel.Lane{Name: ins.name, Note: ins.note, On: make([]bool, cells), Vel: make([]int, cells)})
		}
	}
	// Preserve any non-standard lanes the clip already had (g.Lanes is note-sorted).
	for _, ln := range g.Lanes {
		if !std[ln.Note] {
			out.Lanes = append(out.Lanes, ln)
		}
	}
	return &out
}

// layout renders the drum machine panel for the active frame and handles its pointer
// events. It degrades to a placeholder when there is no arrangement or no drum clip.
func (d *drumPanelState) layout(gtx layout.Context, a *App) layout.Dimensions {
	trackID, clipName, grid := d.resolveGrid(a)
	if grid == nil {
		return panelPlaceholder(gtx, a, "drum machine — open a project.json or .mid with drum notes (channel 9)")
	}
	grid = mergeStandardKit(grid) // always show the full named 16-channel kit (Hydrogen-style)

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

	// Geometry: narrow label column on the left, step cells fill the rest.
	const labelWDp = 66
	labelW := gtx.Dp(unit.Dp(labelWDp))
	const margin = 12
	const minCell = 6
	gap := 4
	availW := size.X - labelW - 2*margin

	// Bar PAGING: a long beat is shown one window of whole bars at a time so the
	// cells stay legible (the panel used to render all 1216 steps as one row that
	// ran off-screen). The window is as many whole bars as fit at a readable cell
	// width; ◀/▶ buttons page through them.
	win := barWindow(nSteps, grid.Steps, availW, gtx.Dp(unit.Dp(20)), d.barOffset)
	d.barOffset = clampInt(d.barOffset, 0, win.MaxOffset)
	viewStart, viewSteps := win.ViewStart, win.ViewSteps

	caption := trackID + " / " + clipName
	if nLanes > 0 {
		caption += "  •  " + plural(nLanes, "lane", "lanes")
		if win.Paged {
			caption += "  •  bars " + itoaPanel(d.barOffset+1) + "-" + itoaPanel(d.barOffset+win.ViewBars) + "/" + itoaPanel(win.TotalBars)
		} else {
			caption += " × " + plural(nSteps, "step", "steps")
		}
	}
	a.drawCanvasCaption(gtx, a.th, caption)

	// Generative button strip (Playbeat-style: Random/House/Trap/4-Floor) plus
	// ◀/▶ bar-nav when the beat is paged.
	btnH := gtx.Dp(unit.Dp(26))
	d.layoutButtons(gtx, a, trackID, clipName, capH, btnH, size.X, win.Paged, win.MaxOffset)
	gridTop := capH + btnH

	// Grid occupies the area below the header + button strip.
	gridAreaH := size.Y - gridTop
	if gridAreaH <= 0 || nSteps <= 0 || nLanes <= 0 {
		return layout.Dimensions{Size: size}
	}

	availH := gridAreaH - 2*margin
	cw := (availW - (viewSteps-1)*gap) / viewSteps
	ch := (availH - (nLanes-1)*gap) / nLanes
	if cw < minCell {
		cw = minCell
	}
	if ch < minCell {
		ch = minCell
	}
	gridW := viewSteps*cw + (viewSteps-1)*gap
	gridH := nLanes*ch + (nLanes-1)*gap

	// Vertically centre the grid within the area below the header + button strip.
	ox := labelW + margin
	oy := gridTop + (gridAreaH-gridH)/2
	if oy < gridTop+margin {
		oy = gridTop + margin
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

	// Draw step cells for the visible window [viewStart, viewStart+viewSteps).
	for li, ln := range grid.Lanes {
		on := dpLaneAccent(li)
		off := dpDimColor(on)
		for col := 0; col < viewSteps; col++ {
			si := viewStart + col // absolute step in the lane
			x0 := ox + col*(cw+gap)
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
		// Buttons (generative + bar nav) take priority over the grid cells.
		if d.handleButtonClick(pe.Position) {
			continue
		}
		// hitTest works in window-local columns; add viewStart for the absolute step.
		if laneIdx, col, hit := d.hitTest(pe.Position, nLanes, viewSteps); hit {
			d.toggleStep(a, grid, trackID, clipName, laneIdx, viewStart+col)
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

// layoutButtons draws the Playbeat-style action strip (Random/House/Trap/4-Floor)
// and, when the beat is paged, ◀/▶ bar-nav buttons. Each button records its rect
// and a click closure for hit-testing. Buttons are evenly spaced across the width.
func (d *drumPanelState) layoutButtons(gtx layout.Context, a *App, trackID, clipName string, top, h, width int, paged bool, maxOffset int) {
	d.buttons = d.buttons[:0]

	// Generative actions (closures capture the resolved track/clip).
	type spec struct {
		label  string
		accent color.NRGBA
		run    func()
	}
	specs := []spec{
		{"Random", colNeonGreen, func() {
			d.genCount++
			d.genBatch(a, ctledit.BeckyEditBatch{Summary: "randomized the beat", Edits: []ctledit.BeckyEdit{
				{Op: ctledit.OpGenerateBeat, Track: trackID, Clip: clipName, Seed: int64(d.genCount)*2654435761 + 1},
			}})
		}},
		{"House", colElecBlue, func() {
			d.genCount++
			d.genBatch(a, ctledit.BeckyEditBatch{Summary: "generated a house beat", Edits: []ctledit.BeckyEdit{
				{Op: ctledit.OpGenerateBeat, Track: trackID, Clip: clipName, Genre: "house", Seed: int64(d.genCount)},
			}})
		}},
		{"Trap", colYellow, func() {
			d.genCount++
			d.genBatch(a, ctledit.BeckyEditBatch{Summary: "generated a trap beat", Edits: []ctledit.BeckyEdit{
				{Op: ctledit.OpGenerateBeat, Track: trackID, Clip: clipName, Genre: "trap", Seed: int64(d.genCount)},
			}})
		}},
		{"4-Floor", colNeonPink, func() {
			d.genBatch(a, ctledit.BeckyEditBatch{Summary: "kick: four on the floor", Edits: []ctledit.BeckyEdit{
				{Op: ctledit.OpEuclidLane, Track: trackID, Clip: clipName, Lane: "kick", Pulses: 4},
			}})
		}},
		// Load Kit: opens a folder picker (Windows) and stores the chosen path in
		// d.kitDir. execPlay bakes that kit into the --play-machine JSON (WithDefaultKitSamples).
		{"Load Kit", colTextDim, func() {
			a.startDrumKitLoad(d)
		}},
	}
	if paged {
		specs = append(specs,
			spec{"<", colTextDim, func() {
				if d.barOffset > 0 {
					d.barOffset--
					a.window.Invalidate()
				}
			}},
			spec{">", colTextDim, func() {
				if d.barOffset < maxOffset {
					d.barOffset++
					a.window.Invalidate()
				}
			}},
		)
	}

	n := len(specs)
	if n == 0 || width <= 0 || h <= 0 {
		return
	}
	margin := gtx.Dp(unit.Dp(8))
	gap := gtx.Dp(unit.Dp(6))
	avail := width - 2*margin - (n-1)*gap
	bw := avail / n
	if bw < gtx.Dp(unit.Dp(36)) {
		bw = gtx.Dp(unit.Dp(36))
	}
	y0 := top + gtx.Dp(unit.Dp(3))
	y1 := top + h - gtx.Dp(unit.Dp(3))
	for i, s := range specs {
		x0 := margin + i*(bw+gap)
		x1 := x0 + bw
		if x1 > width-margin {
			x1 = width - margin
		}
		r := image.Rect(x0, y0, x1, y1)
		fillRRect(gtx.Ops, r, 5, colHeaderBg)
		strokeRect(gtx.Ops, r, s.accent)
		drawLabelAt(gtx, a.th, s.label, x0+gtx.Dp(unit.Dp(7)), y0+gtx.Dp(unit.Dp(3)))
		d.buttons = append(d.buttons, dpButton{label: s.label, rect: r, accent: s.accent, action: s.run})
	}
}

// handleButtonClick runs a button's action when p lands on one. Returns true when
// a button was hit (so the caller skips cell hit-testing).
func (d *drumPanelState) handleButtonClick(p f32.Point) bool {
	pt := image.Pt(int(p.X), int(p.Y))
	for _, b := range d.buttons {
		if pt.In(b.rect) && b.action != nil {
			b.action()
			return true
		}
	}
	return false
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

// dpDrawLaneLabel renders the lane's instrument name (kick/snare/…) inside the
// label strip, vertically centered in the row. drawLabelAt positions text at an
// absolute (x,y) in the panel's op space, so each row gets its own label. Skipped
// only when the row is too short to read.
func dpDrawLaneLabel(gtx layout.Context, a *App, name string, x, y, cellH int) {
	if name == "" || cellH < gtx.Dp(unit.Dp(11)) {
		return
	}
	ty := y + (cellH-gtx.Dp(unit.Dp(13)))/2
	if ty < y {
		ty = y
	}
	drawLabelAt(gtx, a.th, name, x, ty)
}
