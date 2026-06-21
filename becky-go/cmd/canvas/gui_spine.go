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

	"encoding/json"

	"becky-go/internal/canvasbridge"
	"becky-go/internal/ctledit"
	"becky-go/internal/ctlmodel"
	"becky-go/internal/dawmodel"
)

// applyEditBatch applies a BeckyEditBatch (the AI-control action list) to the loaded
// arrangement via the deterministic ctledit applier — the select→ask→transform seam
// (CANVAS-BLUEPRINT.md Step 3). The natural-language→model→batch step is the local
// model boundary; this is the deterministic apply half, reachable now by feeding a
// JSON batch into the agent box. Returns true when the text WAS a batch (handled),
// false when it isn't JSON (so the caller falls through to keyword tool routing).
func (a *App) applyEditBatch(jsonText string) bool {
	batch, err := ctledit.ParseBatch([]byte(jsonText))
	if err != nil {
		return false // not an edit batch — let keyword routing try it
	}
	a.outExpanded = true
	if a.arr == nil || len(a.arr.Tracks) == 0 {
		a.appendLine("open a session first (drop a project.json), then apply an edit batch")
		return true
	}
	a.applyBatch(batch)
	return true
}

// applyPhrase is the deterministic plain-English fallback for the agent box: it
// turns a generative instruction ("randomize the beat", "make a house beat",
// "four on the floor") into a BeckyEditBatch via ctledit.ParsePhrase and applies
// it to the loaded drum clip — so generative beats work in the window with NO
// model. Returns true when the phrase WAS a recognised beat instruction (handled);
// false otherwise so the caller falls through to tool routing.
func (a *App) applyPhrase(text string) bool {
	if a.arr == nil || len(a.arr.Tracks) == 0 {
		return false
	}
	batch, ok := ctledit.ParsePhrase(text, a.arr)
	if !ok {
		return false
	}
	a.outExpanded = true
	a.applyBatch(batch)
	return true
}

// applyNL turns a plain-English instruction into a BeckyEditBatch via ctlmodel
// and applies it through the same ctledit seam as applyEditBatch. Returns true
// when the proposer produced at least one edit (handled); false otherwise so the
// caller can fall through to tool routing. When the model is not wired, ctlmodel
// degrades to its deterministic keyword proposer — so the seam works offline.
func (a *App) applyNL(phrase string) bool {
	if a.arr == nil || len(a.arr.Tracks) == 0 {
		return false
	}
	batch := ctlmodel.PickProposer().Propose(phrase, a.arr)
	if len(batch.Edits) == 0 {
		if batch.Summary != "" {
			a.appendLine("becky: " + batch.Summary)
		}
		return false
	}
	data, err := json.Marshal(batch)
	if err != nil {
		return false
	}
	return a.applyEditBatch(string(data))
}

// applyBatch applies a parsed BeckyEditBatch to the arrangement, swaps in the
// result, and reports the outcome. Shared by applyEditBatch (JSON) and
// applyPhrase (keyword fallback).
func (a *App) applyBatch(batch ctledit.BeckyEditBatch) {
	next, res, aerr := ctledit.Apply(a.arr, batch, nil)
	if aerr != nil {
		a.appendLine("edit batch error: " + firstLine(aerr.Error()))
		return
	}
	a.applyArr(next)
	if batch.Summary != "" {
		a.appendLine("becky: " + batch.Summary)
	}
	a.appendLine(fmt.Sprintf("applied %d edit(s), skipped %d", res.Applied, res.Skipped))
}

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
		if a.pianoPanel != nil {
			a.pianoPanel.pitchSet = false // re-fit the piano pitch range to the new clip
		}
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
		if a.pianoPanel != nil {
			a.pianoPanel.pitchSet = false // re-fit the piano pitch range to the new clip
		}
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
