//go:build gui

package main

import (
	"gioui.org/layout"
	"gioui.org/unit"
)

// layoutToolbar renders a thin horizontal row of Save / Load / Undo / Redo
// buttons just above the transport row. It reuses the overlayBtn affordance
// (the same 44x44 neon-edged squares used by the Play / Stop transport) so
// the visual language is consistent. No new colors -- Save/Load use the
// primary neon-green accent; Undo/Redo use the electric-blue accent.
func (a *App) layoutToolbar(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.overlayBtn(gtx, &a.saveBtn, "Save", colNeonGreen)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.overlayBtn(gtx, &a.loadBtn, "Load", colNeonGreen)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.overlayBtn(gtx, &a.undoBtn, "Undo", colElecBlue)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.overlayBtn(gtx, &a.redoBtn, "Redo", colElecBlue)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
	)
}
