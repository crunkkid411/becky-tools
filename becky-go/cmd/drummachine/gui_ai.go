//go:build gui

// gui_ai.go — THE CENTERPIECE: the AI box drives the GUI. The producer's hard
// requirement is "AI controls the GUI unless I click." So a plain-English line in the
// one command box runs the SAME words→edits pipeline a mouse click would:
//
//	PickParser().Parse(instruction, machine)  →  machinectl.Apply(machine, intent)
//	→  swap in the returned NEW machine  →  next frame RE-RENDERS the pads / steps /
//	   tempo / swing so Jordan WATCHES the controls change.
//
// Transport intents (Intent.Transport == play/stop) are NOT model edits — machinectl
// returns the machine unchanged and we trigger the SAME ▶/■ engine path the buttons
// use, so "play" / "stop" typed in the box work exactly like clicking the buttons.
//
// The summary string Apply returns becomes the one status line. degrade-never-crash:
// an unrecognised instruction is a friendly note, never an error.
package main

import (
	"strings"

	"becky-go/internal/machinectl"
)

// runInstruction is the AI-box submit handler. It parses + applies the instruction
// against the live machine, swaps in the result, surfaces the plain-English summary,
// and re-renders. Transport intents fire the engine instead of editing the model.
func (a *App) runInstruction(instruction string) {
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		return
	}
	a.command.SetText("")

	// PARSE: words → a normalized Intent (deterministic parser unless a local model
	// is wired; machinectl degrades to keywords automatically).
	intent, err := a.parser.Parse(instruction, a.machine)
	if err != nil {
		a.setStatus("becky: couldn't read that — " + err.Error())
		return
	}

	// TRANSPORT: play/stop is a signal, not an edit — drive the engine like the
	// ▶/■ buttons. (machinectl makes no sound; we own the engine exec.)
	if intent.Action == machinectl.Transport {
		switch intent.Transport {
		case machinectl.TransportStop:
			a.stopPlay()
			a.setStatus("■ stopped.")
		default:
			a.startPlay()
			a.setStatus("▶ play.")
		}
		return
	}

	// APPLY: Intent → a NEW machine + a plain-English summary.
	next, summary, _ := machinectl.Apply(a.machine, intent)

	// SWAP IN the new machine and RE-RENDER. If the edit changed the active pad's
	// lane length (e.g. a different pattern), the sequencer re-syncs on the next frame.
	a.machine = next
	a.syncStepButtons()
	a.setStatus(summary) // setStatus invalidates → the window redraws from the new model
}
