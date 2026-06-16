//go:build gui

// gui_theme.go — shared colours, small text helpers, and a couple of canvas text
// overlays used across the GUI files. Kept tiny and dependency-light: the palette is a
// deliberate dark-studio look (not a default toolkit theme), per the design rules.
package main

import (
	"fmt"
	"image/color"
	"runtime"

	"gioui.org/layout"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// Palette — a dark "studio" surface with a warm accent. Defined once so the whole
// canvas shares one intentional look rather than scattered hardcoded colours.
var (
	colWindowBg   = color.NRGBA{R: 0x16, G: 0x18, B: 0x1d, A: 0xff} // app background
	colPanelBg    = color.NRGBA{R: 0x1e, G: 0x21, B: 0x28, A: 0xff} // side panels
	colCanvasBg   = color.NRGBA{R: 0x10, G: 0x12, B: 0x16, A: 0xff} // visual area
	colHeaderBg   = color.NRGBA{R: 0x22, G: 0x26, B: 0x2f, A: 0xff} // top bar / tabs
	colAccent     = color.NRGBA{R: 0xe8, G: 0x8a, B: 0x3c, A: 0xff} // warm orange accent
	colAccentDim  = color.NRGBA{R: 0x6a, G: 0x4a, B: 0x28, A: 0xff} // inactive accent
	colText       = color.NRGBA{R: 0xe6, G: 0xe8, B: 0xec, A: 0xff} // primary text
	colTextDim    = color.NRGBA{R: 0x9a, G: 0xa0, B: 0xaa, A: 0xff} // secondary text
	colGridLine   = color.NRGBA{R: 0x33, G: 0x38, B: 0x42, A: 0xff} // ruler / centre line
	colWave       = color.NRGBA{R: 0x6c, G: 0xc6, B: 0xff, A: 0xff} // waveform body
	colClip       = color.NRGBA{R: 0x4a, G: 0x9e, B: 0x6a, A: 0xff} // DAW clip block
	colLaneA      = color.NRGBA{R: 0x14, G: 0x17, B: 0x1c, A: 0xff} // lane row (even)
	colLaneB      = color.NRGBA{R: 0x18, G: 0x1c, B: 0x22, A: 0xff} // lane row (odd)
	colLaneHeader = color.NRGBA{R: 0x26, G: 0x2b, B: 0x34, A: 0xff} // lane name strip
)

// isWindows reports whether we're running on Windows (drives the .exe suffix).
func isWindows() bool { return runtime.GOOS == "windows" }

// secs formats a duration in seconds as a short m:ss label for captions.
func secs(d float64) string {
	if d <= 0 {
		return "0:00"
	}
	total := int(d + 0.5)
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}

// plural returns "<n> <singular>" or "<n> <plural>" for friendly counts.
func plural(n int, singular, pluralWord string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, pluralWord)
}

// drawCanvasCaption draws a small dim caption in the top-left of the visual area.
func (a *App) drawCanvasCaption(gtx layout.Context, th *material.Theme, txt string) {
	inset := layout.UniformInset(unit.Dp(8))
	inset.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		lbl := material.Body2(th, txt)
		lbl.Color = colTextDim
		return lbl.Layout(gtx)
	})
}

// drawCanvasHint centres a friendly multi-line hint when there's nothing to draw.
func (a *App) drawCanvasHint(gtx layout.Context, th *material.Theme, txt string) {
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		// Constrain the hint width so long messages wrap nicely.
		if maxW := gtx.Dp(unit.Dp(420)); gtx.Constraints.Max.X > maxW {
			gtx.Constraints.Max.X = maxW
		}
		lbl := material.Body1(th, txt)
		lbl.Color = colTextDim
		lbl.Alignment = text.Middle
		return lbl.Layout(gtx)
	})
}
