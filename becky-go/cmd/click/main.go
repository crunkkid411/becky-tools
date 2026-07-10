// becky-click - one dumb call to click a UI control BY NAME, the actuation
// primitive the world-action program needs. Wraps the proven no-pixel
// mouse-control breakthrough (docs/research/mouse-control-findings.md in
// hj-mission-control): find a control by its Name via .NET
// System.Windows.Automation (UIA -> InvokePattern) for modern/WPF/UWP/Chromium
// apps, falling back to the already-installed pywinauto win32 backend (BM_CLICK)
// for classic Win32/Notepad-class controls. No pixel coordinates, no synthetic
// mouse, no foreground steal - the OS routes the action by control identity, so
// none of the SendInput failure modes (cursor fights, snap/restore, stale
// coords) that got Jordan logged out apply.
//
//	becky-click --window "<title-substr>" --name "<control name>" [--control-type Button]
//	            [--verify] [--expect "<text>"] [--json]
//	becky-click --selftest       # OFFLINE proof of the orchestration + verify logic
//
// With --verify it screenshots the target window before+after the click and runs
// becky-ocr as a render check: --expect "<text>" is verified when that text is on
// screen after the click; without --expect it is verified when the rendered text
// changed (a real repaint), guarding against "clicked but nothing happened".
//
// Output: JSON envelope to stdout (beckyio contract), human summary to stderr
// unless --json. Exit: 0 = ok; 1 = failed (not found / click did not register)
// -> {"ok":false,...}; 2 = usage error.
//
// SCOPE (AUTOPILOT Law 2): safe/scratch targets and Jordan's OWN apps he
// authorizes ONLY. This tool does NOT lift the browser freeze - never point it at
// a browser. It clicks; it makes no judgement about WHETHER a click is safe.
package main

import (
	"fmt"
	"os"
	"strings"

	"becky-go/internal/beckyio"
)

func main() {
	o, asJSON, selftest, usageErr := parseArgs(os.Args[1:])

	if selftest {
		os.Exit(runSelftest())
	}
	if usageErr != "" {
		fmt.Fprintln(os.Stderr, "usage: becky-click --window <title-substr> --name <control name> [--control-type Button] [--verify] [--expect <text>] [--json]")
		fmt.Fprintln(os.Stderr, "becky-click:", usageErr)
		os.Exit(2)
	}

	tmp, err := os.MkdirTemp("", "becky-click-")
	if err != nil {
		beckyio.PrintJSON(Result{OK: false, Window: o.window, Name: o.name, Error: "cannot create temp dir: " + err.Error()})
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)

	d, err := newDeps(tmp)
	if err != nil {
		beckyio.PrintJSON(Result{OK: false, Window: o.window, Name: o.name, Error: err.Error()})
		os.Exit(1)
	}

	res := runClick(o, d)

	if !asJSON {
		printPlain(res)
	}
	beckyio.PrintJSON(res)
	if !res.OK {
		os.Exit(1)
	}
}

// parseArgs does a position-independent scan (Go's stdlib flag package stops at
// the first non-flag arg; the same bug cmd/notify, cmd/websearch and cmd/file all
// hit and fixed). Flags may appear in any order.
func parseArgs(args []string) (o options, asJSON, selftest bool, usageErr string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json", "-json":
			asJSON = true
		case "--selftest", "-selftest":
			selftest = true
		case "--verify", "-verify":
			o.verify = true
		case "--window", "-window", "--win":
			i = takeValue(args, i, &o.window, &usageErr, "--window")
		case "--name", "-name", "--control", "--control-name":
			i = takeValue(args, i, &o.name, &usageErr, "--name")
		case "--control-type", "-control-type", "--type", "--role":
			i = takeValue(args, i, &o.controlType, &usageErr, "--control-type")
		case "--expect", "-expect":
			i = takeValue(args, i, &o.expect, &usageErr, "--expect")
		default:
			if strings.HasPrefix(a, "-") {
				usageErr = "unknown flag: " + a
			} else {
				usageErr = "unexpected argument: " + a + " (use --window/--name)"
			}
		}
	}
	if !selftest && usageErr == "" {
		if strings.TrimSpace(o.window) == "" {
			usageErr = "--window is required"
		} else if strings.TrimSpace(o.name) == "" {
			usageErr = "--name is required"
		}
	}
	return o, asJSON, selftest, usageErr
}

// takeValue reads the value following a flag at index i, stores it, and returns
// the advanced index. A missing value sets usageErr.
func takeValue(args []string, i int, dst *string, usageErr *string, flag string) int {
	if i+1 >= len(args) {
		*usageErr = flag + " wants a value"
		return i
	}
	*dst = args[i+1]
	return i + 1
}

// printPlain writes a short human summary to stderr; stdout always carries JSON.
func printPlain(res Result) {
	if !res.OK {
		fmt.Fprintln(os.Stderr, "becky-click:", res.Error)
		return
	}
	fmt.Fprintf(os.Stderr, "clicked %q in %q via %s\n", res.Name, res.Window, res.Method)
	if res.VerifyText != "" || res.Verified {
		status := "NOT confirmed"
		if res.Verified {
			status = "confirmed by becky-ocr"
		}
		fmt.Fprintln(os.Stderr, "verify:", status)
	}
}
