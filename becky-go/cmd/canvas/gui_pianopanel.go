//go:build gui

// gui_pianopanel.go -- the in-window PIANO ROLL (CANVAS-BLUEPRINT.md panel 2b).
//
// CONTRACT (keep these signatures stable):
//   - type pianoPanel + func newPianoPanel() *pianoPanel
//   - func (p *pianoPanel) layout(gtx layout.Context, a *App) layout.Dimensions
//
// All edits go through a.applyArr (immutable dawmodel verbs). Never mutate a.arr directly.
// Uses its own ptrTag/keyTag -- never App's canvasTag -- to avoid pointer-event conflicts.
package main

import (
	"image"
	"image/color"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"becky-go/internal/dawmodel"
)

// -- layout constants ----------------------------------------------------------

const (
	prGutterW          = 48  // left piano-key gutter width (px)
	prRowHDp           = 12  // note-row height in dp
	prDefaultPxPerTick = 0.1 // initial horizontal zoom
	prEdgeHitPx        = 8   // px either side of note right edge = resize zone
	prVelLaneH         = 32  // velocity lane height at the bottom
	prMinPitch         = 0   // MIDI 0
	prMaxPitch         = 127 // MIDI 127
)

// piano key layout: true = black key for semitone index 0-11
var prBlackKey = [12]bool{
	false, true, false, true, false,
	false, true, false, true, false, true, false,
}

// -- colours -------------------------------------------------------------------

var (
	prNoteBody    = color.NRGBA{R: 0x1e, G: 0x6a, B: 0x10, A: 0xff}
	prNoteBodySel = color.NRGBA{R: 0x39, G: 0xff, B: 0x14, A: 0xff}
	prVelBarCol   = color.NRGBA{R: 0x80, G: 0x00, B: 0xff, A: 0xcc}
	prWhiteRow    = color.NRGBA{R: 0x1c, G: 0x1c, B: 0x1c, A: 0xff}
	prBlackRow    = color.NRGBA{R: 0x0c, G: 0x0c, B: 0x0c, A: 0xff}
	prKeyWhite    = color.NRGBA{R: 0xe8, G: 0xe8, B: 0xe8, A: 0xff}
	prKeyBlack    = color.NRGBA{R: 0x22, G: 0x22, B: 0x22, A: 0xff}
	prBarLine     = color.NRGBA{R: 0x3a, G: 0x3a, B: 0x3a, A: 0xff}
	prBeatLine    = color.NRGBA{R: 0x28, G: 0x28, B: 0x28, A: 0xff}
	prSnapLine    = color.NRGBA{R: 0x20, G: 0x20, B: 0x20, A: 0xff}
)

// -- drag state ----------------------------------------------------------------

type prDragKind int

const (
	prDragNone prDragKind = iota
	prDragBody            // move selected notes
	prDragEdge            // resize note right edge
)

// -- panel ---------------------------------------------------------------------

type pianoPanel struct {
	ptrTag bool
	keyTag bool

	pxPerTick  float64
	scrollTick int

	pitchMin int
	pitchMax int
	pitchSet bool

	selectedIDs map[uint64]bool

	dragKind    prDragKind
	dragStart   f32.Point
	dragLast    f32.Point
	dragEdgeID  uint64
	dragBaseDur int

	pressCount  int
	lastPressPt f32.Point
}

func newPianoPanel() *pianoPanel {
	return &pianoPanel{
		pxPerTick:   prDefaultPxPerTick,
		pitchMin:    36,
		pitchMax:    84,
		selectedIDs: make(map[uint64]bool),
	}
}

