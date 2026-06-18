//go:build gui

// gui_theme.go — the BRAND palette + tiny drawing helpers for becky-drummachine.
// COPIED from cmd/canvas/gui_theme.go + gui_waveform.go (both `package main`, so we
// can't import them) to keep ONE visual language across becky's GUIs: raw neon-on-
// black, colour and shape over text. The look is Jordan's brand (punk / scene-kid /
// DIY) — neon-green #39FF14 primary on pure black.
package main

import (
	"image"
	"image/color"
	"runtime"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"golang.org/x/exp/shiny/materialdesign/icons"
)

// Brand palette — identical values to cmd/canvas so the two windows match.
var (
	colWindowBg = color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0xff} // pure black app bg
	colPanelBg  = color.NRGBA{R: 0x0a, G: 0x0a, B: 0x0a, A: 0xff} // near-black panels
	colCanvasBg = color.NRGBA{R: 0x05, G: 0x05, B: 0x05, A: 0xff} // the work surface
	colHeaderBg = color.NRGBA{R: 0x14, G: 0x14, B: 0x14, A: 0xff} // raised chrome / hover
	colGridLine = color.NRGBA{R: 0x33, G: 0x33, B: 0x33, A: 0xff} // dark-gray frames

	colNeonGreen = color.NRGBA{R: 0x39, G: 0xff, B: 0x14, A: 0xff} // PRIMARY accent
	colElecBlue  = color.NRGBA{R: 0x00, G: 0xae, B: 0xef, A: 0xff} // electric blue
	colNeonPink  = color.NRGBA{R: 0xff, G: 0x00, B: 0x7f, A: 0xff} // neon pink
	colCrimson   = color.NRGBA{R: 0xdc, G: 0x14, B: 0x3c, A: 0xff} // crimson (stop)
	colYellow    = color.NRGBA{R: 0xff, G: 0xd7, B: 0x00, A: 0xff} // bright yellow

	colText    = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff} // primary text
	colTextDim = color.NRGBA{R: 0x88, G: 0x88, B: 0x88, A: 0xff} // dim text
)

// padAccents are the per-row neon accents for the 4x4 pad grid (top row green,
// then blue, yellow, pink) so the four rows read apart at a glance — colour over text.
var padAccents = [4]color.NRGBA{colNeonGreen, colElecBlue, colYellow, colNeonPink}

// padAccent returns the accent for the pad at grid index 0..15 (by its row).
func padAccent(pad int) color.NRGBA {
	row := pad / 4
	if row < 0 || row >= len(padAccents) {
		return colNeonGreen
	}
	return padAccents[row]
}

// dim darkens an accent for an "off"/idle state (same hue, much lower brightness).
func dim(c color.NRGBA) color.NRGBA {
	return color.NRGBA{R: c.R / 7, G: c.G / 7, B: c.B / 7, A: 0xff}
}

// --- icons -----------------------------------------------------------------------

// iconSet holds the decoded chrome icons; a nil entry draws a placeholder square.
type iconSet struct {
	play   *widget.Icon
	stop   *widget.Icon
	folder *widget.Icon
	save   *widget.Icon
	run    *widget.Icon
}

// loadIcons decodes the chrome icons. A failed decode degrades to nil (placeholder).
func loadIcons() iconSet {
	mk := func(b []byte) *widget.Icon {
		ic, err := widget.NewIcon(b)
		if err != nil {
			return nil
		}
		return ic
	}
	return iconSet{
		play:   mk(icons.AVPlayArrow),
		stop:   mk(icons.AVStop),
		folder: mk(icons.FileFolderOpen),
		save:   mk(icons.ContentSave),
		run:    mk(icons.ActionSearch),
	}
}

// isWindows reports whether we're on Windows (drives the .exe suffix).
func isWindows() bool { return runtime.GOOS == "windows" }

// --- drawing helpers (copied from cmd/canvas/gui_waveform.go) ---------------------

// fillRRect fills a rounded rectangle with colour c.
func fillRRect(ops *op.Ops, r image.Rectangle, radius int, c color.NRGBA) {
	defer clip.UniformRRect(r, radius).Push(ops).Pop()
	paint.ColorOp{Color: c}.Add(ops)
	paint.PaintOp{}.Add(ops)
}

// strokeRect draws a 1px rectangle outline in colour c (four hairline edges).
func strokeRect(ops *op.Ops, r image.Rectangle, c color.NRGBA) {
	edges := []image.Rectangle{
		{Min: r.Min, Max: image.Pt(r.Max.X, r.Min.Y+1)},
		{Min: image.Pt(r.Min.X, r.Max.Y-1), Max: r.Max},
		{Min: r.Min, Max: image.Pt(r.Min.X+1, r.Max.Y)},
		{Min: image.Pt(r.Max.X-1, r.Min.Y), Max: r.Max},
	}
	for _, e := range edges {
		func() {
			defer clip.Rect(e).Push(ops).Pop()
			paint.ColorOp{Color: c}.Add(ops)
			paint.PaintOp{}.Add(ops)
		}()
	}
}

// widgetBg fills a widget's area with bg, then draws w on top (the standard Gio
// "background under a widget" idiom — copied from cmd/canvas/gui.go).
func widgetBg(gtx layout.Context, bg color.NRGBA, w layout.Widget) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := w(gtx)
	call := macro.Stop()

	rect := clip.Rect{Max: dims.Size}
	defer rect.Push(gtx.Ops).Pop()
	paint.ColorOp{Color: bg}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	call.Add(gtx.Ops)
	return dims
}

// borderBox draws a coloured 1px frame around w.
func borderBox(gtx layout.Context, edge color.NRGBA, w layout.Widget) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := w(gtx)
	call := macro.Stop()
	strokeRect(gtx.Ops, image.Rect(0, 0, dims.Size.X, dims.Size.Y), edge)
	call.Add(gtx.Ops)
	return dims
}

// fieldBox wraps a text editor in a padded, canvas-coloured box so it reads as input.
func fieldBox(gtx layout.Context, w layout.Widget) layout.Dimensions {
	return widgetBg(gtx, colCanvasBg, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(unit.Dp(8)).Layout(gtx, w)
	})
}

// caption draws a small dim caption label.
func (a *App) caption(gtx layout.Context, txt string) layout.Dimensions {
	lbl := material.Caption(a.th, txt)
	lbl.Color = colTextDim
	return lbl.Layout(gtx)
}
