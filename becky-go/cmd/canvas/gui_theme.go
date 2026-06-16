//go:build gui

// gui_theme.go — the BRAND palette + icon set + tiny text/format helpers shared across
// the GUI. The look is Jordan's brand: raw, rebellious, high-contrast neon-on-black
// (punk / scene-kid / DIY — NOT a corporate toolkit theme). Text is used MINIMALLY;
// colour and shape carry the meaning. Each mode owns one accent colour so the window
// tells you where you are at a glance.
package main

import (
	"fmt"
	"image/color"
	"runtime"

	"gioui.org/layout"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"becky-go/internal/canvas"

	"golang.org/x/exp/shiny/materialdesign/icons"
)

// Brand palette — black + near-black panels, neon-green primary, with secondary neon
// accents reserved for distinct modes/states. Defined once so the whole canvas shares
// one intentional, branded look (per Jordan's brand file).
var (
	colWindowBg = color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0xff} // pure black app bg
	colPanelBg  = color.NRGBA{R: 0x0a, G: 0x0a, B: 0x0a, A: 0xff} // near-black panels (dock)
	colCanvasBg = color.NRGBA{R: 0x05, G: 0x05, B: 0x05, A: 0xff} // the canvas surface
	colHeaderBg = color.NRGBA{R: 0x14, G: 0x14, B: 0x14, A: 0xff} // raised chrome / hover
	colGridLine = color.NRGBA{R: 0x33, G: 0x33, B: 0x33, A: 0xff} // dark-gray lines/frames

	// Neon accents.
	colNeonGreen  = color.NRGBA{R: 0x39, G: 0xff, B: 0x14, A: 0xff} // PRIMARY accent
	colElecBlue   = color.NRGBA{R: 0x00, G: 0xae, B: 0xef, A: 0xff} // electric blue
	colDeepPurple = color.NRGBA{R: 0x80, G: 0x00, B: 0x80, A: 0xff} // deep purple
	colNeonPink   = color.NRGBA{R: 0xff, G: 0x00, B: 0x7f, A: 0xff} // neon pink
	colCrimson    = color.NRGBA{R: 0xdc, G: 0x14, B: 0x3c, A: 0xff} // crimson
	colYellow     = color.NRGBA{R: 0xff, G: 0xd7, B: 0x00, A: 0xff} // bright yellow (sparingly)

	colAccent = colNeonGreen // the default/primary accent

	// Text — white, used minimally.
	colText    = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff} // primary text (white)
	colTextDim = color.NRGBA{R: 0x88, G: 0x88, B: 0x88, A: 0xff} // secondary/dim text

	// Surface-specific colours, expressed through the neon accents.
	colWave       = colElecBlue                                     // waveform body (blue mode)
	colClip       = color.NRGBA{R: 0x1e, G: 0x6a, B: 0x10, A: 0xff} // DAW clip block (dim green)
	colClipEdge   = colNeonGreen                                    // clip outline
	colLaneA      = color.NRGBA{R: 0x08, G: 0x08, B: 0x08, A: 0xff} // lane row (even)
	colLaneB      = color.NRGBA{R: 0x0e, G: 0x0e, B: 0x0e, A: 0xff} // lane row (odd)
	colLaneHeader = color.NRGBA{R: 0x14, G: 0x14, B: 0x14, A: 0xff} // lane name strip
	colGlow       = color.NRGBA{R: 0x39, G: 0xff, B: 0x14, A: 0x33} // hover glow (translucent green)
)

// modeAccent maps each mode to its signature neon colour so the dock + canvas chrome
// recolour to tell Jordan which mode he's in (colour-coding, per the nice-to-have).
func modeAccent(m canvas.Mode) color.NRGBA {
	switch m {
	case canvas.ModeAsk:
		return colNeonGreen
	case canvas.ModeVideo:
		return colNeonPink
	case canvas.ModeDAW:
		return colElecBlue
	case canvas.ModeMIDI:
		return colDeepPurple
	case canvas.ModeDrum:
		return colYellow
	default:
		return colNeonGreen
	}
}

// --- icons -----------------------------------------------------------------------
//
// Material-design icons via golang.org/x/exp/shiny/materialdesign/icons, decoded once
// at startup (widget.NewIcon). A failed decode degrades to nil; the dock then draws a
// neon square placeholder instead of crashing.

// iconSet holds the decoded dock + chrome icons. nil entries draw a placeholder.
type iconSet struct {
	record   *widget.Icon // mic    — record audio
	draw     *widget.Icon // brush  — draw/pen mode
	piano    *widget.Icon // note   — piano-roll mode
	drum     *widget.Icon // grid   — drum machine
	video    *widget.Icon // movie  — video mode
	folder   *widget.Icon // open   — load a file/folder
	clear    *widget.Icon // X      — clear output
	expand   *widget.Icon // chevron— expand output
	collapse *widget.Icon // chevron— collapse output
	run      *widget.Icon // search — run the agent box
}

// loadIcons decodes every dock/chrome icon. A nil entry (decode failure) is tolerated
// by the dock renderer, which draws a placeholder glyph in its place — degrade, never
// crash.
func loadIcons() iconSet {
	mk := func(b []byte) *widget.Icon {
		ic, err := widget.NewIcon(b)
		if err != nil {
			return nil
		}
		return ic
	}
	return iconSet{
		record:   mk(icons.AVMic),
		draw:     mk(icons.ImageBrush),
		piano:    mk(icons.ImageMusicNote),
		drum:     mk(icons.ImageGridOn),
		video:    mk(icons.AVMovie),
		folder:   mk(icons.FileFolderOpen),
		clear:    mk(icons.ContentClear),
		expand:   mk(icons.NavigationExpandLess),
		collapse: mk(icons.NavigationExpandMore),
		run:      mk(icons.ActionSearch),
	}
}

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
	inset := layout.UniformInset(unit.Dp(10))
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
		if maxW := gtx.Dp(unit.Dp(440)); gtx.Constraints.Max.X > maxW {
			gtx.Constraints.Max.X = maxW
		}
		lbl := material.Body1(th, txt)
		lbl.Color = colTextDim
		lbl.Alignment = text.Middle
		return lbl.Layout(gtx)
	})
}
