//go:build gui

package main

import (
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// layoutToolbar renders a thin row of Save / Load / Undo / Redo buttons above the
// transport. Each button is sized to its label (the old 44x44 squares clipped
// "Save" -> "Sav e"). No new colors: Save/Load = neon green, Undo/Redo = electric blue.
func (a *App) layoutToolbar(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return a.toolBtn(gtx, &a.saveBtn, "Save", colNeonGreen) }),
		layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return a.toolBtn(gtx, &a.loadBtn, "Load", colNeonGreen) }),
		layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return a.toolBtn(gtx, &a.undoBtn, "Undo", colElecBlue) }),
		layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return a.toolBtn(gtx, &a.redoBtn, "Redo", colElecBlue) }),
		layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
		// Speak: becky voices the agent-box text (or her last line) via NeuTTS Air.
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.toolBtn(gtx, &a.speakBtn, a.speakLabel(), colNeonPink)
		}),
	)
}

// speakLabel shows "Speaking…" while an utterance is in flight, else "Speak".
func (a *App) speakLabel() string {
	a.mu.Lock()
	sp := a.speaking
	a.mu.Unlock()
	if sp {
		return "Speaking…"
	}
	return "Speak"
}

// toolBtn is a labeled pill sized to its text, with a neon-edged rounded background.
// layout.Background draws the pill behind the label and inherits the label's size,
// so the text never clips regardless of its length.
func (a *App) toolBtn(gtx layout.Context, btn *widget.Clickable, text string, accent color.NRGBA) layout.Dimensions {
	return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		fg, border := accent, accent
		if btn.Hovered() {
			fg, border = colNeonGreen, colNeonGreen
		}
		return layout.Background{}.Layout(gtx,
			func(gtx layout.Context) layout.Dimensions {
				sz := gtx.Constraints.Min
				fillRRect(gtx.Ops, image.Rect(0, 0, sz.X, sz.Y), 6, colCanvasBg)
				strokeRect(gtx.Ops, image.Rect(0, 0, sz.X, sz.Y), border)
				return layout.Dimensions{Size: sz}
			},
			func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(7)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					lbl := material.Caption(a.th, text)
					lbl.Color = fg
					return lbl.Layout(gtx)
				})
			},
		)
	})
}
