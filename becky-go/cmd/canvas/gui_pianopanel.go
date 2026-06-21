//go:build gui

// gui_pianopanel.go — the in-window PIANO ROLL (CANVAS-BLUEPRINT.md panel 2b).
//
// CONTRACT (a subagent fills the body; keep these signatures stable):
//   - type pianoPanel + func newPianoPanel() *pianoPanel
//   - func (p *pianoPanel) layout(gtx, a *App) layout.Dimensions  — renders the roll
//     for a.arr and handles its OWN pointer input, emitting edits via a.applyArr.
//
// It reads the editable model a.arr and edits it with the dawmodel pianoroll verbs
// (AddNote/MoveNotes/ResizeNotes/SetVelocity/DeleteNotes/Transpose), each returning
// a NEW arrangement passed to a.applyArr. a.firstMidiClip() gives the default edit
// target. This stub draws a placeholder so the build stays green.
package main

import "gioui.org/layout"

type pianoPanel struct{}

func newPianoPanel() *pianoPanel { return &pianoPanel{} }

func (p *pianoPanel) layout(gtx layout.Context, a *App) layout.Dimensions {
	return panelPlaceholder(gtx, a, "piano roll — open a project.json or .mid (panel pending)")
}
