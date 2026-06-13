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
//
// Usage: run it (double-click, or `becky-ask [path ...]` on PATH). It needs an
// interactive terminal; with no TTY (piped/headless) it prints what it parsed
// (incl. any dropped Target) and exits cleanly rather than crashing.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

func main() {
	args := os.Args[1:]

	// becky-ask is an interactive TUI. If there is no real terminal on stdin
	// (piped, redirected, or a headless runner), do NOT launch bubbletea (it would
	// error on a missing TTY). Instead, honestly report what we parsed — including
	// any dropped Target — and exit 0. This keeps the no-TTY path crash-free and
	// makes the drag-drop→target behavior observable without a terminal.
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		// Headless run mode: `BECKY_ASK_RUN=transcribe becky-ask <file>` runs the
		// workflow and exits — for scripting a folder, and for verifying behavior
		// without a terminal. With no env set, just report what was parsed.
		if op := strings.TrimSpace(os.Getenv("BECKY_ASK_RUN")); op != "" {
			os.Exit(runHeadless(op, args))
		}
		printNoTTY(args)
		os.Exit(0)
	}

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
