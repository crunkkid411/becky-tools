// becky-ask — the lightweight, double-click chat front-door to becky-tools.
//
// becky-ask is the NATURAL-LANGUAGE chat window. becky.exe is the COMMAND ENGINE
// (structured ops like `becky profile "X"`); becky-ask is the human-facing chat
// layer that drives becky's tools from plain language. See SPEC-BECKY-ASK.md.
//
// What this binary does now:
//   - DRAG-AND-DROP / argv target: dropping a file or folder onto becky-ask.exe
//     passes its path(s) as command-line args; becky-ask treats that as "this is
//     what I'm referring to" (the Target) and offers one-key actions for it.
//   - Quick-action buttons: selectable rows (number keys) run the obvious becky-*
//     op on the target with no typing.
//   - Act-vs-discuss intent: a clear action on a target ("transcribe this") runs;
//     a question/idea/ambiguous request is answered or clarified — never a tool
//     call by default. The intent step uses the local Qwen3.5 model when present,
//     else the offline keyword catalog.
//   - SINGLE-SHOT (scriptable) mode: `--question "<q>"` (or `--image <f>
//     --question "<q>"`) answers ONE request, prints a plain answer, and exits —
//     for scripts/agents/CI. This is a PURE ADDITION beside the interactive TUI;
//     it is opt-in (only via an explicit flag) and NEVER the default. See
//     SPEC-ASK-SINGLESHOT.md and singleshot.go.
//
// Usage: run it (double-click, or `becky-ask [path ...]` on PATH). With a real
// terminal and NO single-shot flag it launches the colored bubbletea TUI — the
// accessibility-AID default (ACCESSIBILITY.md). With no TTY it prints what it
// parsed and exits cleanly rather than crashing.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

// askMode is the top-level entry becky-ask resolves to from its flags/environment.
// Making this a named, testable value is load-bearing: a regression test asserts
// that an explicit single-shot flag selects modeSingleShot and that the default
// (no flag + a terminal) selects modeTUI — i.e. single-shot can NEVER shadow the
// interactive colored TUI (the accessibility AID; ACCESSIBILITY.md, SPEC §1).
type askMode int

const (
	modeTUI         askMode = iota // interactive colored bubbletea window (the DEFAULT)
	modeSingleShot                 // --question / --image: print one answer, exit
	modeHeadlessRun                // BECKY_ASK_RUN=<op> on a target (existing no-TTY path)
	modeNoTTY                      // piped/headless with no flag/env: printNoTTY echo
)

// decideMode is the ONLY behavioral switch added to startup, expressed as a pure
// function so it is unit-testable without a terminal. Precedence (SPEC §3.1):
//
//	single-shot flag present            -> modeSingleShot   [NEW, opt-in only]
//	else terminal on stdin              -> modeTUI          [UNCHANGED DEFAULT]
//	else BECKY_ASK_RUN set              -> modeHeadlessRun  [UNCHANGED]
//	else                                -> modeNoTTY        [UNCHANGED]
//
// Single-shot is checked FIRST and fires ONLY on an explicit flag, so it can never
// auto-select just because something looks headless, and the interactive TUI stays
// the default whenever a real terminal is present and no single-shot flag is given.
func decideMode(ss *ssFlags, isTTY bool, beckyAskRun string) askMode {
	if ss != nil && ss.isSingleShot() {
		return modeSingleShot
	}
	if isTTY {
		return modeTUI
	}
	if strings.TrimSpace(beckyAskRun) != "" {
		return modeHeadlessRun
	}
	return modeNoTTY
}

func main() {
	ss, rest := parseSingleShotFlags(os.Args[1:])
	isTTY := term.IsTerminal(int(os.Stdin.Fd()))

	switch decideMode(ss, isTTY, os.Getenv("BECKY_ASK_RUN")) {
	case modeSingleShot:
		os.Exit(runSingleShot(ss))
	case modeTUI:
		launchTUI(rest)
	case modeHeadlessRun:
		os.Exit(runHeadless(strings.TrimSpace(os.Getenv("BECKY_ASK_RUN")), rest))
	default: // modeNoTTY
		printNoTTY(rest)
		os.Exit(0)
	}
}

// launchTUI starts the interactive colored bubbletea window. It is split out from
// main so the mode-selection logic can be tested without ever entering bubbletea
// (the accessibility guard: TestSingleShot_DoesNotLaunchTUI replaces this seam with
// a spy). The body is byte-for-byte the original TUI launch — WithAltScreen +
// WithMouseCellMotion — so the human window is unchanged.
var launchTUI = func(args []string) {
	p := tea.NewProgram(
		modelFromArgs(args),
		tea.WithAltScreen(),       // full-window chat, like the original
		tea.WithMouseCellMotion(), // mouse-wheel scrollback in the viewport
	)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "becky-ask error: %v\n", err)
		os.Exit(1)
	}
}

// runHeadless executes one request without the TUI (BECKY_ASK_RUN=<op>). "transcribe"
// runs the full verified-transcript workflow; any other op runs its single tool. It
// prints the saved sidecar paths + a one-line summary and returns a process exit code.
func runHeadless(op string, args []string) int {
	t := resolveTarget(args)
	if !t.HasTarget() {
		fmt.Fprintln(os.Stderr, "becky-ask --run: no existing file/folder in args")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	var res runResult
	if op == "transcribe" {
		res = runWorkflow(ctx, t, []actionID{actTranscribe})
	} else {
		res = runOps(ctx, t, []actionID{actionID(op)})
	}
	for _, s := range res.Saved {
		fmt.Fprintln(os.Stderr, "Saved: "+s)
	}
	if res.Stdout != "" {
		fmt.Fprintln(os.Stderr, res.Stdout)
	}
	if res.Err != nil {
		fmt.Fprintln(os.Stderr, "error: "+res.Err.Error())
		return 1
	}
	return 0
}

// printNoTTY summarizes, to stderr, what becky-ask would start with — the dropped
// Target and the quick actions that apply — then explains it needs a terminal.
// This is the headless-observable proof that an argv path becomes the Target.
func printNoTTY(args []string) {
	t := resolveTarget(args)
	if t.HasTarget() {
		fmt.Fprintf(os.Stderr, "becky-ask: Target = %s\n", t.Label())
		fmt.Fprintf(os.Stderr, "becky-ask: target path = %s\n", t.Primary())
		actions := quickActionsFor(t)
		if len(actions) > 0 {
			fmt.Fprintln(os.Stderr, "becky-ask: one-key actions available:")
			for i, a := range actions {
				cmd := commandFor(a, t)
				fmt.Fprintf(os.Stderr, "  [%d] %-10s -> %s\n", i+1, a.Label, commandString(cmd))
			}
		} else {
			fmt.Fprintln(os.Stderr, "becky-ask: no one-key action fits this target.")
		}
	} else if len(args) > 0 {
		fmt.Fprintf(os.Stderr, "becky-ask: no existing file/folder among args: %v\n", args)
	}
	fmt.Fprintln(os.Stderr, "becky-ask is an interactive chat window — run it in a terminal (double-click becky-ask.exe, or `becky-ask` in a console).")
}
