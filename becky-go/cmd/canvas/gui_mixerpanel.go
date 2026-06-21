//go:build gui

// gui_mixerpanel.go — the in-window MIXER + routing (CANVAS-BLUEPRINT.md panel 2c).
//
// CONTRACT (a subagent fills the body; keep these signatures stable):
//   - type mixerPanel + func newMixerPanel() *mixerPanel
//   - func (m *mixerPanel) layout(gtx, a *App) layout.Dimensions
//
// One channel strip per a.arr track: fader → a.arr.SetGain, pan → SetPan, mute/solo
// → SetMute/SetSolo, bus picker → RouteTo (all immutable, each returns a NEW
// arrangement passed to a.applyArr). A routing view draws a.arr.Buses + their
// sidechain edges. This stub draws a placeholder so the build stays green.
package main

import "gioui.org/layout"

type mixerPanel struct{}

func newMixerPanel() *mixerPanel { return &mixerPanel{} }

func (m *mixerPanel) layout(gtx layout.Context, a *App) layout.Dimensions {
	return panelPlaceholder(gtx, a, "mixer — open a session to see channel strips (panel pending)")
}