// layout renders the piano roll and handles input for the current frame.
func (p *pianoPanel) layout(gtx layout.Context, a *App) layout.Dimensions {
	trackID, clipName, ok := a.firstMidiClip()
	if !ok {
		return panelPlaceholder(gtx, a, "piano roll -- open a project.json or .mid")
	}
	c := p.lookupClip(a.arr, trackID, clipName)
	if c == nil {
		return panelPlaceholder(gtx, a, "piano roll -- clip not found")
	}

	sz := gtx.Constraints.Max
	if sz.X <= 0 || sz.Y <= 0 {
		return layout.Dimensions{Size: sz}
	}

	ppq := 480
	if a.arr != nil && a.arr.PPQ > 0 {
		ppq = a.arr.PPQ
	}

	rowH := gtx.Dp(prRowHDp)
	if rowH < 4 {
		rowH = 4
	}

	if !p.pitchSet {
		p.autoFitPitch(c)
		p.pitchSet = true
	}

	roll := p.rollRect(sz)

	p.drawRows(gtx, sz, rowH, roll)
	p.drawGridLines(gtx, ppq, roll)
	p.drawGutter(gtx, sz, rowH, roll)
	p.drawNotes(gtx, c, rowH, roll)
	p.drawVelLane(gtx, c, sz, roll)

	p.handlePointer(gtx, a, trackID, clipName, c, roll, rowH, ppq)
	p.handleKeyboard(gtx, a, trackID, clipName)

	return layout.Dimensions{Size: sz}
}

// -- geometry helpers ----------------------------------------------------------

func (p *pianoPanel) rollRect(sz image.Point) image.Rectangle {
	return image.Rect(prGutterW, 0, sz.X, sz.Y-prVelLaneH)
}

func (p *pianoPanel) tickToX(tick int, roll image.Rectangle) float32 {
	return float32(roll.Min.X) + float32(tick-p.scrollTick)*float32(p.pxPerTick)
}

func (p *pianoPanel) xToTick(x float32, roll image.Rectangle, ppq int) int {
	rawTick := p.scrollTick + int((float64(x)-float64(roll.Min.X))/p.pxPerTick)
	step := ppq / 4
	if step < 1 {
		step = 1
	}
	snapped := (rawTick / step) * step
	if snapped < 0 {
		snapped = 0
	}
	return snapped
}

func (p *pianoPanel) pitchToY(pitch, rowH int, roll image.Rectangle) int {
	return roll.Min.Y + (p.pitchMax-pitch)*rowH
}

func (p *pianoPanel) yToPitch(y float32, rowH int, roll image.Rectangle) int {
	row := (float64(y) - float64(roll.Min.Y)) / float64(rowH)
	pitch := p.pitchMax - int(row)
	if pitch < prMinPitch {
		pitch = prMinPitch
	}
	if pitch > prMaxPitch {
		pitch = prMaxPitch
	}
	return pitch
}

// -- lookup helpers ------------------------------------------------------------

func (p *pianoPanel) lookupClip(arr *dawmodel.Arrangement, trackID, clipName string) *dawmodel.Clip {
	if arr == nil {
		return nil
	}
	for i := range arr.Tracks {
		if arr.Tracks[i].ID != trackID {
			continue
		}
		for j := range arr.Tracks[i].Clips {
			if arr.Tracks[i].Clips[j].Name == clipName {
				return &arr.Tracks[i].Clips[j]
			}
		}
	}
	return nil
}

func (p *pianoPanel) autoFitPitch(c *dawmodel.Clip) {
	if len(c.Notes) == 0 {
		return
	}
	lo, hi := c.Notes[0].Pitch, c.Notes[0].Pitch
	for _, n := range c.Notes {
		if n.Pitch < lo {
			lo = n.Pitch
		}
		if n.Pitch > hi {
			hi = n.Pitch
		}
	}
	pad := 4
	p.pitchMin = lo - pad
	p.pitchMax = hi + pad
	if p.pitchMin < prMinPitch {
		p.pitchMin = prMinPitch
	}
	if p.pitchMax > prMaxPitch {
		p.pitchMax = prMaxPitch
	}
}

// -- drawing -------------------------------------------------------------------

