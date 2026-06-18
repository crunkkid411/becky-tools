//go:build gui

// gui_pads.go — the 16 PADS (4x4 Maschine layout) and the per-pad STEP SEQUENCER
// row. Pads and steps are big, colour-coded clickable squares — "colours and shapes
// over text". Each is a widget.Clickable laid out as a coloured tile; clicking a pad
// selects + auditions it, clicking a step cell toggles a hit.
//
// All edits go through the immutable drummachine model: an edit returns a NEW machine
// which replaces a.machine, and the next frame re-renders from it. Same path the AI
// box uses, so the human and the AI drive the exact same model.
package main

import (
	"image"
	"image/color"
	"strconv"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"becky-go/internal/drummachine"
)

// padGridCols / padGridRows fix the Maschine 4x4 layout. The grid is drawn TOP row
// first (pad 12..15 on top, pad 0..3 on the bottom) so it matches hardware where the
// bottom-left pad is the kick (pad 0).
const (
	padGridCols = 4
	padGridRows = 4
)

// layoutPads draws the 4x4 pad grid as the big central surface. Row order is bottom-
// up (hardware layout: kick at bottom-left), so the top visual row is pads 12..15.
func (a *App) layoutPads(gtx layout.Context) layout.Dimensions {
	return borderBox(gtx, colGridLine, func(gtx layout.Context) layout.Dimensions {
		return widgetBg(gtx, colCanvasBg, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				rows := make([]layout.FlexChild, 0, padGridRows*2-1)
				for visRow := 0; visRow < padGridRows; visRow++ {
					if visRow > 0 {
						rows = append(rows, layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout))
					}
					row := visRow // hardware row from the bottom: top visual row = highest pads
					rows = append(rows, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return a.layoutPadRow(gtx, padGridRows-1-row)
					}))
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
			})
		})
	})
}

// layoutPadRow draws one row of 4 pads for hardware row hwRow (0 = bottom).
func (a *App) layoutPadRow(gtx layout.Context, hwRow int) layout.Dimensions {
	cells := make([]layout.FlexChild, 0, padGridCols*2-1)
	for col := 0; col < padGridCols; col++ {
		if col > 0 {
			cells = append(cells, layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout))
		}
		pad := hwRow*padGridCols + col
		cells = append(cells, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return a.layoutPad(gtx, pad)
		}))
	}
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, cells...)
}

// layoutPad draws one pad tile: a big rounded square, neon-bright when selected,
// dim-but-coloured otherwise, with its name. A muted pad reads crossed-out-dim; a
// pad with any active step gets a small lit corner so the beat shows on the grid.
func (a *App) layoutPad(gtx layout.Context, pad int) layout.Dimensions {
	if pad < 0 || pad >= drummachine.PadCount {
		return layout.Dimensions{}
	}
	btn := &a.pads[pad]
	accent := padAccent(pad)
	return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		size := gtx.Constraints.Max
		r := image.Rect(0, 0, size.X, size.Y)

		fill := dim(accent)
		border := accent
		switch {
		case pad == a.selected:
			fill = accent // selected pad glows full-bright
		case btn.Hovered():
			fill = color.NRGBA{R: accent.R / 3, G: accent.G / 3, B: accent.B / 3, A: 0xff}
		}
		if a.padMuted(pad) {
			fill = colHeaderBg
			border = colTextDim
		}
		fillRRect(gtx.Ops, r, 10, fill)
		strokeRect(gtx.Ops, r, border)

		// A lit corner dot when this pad has at least one active step (beat shows).
		if a.padHasHits(pad) {
			d := gtx.Dp(unit.Dp(10))
			fillRRect(gtx.Ops, image.Rect(size.X-d-6, 6, size.X-6, 6+d), 5, colNeonGreen)
		}

		// The pad name (the only text; small, contrast-aware).
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body2(a.th, a.padLabel(pad))
			lbl.Color = a.padTextColor(pad)
			lbl.TextSize = unit.Sp(13)
			return lbl.Layout(gtx)
		})
	})
}

// padTextColor picks black-on-bright for the selected pad, dim-white otherwise, for
// legibility against the tile fill.
func (a *App) padTextColor(pad int) color.NRGBA {
	if pad == a.selected {
		return colWindowBg // black text on the bright selected tile
	}
	return colText
}

// --- step sequencer --------------------------------------------------------------

// layoutSequencer draws the selected pad's step lane as a single row of cells. A lit
// cell = a hit; click toggles it. Every 4th cell gets a brighter group band so the
// beat (1-e-and-a) reads at a glance.
func (a *App) layoutSequencer(gtx layout.Context) layout.Dimensions {
	a.syncStepButtons()
	steps := len(a.stepBtns)
	if steps == 0 {
		return layout.Dimensions{}
	}
	accent := padAccent(a.selected)

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.caption(gtx, "steps — "+a.padLabel(a.selected))
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			cells := make([]layout.FlexChild, 0, steps*2-1)
			for i := 0; i < steps; i++ {
				if i > 0 {
					cells = append(cells, layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout))
				}
				step := i
				cells = append(cells, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return a.layoutStepCell(gtx, step, accent)
				}))
			}
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, cells...)
		}),
	)
}

// stepCellHeight fixes the sequencer row height so the cells are chunky and obvious.
const stepCellHeight = 44

