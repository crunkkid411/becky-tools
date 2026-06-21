//go:build gui

// gui_dock.go — the DOCK: a vertical strip of BIG, OBVIOUS icon buttons down the left
// edge. This is the icon-first replacement for the old wall-of-text tool list. Each
// button is a square, neon-on-black; hovering lights it with a subtle glow and a small
// tooltip (the ONLY text on the dock, and only on hover). Each button switches the
// canvas MODE or fires an ACTION:
//
//	RECORD (mic)    — record audio via becky-daw-engine
//	DRAW   (brush)  — toggle pen/draw mode
//	PIANO  (note)   — switch to piano-roll mode
//	DRUM   (grid)   — switch to drum-machine mode (clickable step grid)
//	VIDEO  (movie)  — switch to video mode
//	OPEN   (folder) — load a file/folder as the target
//
// No text labels on the buttons; meaning is the icon + the mode accent colour.
package main

import (
	"image"
	"image/color"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"becky-go/internal/canvas"
)

// dockWidth is the fixed width of the icon dock column.
const dockWidth = 76

// dockButtonSize is the side length of a dock icon button (big and obvious).
const dockButtonSize = 52

// dockItem is one dock button: its icon, tooltip, signature accent colour, the click
// state, and whether it is the active mode/state (drawn filled).
type dockItem struct {
	icon    *widget.Icon
	tip     string
	accent  color.NRGBA
	clicker *widget.Clickable
	active  bool
}

// dockItems builds the dock button list in fixed order. Mode buttons mark themselves
// active when their mode is current; DRAW marks active while pen mode is on.
func (a *App) dockItems() []dockItem {
	return []dockItem{
		{icon: a.icons.record, tip: "Record audio", accent: colCrimson, clicker: &a.dockRecord},
		{icon: a.icons.draw, tip: "Draw on the canvas", accent: colNeonGreen, clicker: &a.dockDraw, active: a.drawMode},
		{icon: a.icons.piano, tip: "Piano roll", accent: colDeepPurple, clicker: &a.dockPiano, active: a.activeMode == canvas.ModeMIDI},
		{icon: a.icons.drum, tip: "Drum machine", accent: colYellow, clicker: &a.dockDrum, active: a.activeMode == canvas.ModeDrum},
		{icon: a.icons.video, tip: "Video", accent: colNeonPink, clicker: &a.dockVideo, active: a.activeMode == canvas.ModeVideo},
		{icon: a.icons.folder, tip: "Open a file or folder", accent: colElecBlue, clicker: &a.dockOpen},
	}
}

// layoutDock draws the icon dock: the brand diamond at the top, then the stack of big
// icon buttons. Fixed width so the canvas owns the rest of the window.
func (a *App) layoutDock(gtx layout.Context) layout.Dimensions {
	gtx.Constraints.Min.X = gtx.Dp(unit.Dp(dockWidth))
	gtx.Constraints.Max.X = gtx.Constraints.Min.X
	return widgetBg(gtx, colPanelBg, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			modes := a.dockItems()
			hub := a.hubItems()
			children := make([]layout.FlexChild, 0, (len(modes)+len(hub))*2+4)
			children = append(children, layout.Rigid(a.layoutBrandDiamond)) // scene-kid mark
			children = append(children, layout.Rigid(layout.Spacer{Height: unit.Dp(14)}.Layout))
			addButton := func(it dockItem) {
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return a.layoutDockButton(gtx, it)
				}))
				children = append(children, layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout))
			}
			for _, it := range modes {
				addButton(it)
			}
			// Divider, then the HUB: buttons that open real standalone tool windows.
			children = append(children, layout.Rigid(a.layoutDockDivider))
			for _, it := range hub {
				addButton(it)
			}
			return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx, children...)
		})
	})
}