func prFill(gtx layout.Context, r image.Rectangle, col color.NRGBA) {
	defer clip.Rect(r).Push(gtx.Ops).Pop()
	paint.ColorOp{Color: col}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

func (p *pianoPanel) drawRows(gtx layout.Context, sz image.Point, rowH int, roll image.Rectangle) {
	for pitch := p.pitchMin; pitch <= p.pitchMax; pitch++ {
		y := p.pitchToY(pitch, rowH, roll)
		r := image.Rect(0, y, sz.X, y+rowH)
		sem := pitch % 12
		if sem < 0 {
			sem += 12
		}
		col := prWhiteRow
		if prBlackKey[sem] {
			col = prBlackRow
		}
		prFill(gtx, r, col)
	}
}

func (p *pianoPanel) drawGridLines(gtx layout.Context, ppq int, roll image.Rectangle) {
	ticksPerBar := ppq * 4
	ticksPerBeat := ppq
	ticksPerSixteenth := ppq / 4
	if ticksPerSixteenth < 1 {
		ticksPerSixteenth = 1
	}
	width := roll.Max.X - roll.Min.X
	endTick := p.scrollTick + int(float64(width)/p.pxPerTick) + ticksPerBar
	for t := (p.scrollTick / ticksPerSixteenth) * ticksPerSixteenth; t < endTick; t += ticksPerSixteenth {
		x := int(p.tickToX(t, roll))
		if x < roll.Min.X || x > roll.Max.X {
			continue
		}
		col := prSnapLine
		if t%ticksPerBar == 0 {
			col = prBarLine
		} else if t%ticksPerBeat == 0 {
			col = prBeatLine
		}
		prFill(gtx, image.Rect(x, roll.Min.Y, x+1, roll.Max.Y), col)
	}
}

func (p *pianoPanel) drawGutter(gtx layout.Context, sz image.Point, rowH int, roll image.Rectangle) {
	_ = sz
	for pitch := p.pitchMin; pitch <= p.pitchMax; pitch++ {
		y := p.pitchToY(pitch, rowH, roll)
		sem := pitch % 12
		if sem < 0 {
			sem += 12
		}
		col := prKeyWhite
		if prBlackKey[sem] {
			col = prKeyBlack
		}
		prFill(gtx, image.Rect(0, y, prGutterW-1, y+rowH), col)
	}
}

func (p *pianoPanel) drawNotes(gtx layout.Context, c *dawmodel.Clip, rowH int, roll image.Rectangle) {
	for _, n := range c.Notes {
		if n.Pitch < p.pitchMin || n.Pitch > p.pitchMax {
			continue
		}
		x0 := int(p.tickToX(n.Start, roll))
		x1 := int(p.tickToX(n.Start+n.Dur, roll))
		if x1 <= roll.Min.X || x0 >= roll.Max.X {
			continue
		}
		if x0 < roll.Min.X {
			x0 = roll.Min.X
		}
		if x1 > roll.Max.X {
			x1 = roll.Max.X
		}
		if x1 <= x0 {
			x1 = x0 + 2
		}
		y := p.pitchToY(n.Pitch, rowH, roll)
		col := prNoteBody
		if p.selectedIDs[n.ID] {
			col = prNoteBodySel
		}
		prFill(gtx, image.Rect(x0, y+1, x1-1, y+rowH-1), col)
	}
}

func (p *pianoPanel) drawVelLane(gtx layout.Context, c *dawmodel.Clip, sz image.Point, roll image.Rectangle) {
	laneY := sz.Y - prVelLaneH
	prFill(gtx, image.Rect(prGutterW, laneY, sz.X, sz.Y), color.NRGBA{R: 0x10, G: 0x10, B: 0x10, A: 0xff})
	for _, n := range c.Notes {
		x := int(p.tickToX(n.Start, roll))
		if x < prGutterW || x > sz.X {
			continue
		}
		h := int(float32(prVelLaneH) * float32(n.Vel) / 127.0)
		if h < 1 {
			h = 1
		}
		prFill(gtx, image.Rect(x, laneY+prVelLaneH-h, x+3, laneY+prVelLaneH), prVelBarCol)
	}
}

// -- pointer input -------------------------------------------------------------

func (p *pianoPanel) handlePointer(
	gtx layout.Context, a *App,
	trackID, clipName string, c *dawmodel.Clip,
	roll image.Rectangle, rowH, ppq int,
) {
	area := clip.Rect(roll).Push(gtx.Ops)
	event.Op(gtx.Ops, &p.ptrTag)
	area.Pop()

	for {
		ev, ok := gtx.Event(pointer.Filter{
			Target: &p.ptrTag,
			Kinds:  pointer.Press | pointer.Release | pointer.Drag | pointer.Scroll,
		})
		if !ok {
			break
		}
		pe, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch pe.Kind {
		case pointer.Scroll:
			delta := int(pe.Scroll.X / float32(p.pxPerTick))
			p.scrollTick += delta
			if p.scrollTick < 0 {
				p.scrollTick = 0
			}
		case pointer.Press:
			p.handlePress(pe, a, trackID, clipName, c, roll, rowH, ppq)
		case pointer.Drag:
			p.handleDrag(pe)
		case pointer.Release:
			p.handleRelease(pe, a, trackID, clipName, ppq)
		}
	}
}

func (p *pianoPanel) handlePress(
	pe pointer.Event, a *App,
	trackID, clipName string, c *dawmodel.Clip,
	roll image.Rectangle, rowH, ppq int,
) {
	pt := pe.Position

	isDbl := false
	if p.pressCount > 0 {
		dx := pt.X - p.lastPressPt.X
		dy := pt.Y - p.lastPressPt.Y
		dist := dx*dx + dy*dy
		thresh := float32(rowH * 3 * rowH * 3)
		if dist < thresh {
			isDbl = true
		}
	}
	p.pressCount++
	p.lastPressPt = pt

	if isDbl {
		p.pressCount = 0
		tick := p.xToTick(pt.X, roll, ppq)
		pitch := p.yToPitch(pt.Y, rowH, roll)
		dur := ppq / 4
		if dur < 1 {
			dur = 1
		}
		n := dawmodel.Note{Start: tick, Dur: dur, Pitch: pitch, Vel: 100}
		if next, newID, err := a.arr.AddNote(trackID, clipName, n); err == nil {
			a.applyArr(next)
			p.selectedIDs = map[uint64]bool{newID: true}
		}
		p.dragKind = prDragNone
		return
	}

	if hit, edgeID, baseDur := p.hitTestEdge(c, pt, roll, rowH); hit {
		p.dragKind = prDragEdge
		p.dragEdgeID = edgeID
		p.dragBaseDur = baseDur
		p.dragStart = pt
		p.dragLast = pt
		if !pe.Modifiers.Contain(key.ModShift) {
			p.selectedIDs = map[uint64]bool{edgeID: true}
		}
		return
	}

	if noteID, onNote := p.hitTestBody(c, pt, roll, rowH); onNote {
		if !pe.Modifiers.Contain(key.ModShift) {
			if !p.selectedIDs[noteID] {
				p.selectedIDs = map[uint64]bool{noteID: true}
			}
		} else {
			if p.selectedIDs[noteID] {
				delete(p.selectedIDs, noteID)
			} else {
				p.selectedIDs[noteID] = true
			}
		}
		p.dragKind = prDragBody
		p.dragStart = pt
		p.dragLast = pt
		return
	}

	if !pe.Modifiers.Contain(key.ModShift) {
		p.selectedIDs = make(map[uint64]bool)
	}
	p.dragKind = prDragNone
}

func (p *pianoPanel) handleDrag(pe pointer.Event) {
	if p.dragKind != prDragNone {
		p.dragLast = pe.Position
	}
}

func (p *pianoPanel) handleRelease(
	pe pointer.Event, a *App,
	trackID, clipName string,
	ppq int,
) {
	if p.dragKind == prDragNone {
		return
	}

	dx := pe.Position.X - p.dragStart.X
	dy := pe.Position.Y - p.dragStart.Y

	step := ppq / 4
	if step < 1 {
		step = 1
	}
	pxPerStep := float32(p.pxPerTick) * float32(step)

	switch p.dragKind {
	case prDragBody:
		dTicks := 0
		if pxPerStep > 0 {
			dTicks = int(dx/pxPerStep) * step
		}
		// rowH not available; approximate 12px per row (matches prRowHDp default)
		dPitch := -int(dy / 12.0)
		if dTicks != 0 || dPitch != 0 {
			if next, err := a.arr.MoveNotes(trackID, clipName, p.selectedIDSlice(), dTicks, dPitch); err == nil {
				a.applyArr(next)
			}
		}

	case prDragEdge:
		dDur := 0
		if pxPerStep > 0 {
			dDur = int(dx/pxPerStep) * step
		}
		if dDur != 0 {
			if next, err := a.arr.ResizeNotes(trackID, clipName, []uint64{p.dragEdgeID}, dDur); err == nil {
				a.applyArr(next)
			}
		}
	}

	p.dragKind = prDragNone
}

func (p *pianoPanel) hitTestBody(c *dawmodel.Clip, pt f32.Point, roll image.Rectangle, rowH int) (uint64, bool) {
	for i := len(c.Notes) - 1; i >= 0; i-- {
		n := c.Notes[i]
		if n.Pitch < p.pitchMin || n.Pitch > p.pitchMax {
			continue
		}
		x0 := p.tickToX(n.Start, roll)
		x1 := p.tickToX(n.Start+n.Dur, roll)
		y := float32(p.pitchToY(n.Pitch, rowH, roll))
		if pt.X >= x0 && pt.X < x1-prEdgeHitPx &&
			pt.Y >= y && pt.Y < y+float32(rowH) {
			return n.ID, true
		}
	}
	return 0, false
}

func (p *pianoPanel) hitTestEdge(c *dawmodel.Clip, pt f32.Point, roll image.Rectangle, rowH int) (bool, uint64, int) {
	for i := len(c.Notes) - 1; i >= 0; i-- {
		n := c.Notes[i]
		if n.Pitch < p.pitchMin || n.Pitch > p.pitchMax {
			continue
		}
		x1 := p.tickToX(n.Start+n.Dur, roll)
		y := float32(p.pitchToY(n.Pitch, rowH, roll))
		if pt.X >= x1-prEdgeHitPx && pt.X <= x1+prEdgeHitPx &&
			pt.Y >= y && pt.Y < y+float32(rowH) {
			return true, n.ID, n.Dur
		}
	}
	return false, 0, 0
}

func (p *pianoPanel) selectedIDSlice() []uint64 {
	ids := make([]uint64, 0, len(p.selectedIDs))
	for id := range p.selectedIDs {
		ids = append(ids, id)
	}
	return ids
}

// -- keyboard input ------------------------------------------------------------

func (p *pianoPanel) handleKeyboard(gtx layout.Context, a *App, trackID, clipName string) {
	area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	event.Op(gtx.Ops, &p.keyTag)
	area.Pop()

	for {
		ev, ok := gtx.Event(key.Filter{Focus: &p.keyTag, Name: key.NameDeleteBackward})
		if !ok {
			break
		}
		if ke, ok2 := ev.(key.Event); ok2 && ke.State == key.Press {
			p.doDelete(a, trackID, clipName)
		}
	}
	for {
		ev, ok := gtx.Event(key.Filter{Focus: &p.keyTag, Name: key.NameDeleteForward})
		if !ok {
			break
		}
		if ke, ok2 := ev.(key.Event); ok2 && ke.State == key.Press {
			p.doDelete(a, trackID, clipName)
		}
	}
}

func (p *pianoPanel) doDelete(a *App, trackID, clipName string) {
	if len(p.selectedIDs) == 0 {
		return
	}
	if next, err := a.arr.DeleteNotes(trackID, clipName, p.selectedIDSlice()); err == nil {
		a.applyArr(next)
		p.selectedIDs = make(map[uint64]bool)
	}
}

// dragBaseDur is stored for future use (e.g. min-dur clamping during live drag preview).
var _ = (*pianoPanel)(nil).dragBaseDur

// ensure op import is used
var _ *op.Ops