// layoutStepCell draws one step cell for the selected pad's lane.
func (a *App) layoutStepCell(gtx layout.Context, step int, accent color.NRGBA) layout.Dimensions {
	btn := &a.stepBtns[step]
	on := a.stepOn(a.selected, step)
	h := gtx.Dp(unit.Dp(stepCellHeight))
	gtx.Constraints.Min.Y = h
	gtx.Constraints.Max.Y = h
	return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		size := gtx.Constraints.Max
		if size.Y > h {
			size.Y = h
		}
		r := image.Rect(0, 0, size.X, size.Y)
		// Downbeat band behind the first cell of each group of four.
		if step%4 == 0 {
			fillRRect(gtx.Ops, image.Rect(0, 0, size.X, size.Y), 6, colHeaderBg)
		}
		if on {
			fillRRect(gtx.Ops, r, 5, accent)
		} else {
			fillRRect(gtx.Ops, r, 5, dim(accent))
			strokeRect(gtx.Ops, r, accent)
		}
		return layout.Dimensions{Size: size}
	})
}

// --- model edits (immutable; same model the AI box drives) ------------------------

// selectPad selects pad i and auditions it (best-effort engine exec). Selecting also
// re-syncs the step-button slice to that pad's lane length.
func (a *App) selectPad(i int) {
	if i < 0 || i >= drummachine.PadCount {
		return
	}
	a.selected = i
	a.syncStepButtons()
	a.setStatus("pad: " + a.padLabel(i))
	a.auditionPad(i) // exec --play-pad (best-effort; silent if no engine)
}

// toggleStep flips one step on/off in the SELECTED pad's lane via the immutable model
// and swaps in the returned machine so the UI re-renders the change.
func (a *App) toggleStep(step int) {
	pat := a.activePattern()
	next, err := a.machine.ToggleStep(pat, a.selected, step)
	if err != nil {
		a.setStatus("couldn't toggle that step: " + err.Error())
		return
	}
	a.machine = next
	a.window.Invalidate()
}

// syncStepButtons resizes a.stepBtns to match the selected pad's lane length so each
// cell has a stable click target across re-renders.
func (a *App) syncStepButtons() {
	n := a.laneLen(a.selected)
	if len(a.stepBtns) != n {
		a.stepBtns = make([]widget.Clickable, n)
	}
}

// --- model queries ---------------------------------------------------------------

// activePattern returns the index of the pattern the first scene plays (matches
// machinectl's "current pattern" notion), clamped to a valid index.
func (a *App) activePattern() int {
	m := a.machine
	if m == nil || len(m.Scenes) == 0 {
		return 0
	}
	pi := m.Scenes[0].PatternIndex
	if pi < 0 || pi >= m.PatternCount() {
		return 0
	}
	return pi
}

// laneLen returns the number of steps in the selected pad's lane of the active
// pattern (degrades to drummachine.DefaultSteps).
func (a *App) laneLen(pad int) int {
	m := a.machine
	pat := a.activePattern()
	if m == nil || pat < 0 || pat >= m.PatternCount() {
		return drummachine.DefaultSteps
	}
	p := m.Bank.Patterns[pat]
	if pad < 0 || pad >= len(p.Lanes) {
		return p.Steps
	}
	return len(p.Lanes[pad])
}

// stepOn reports whether pad's lane has a hit at step in the active pattern.
func (a *App) stepOn(pad, step int) bool {
	m := a.machine
	pat := a.activePattern()
	if m == nil || pat < 0 || pat >= m.PatternCount() {
		return false
	}
	p := m.Bank.Patterns[pat]
	if pad < 0 || pad >= len(p.Lanes) || step < 0 || step >= len(p.Lanes[pad]) {
		return false
	}
	return p.Lanes[pad][step].On
}

// padHasHits reports whether pad has any active step in the active pattern.
func (a *App) padHasHits(pad int) bool {
	for s := 0; s < a.laneLen(pad); s++ {
		if a.stepOn(pad, s) {
			return true
		}
	}
	return false
}

// padMuted reports whether pad is muted (or not in the audible set due to a solo).
func (a *App) padMuted(pad int) bool {
	m := a.machine
	if m == nil || pad < 0 || pad >= len(m.Kit.Pads) {
		return false
	}
	if m.Kit.Pads[pad].Mute {
		return true
	}
	// A solo elsewhere makes this pad effectively silent.
	for _, idx := range m.AudiblePads() {
		if idx == pad {
			return false
		}
	}
	return true
}

// padLabel returns the pad's name (or "Pad N").
func (a *App) padLabel(pad int) string {
	m := a.machine
	if m != nil && pad >= 0 && pad < len(m.Kit.Pads) && m.Kit.Pads[pad].Name != "" {
		return m.Kit.Pads[pad].Name
	}
	return "Pad " + strconv.Itoa(pad+1)
}

// --- icon button -----------------------------------------------------------------

// iconBtn draws a compact labelled icon button: a neon-edged rounded square with an
// icon (or a placeholder square) and an optional short caption beneath. Hover
// brightens it. Mirrors cmd/canvas's iconBtn affordance.
func (a *App) iconBtn(gtx layout.Context, btn *widget.Clickable, ic *widget.Icon, label string, accent color.NRGBA) layout.Dimensions {
	return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		col := accent
		bg := colCanvasBg
		if btn.Hovered() {
			col = colNeonGreen
			bg = colHeaderBg
		}
		return layout.UniformInset(unit.Dp(4)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					side := gtx.Dp(unit.Dp(34))
					gtx.Constraints = layout.Exact(image.Pt(side, side))
					fillRRect(gtx.Ops, image.Rect(0, 0, side, side), 7, bg)
					strokeRect(gtx.Ops, image.Rect(0, 0, side, side), col)
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						isz := gtx.Dp(unit.Dp(20))
						gtx.Constraints = layout.Exact(image.Pt(isz, isz))
						if ic != nil {
							return ic.Layout(gtx, col)
						}
						// Placeholder glyph: a neon square (degrade, never crash).
						fillRRect(gtx.Ops, image.Rect(0, 0, isz, isz), 3, col)
						return layout.Dimensions{Size: image.Pt(isz, isz)}
					})
				}),
			)
		})
	})
}
