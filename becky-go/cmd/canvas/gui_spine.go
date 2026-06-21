//go:build gui

// gui_spine.go — the Becky Canvas SPINE plumbing (CANVAS-BLUEPRINT.md Step 1). The
// App holds ONE dawmodel.Arrangement as the source of musical truth; every panel
// edit funnels through applyArr, which rebuilds the derived scene cache. Loading a
// becky-compose project.json (or a .mid) fills the arrangement so the panels show
// the real session.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"becky-go/internal/canvasbridge"
	"becky-go/internal/dawmodel"
)

// applyArr swaps in a new arrangement as the source of truth and rebuilds the
// derived scene cache, then repaints. Every panel edit calls this. A nil next is
// ignored (degrade, never crash).
func (a *App) applyArr(next *dawmodel.Arrangement) {
	if next == nil {
		return
	}
	a.arr = next
	a.scene = canvasbridge.SceneFromArrangement(next)
	if a.window != nil {
		a.window.Invalidate()
	}
}

// maybeLoadArrangement loads a dropped/opened target into the editable arrangement
// when it's a becky-compose project.json or a .mid, so the panels show the real
// session. Anything else leaves arr untouched. Returns true when it loaded.
func (a *App) maybeLoadArrangement(path string) bool {
	p := strings.TrimSpace(path)
	low := strings.ToLower(p)
	switch {
	case strings.HasSuffix(low, ".json"):
		arr, err := canvasbridge.ArrangementFromProjectFile(p)
		if err != nil || arr == nil || len(arr.Tracks) == 0 {
			return false
		}
		a.applyArr(arr)
		a.appendLine(fmt.Sprintf("loaded session: %s (%d tracks)", filepath.Base(p), len(arr.Tracks)))
		return true
	case strings.HasSuffix(low, ".mid"), strings.HasSuffix(low, ".midi"):
		data, err := os.ReadFile(p)
		if err != nil {
			return false
		}
		arr, perr := dawmodel.FromSMF(data)
		if arr == nil || len(arr.Tracks) == 0 {
			return false
		}
		a.applyArr(arr)
		note := ""
		if perr != nil {
			note = " (partial)"
		}
		a.appendLine(fmt.Sprintf("loaded MIDI: %s%s", filepath.Base(p), note))
		return true
	}
	return false
}

// firstMidiClip returns the trackID + clipName of the first non-empty MIDI clip in
// the arrangement (the piano roll's default edit target) and whether one was found.
func (a *App) firstMidiClip() (trackID, clipName string, ok bool) {
	if a.arr == nil {
		return "", "", false
	}
	for _, t := range a.arr.Tracks {
		if t.Kind == dawmodel.KindAudio {
			continue
		}
		for _, c := range t.Clips {
			return t.ID, c.Name, true
		}
	}
	return "", "", false
}

// panelPlaceholder centers a one-line hint in the panel area — the shared empty
// state a stub panel (or an empty arrangement) shows. Subagents render real content
// instead; this stays the degrade target.
func panelPlaceholder(gtx layout.Context, a *App, hint string) layout.Dimensions {
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body1(a.th, hint)
			lbl.Color = colTextDim
			return lbl.Layout(gtx)
		})
	})
}
