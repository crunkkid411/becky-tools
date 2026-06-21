//go:build gui

// gui_audiopanel.go — the in-window AUDIO / vocal tracks (CANVAS-BLUEPRINT.md panel
// 2d). Lower priority; defined now so the spine contract is complete.
//
// CONTRACT (a subagent fills the body; keep these signatures stable):
//   - type audioPanel + func newAudioPanel() *audioPanel
//   - func (p *audioPanel) layout(gtx, a *App) layout.Dimensions
//
// Renders a.arr's audio tracks (Kind==KindAudio) as min/max waveform bars from their
// clip Peaks; trim/move are clip edits → a.applyArr. This stub draws a placeholder.
package main

import "gioui.org/layout"

type audioPanel struct{}

func newAudioPanel() *audioPanel { return &audioPanel{} }

func (p *audioPanel) layout(gtx layout.Context, a *App) layout.Dimensions {
	return panelPlaceholder(gtx, a, "audio tracks (panel pending)")
}
