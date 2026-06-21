//go:build gui

// gui_drumpanel.go — the in-window DRUM MACHINE (CANVAS-BLUEPRINT.md panel 2a).
// Replaces the old 4×16 toy with a real grid bound to the arrangement's drum lane.
//
// CONTRACT (a subagent fills the body; keep these signatures stable):
//   - type drumPanelState + func newDrumPanelState() *drumPanelState
//   - func (d *drumPanelState) layout(gtx, a *App) layout.Dimensions
//
// It reads a.arr's drum lane via a.arr.DrumGridOf(trackID, clipName, stepTicks),
// renders the steps×lanes grid, and on a click calls DrumGrid.SetStep then
// a.arr.ApplyDrumGrid(...) → a.applyArr (immutable). May embed a
// drummachine.Machine for kit/pad richness (reuse cmd/drummachine's Gio code). This
// stub draws a placeholder so the build stays green.
package main

import "gioui.org/layout"

type drumPanelState struct{}

func newDrumPanelState() *drumPanelState { return &drumPanelState{} }

func (d *drumPanelState) layout(gtx layout.Context, a *App) layout.Dimensions {
	return panelPlaceholder(gtx, a, "drum machine — open a session, or use the Drum Machine hub button (panel pending)")
}