// layoutDockButton draws one big square icon button. Active = filled accent (icon goes
// black for contrast); idle = dark with a neon icon; hover = subtle glow + tooltip. The
// whole square is the click target.
func (a *App) layoutDockButton(gtx layout.Context, it dockItem) layout.Dimensions {
	side := gtx.Dp(unit.Dp(dockButtonSize))
	gtx.Constraints = layout.Exact(image.Pt(side, side))
	return it.clicker.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		hovered := it.clicker.Hovered()
		bg := colCanvasBg
		switch {
		case it.active:
			bg = it.accent
		case hovered:
			bg = colHeaderBg
		}
		fillRRect(gtx.Ops, image.Rect(0, 0, side, side), 10, bg)
		if hovered && !it.active {
			fillRRect(gtx.Ops, image.Rect(0, 0, side, side), 10, colGlow) // glow overlay
		}
		strokeRect(gtx.Ops, image.Rect(0, 0, side, side), it.accent) // neon edge

		iconCol := it.accent
		if it.active {
			iconCol = colWindowBg // black icon on a neon fill
		}
		layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			isz := gtx.Dp(unit.Dp(28))
			gtx.Constraints = layout.Exact(image.Pt(isz, isz))
			if it.icon != nil {
				return it.icon.Layout(gtx, iconCol)
			}
			fillRRect(gtx.Ops, image.Rect(0, 0, isz, isz), 4, iconCol) // placeholder, never crash
			return layout.Dimensions{Size: image.Pt(isz, isz)}
		})

		if hovered {
			a.drawTooltip(gtx, side, it.tip)
		}
		return layout.Dimensions{Size: image.Pt(side, side)}
	})
}

// layoutBrandDiamond draws the Scene-Kid diamond: a neon-green rotated-square outline,
// the canvas's little brand mark at the top of the dock.
func (a *App) layoutBrandDiamond(gtx layout.Context) layout.Dimensions {
	side := gtx.Dp(unit.Dp(34))
	gtx.Constraints = layout.Exact(image.Pt(side, side))
	c := float32(side) / 2
	d := float32(side)/2 - 2
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(c, c-d))
	p.LineTo(f32.Pt(c+d, c))
	p.LineTo(f32.Pt(c, c+d))
	p.LineTo(f32.Pt(c-d, c))
	p.Close()
	paint.FillShape(gtx.Ops, colNeonGreen, clip.Stroke{Path: p.End(), Width: 2.5}.Op())
	return layout.Dimensions{Size: image.Pt(side, side)}
}

// layoutDockDivider draws a thin horizontal rule separating the mode buttons (top)
// from the hub launch buttons (bottom) — a quiet visual cue that the lower group
// opens real standalone tool windows.
func (a *App) layoutDockDivider(gtx layout.Context) layout.Dimensions {
	w := gtx.Dp(unit.Dp(36))
	h := gtx.Dp(unit.Dp(2))
	padV := gtx.Dp(unit.Dp(5))
	fillRRect(gtx.Ops, image.Rect(0, padV, w, padV+h), 1, colGlow)
	return layout.Dimensions{Size: image.Pt(w, h+padV*2)}
}

// drawTooltip paints a small floating tooltip just to the right of a dock button. It
// records the label, draws a pill behind it, then replays the label — the standard Gio
// "background under a widget" idiom, offset beside the button.
func (a *App) drawTooltip(gtx layout.Context, btnSide int, txt string) {
	defer op.Offset(image.Pt(btnSide+8, btnSide/2-12)).Push(gtx.Ops).Pop()
	macro := op.Record(gtx.Ops)
	dims := layout.UniformInset(unit.Dp(5)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		lbl := material.Body2(a.th, txt)
		lbl.Color = colText
		return lbl.Layout(gtx)
	})
	call := macro.Stop()
	fillRRect(gtx.Ops, image.Rect(0, 0, dims.Size.X, dims.Size.Y), 5, colHeaderBg)
	strokeRect(gtx.Ops, image.Rect(0, 0, dims.Size.X, dims.Size.Y), colNeonGreen)
	call.Add(gtx.Ops)
}
