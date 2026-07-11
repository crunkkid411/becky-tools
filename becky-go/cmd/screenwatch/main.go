// becky-screenwatch — is this screen STALLED on something a text watchdog can't
// see? One dumb call: hand it a screenshot (or let it grab the screen), get back
// a JSON verdict — {stalled, state, reason, confidence} — for the GUI/browser
// surfaces MissionControl's terminal.cpp text watchdog is blind to: a permission
// or consent dialog, a modal waiting for input, a "Do you want to proceed?"
// prompt, a frozen browser dialog, an error/crash box.
//
//	becky-screenwatch --image screen.png [--json]
//	becky-screenwatch --capture [--json]          # grab the primary display first (Windows)
//	becky-screenwatch --selftest                  # offline, no-model proof of the classifier
//
// It REUSES the winning becky-vision config proven in
// hj-mission-control\docs\RECOVERY.md ("becky-vision gate results", Test 5): the
// 1.6B LFM2.5-VL model called directly with a pointed prompt — one fast call, no
// escalation chain. It does NOT reinvent the vision ladder; it is a thin wrapper
// that bakes in that config and classifies the answer.
//
// becky-shaped: OFFLINE (one local model call), DETERMINISTIC (temp 0 → same
// image → same verdict), DEGRADE-NEVER-CRASH (a missing model/binary/image or a
// failed capture yields degraded:true + exit 0, never a panic — a watchdog must
// not be crashed by its own eyes).
//
// Exit codes: 0 = ran (incl. a clean degrade or a passing selftest); 1 =
// unexpected error / selftest failure; 2 = usage.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"becky-go/internal/vision"
)

func main() {
	opts, asJSON, selftest, usageErr := parseArgs(os.Args[1:])
	if selftest {
		os.Exit(runSelftest())
	}
	if usageErr != "" {
		fmt.Fprintln(os.Stderr, usageErr)
		fmt.Fprintln(os.Stderr, `usage: becky-screenwatch --image <screen.png> [--json]`)
		fmt.Fprintln(os.Stderr, `   or: becky-screenwatch --capture [--json]      (grab the primary display, Windows)`)
		os.Exit(2)
	}

	v := Watch(opts)
	emit(v, asJSON)
}

// parseArgs does POSITION-INDEPENDENT flag parsing by hand. Go's stdlib flag
// package stops at the first non-flag arg, which becky-notify hit as a real bug
// (`becky-notify "text" --json` wrongly rejected). We avoid it entirely: scan all
// args, accept flags in any order, treat a lone positional as the image path.
func parseArgs(args []string) (opts Options, asJSON, selftest bool, usageErr string) {
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json", "-json":
			asJSON = true
		case "--selftest", "-selftest":
			selftest = true
		case "--capture", "-capture":
			opts.Capture = true
		case "--keep-shot", "-keep-shot":
			opts.KeepShot = true
		case "--image", "-image":
			if i+1 < len(args) {
				i++
				opts.Image = args[i]
			}
		case "--dir", "-dir":
			if i+1 < len(args) {
				i++
				opts.ModelDir = args[i]
			}
		case "--prompt", "-prompt":
			if i+1 < len(args) {
				i++
				opts.Prompt = args[i]
			}
		case "--bin", "-bin":
			if i+1 < len(args) {
				i++
				opts.Bin = args[i]
			}
		case "-h", "--help", "help":
			usageErr = "becky-screenwatch: decide whether a screen is stalled on a dialog/prompt a text watchdog can't see"
			return
		default:
			if strings.HasPrefix(a, "-") {
				usageErr = "unknown flag: " + a
				return
			}
			positional = append(positional, a)
		}
	}
	// A bare positional is the image path (so `becky-screenwatch screen.png` works).
	if opts.Image == "" && len(positional) == 1 {
		opts.Image = positional[0]
	} else if len(positional) > 1 {
		usageErr = "too many arguments; expected one image path"
		return
	}
	if opts.Image == "" && !opts.Capture {
		usageErr = "need --image <path> or --capture"
	}
	return
}

func emit(v Verdict, asJSON bool) {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			fmt.Fprintln(os.Stderr, "encode:", err)
			os.Exit(1)
		}
		return
	}
	printReport(v)
}

// printReport is the plain-language read for a non-developer: the bottom line
// first (STALLED / not), then what it saw.
func printReport(v Verdict) {
	if v.Degraded {
		fmt.Println("becky-screenwatch could not read the screen.")
		fmt.Println("  reason:", v.Error)
		fmt.Println("  (graceful degrade — a model/file/capture was missing, not a crash.)")
		return
	}
	if v.Stalled {
		fmt.Printf("STALLED — %s (confidence %.2f)\n", v.State, v.Confidence)
	} else {
		fmt.Printf("not stalled — %s (confidence %.2f)\n", v.State, v.Confidence)
	}
	if v.Reason != "" {
		fmt.Println("  saw:", v.Reason)
	}
}

// runSelftest is the one-command, OFFLINE, no-model proof of the real decision
// path: it feeds Classify canned model answers (the four fixture cases) and
// checks the verdict, plus the argv/flag parsing and the baked-in config. No GPU,
// no model, no llama binary required. becky's "provable handoff" gate.
func runSelftest() int {
	type check struct {
		name string
		ok   bool
	}

	// Classify cases (canned model answers mirroring the four testdata fixtures).
	permState, _, _ := Classify("WAITING\nUse skill \"claude-in-chrome\"? Do you want to proceed? 1. Yes 3. No")
	errState, _, _ := Classify("ERROR\nMissionControl.exe has stopped working. A problem caused the application to close. OK")
	// the tiny model often mislabels the crash box as WAITING (it waits for OK);
	// the unambiguous error body must still win (real fixture-2 behavior).
	errMislabeled, _, _ := Classify("WAITING\nMissionControl.exe has stopped working. A problem caused the application to close. OK")
	idleState, _, _ := Classify("IDLE\nA command prompt after a completed build, nothing waiting.")
	deskState, _, _ := Classify("IDLE\nAn empty desktop with a taskbar and wallpaper, no windows open.")
	// keyword fallback (no label line at all):
	kwPerm, _, _ := Classify("The screen shows a dialog asking 'Do you want to proceed?' with options 1. Yes and 3. No.")
	kwErr, _, _ := Classify("A message box: the application has stopped working.")
	// a bare shell prompt must NOT read as a stall even though it 'waits':
	bareState, _, _ := Classify("A terminal at a command prompt C:\\> ready for a command. Nothing is waiting.")

	// flag parsing (position independence + the --json-after-positional bug).
	o1, j1, _, e1 := parseArgs([]string{"shot.png", "--json"})
	o2, _, s2, _ := parseArgs([]string{"--selftest"})
	o3, _, _, e3 := parseArgs([]string{"--capture"})
	_, _, _, e4 := parseArgs([]string{}) // no args = usage error

	checks := []check{
		{"permission prompt -> waiting_input (stalled)", permState == StateWaitingInput},
		{"error dialog -> error_dialog (stalled)", errState == StateErrorDialog},
		{"crash body mislabeled WAITING -> error_dialog", errMislabeled == StateErrorDialog},
		{"idle terminal -> not stalled", idleState == StateIdle || idleState == StateActive},
		{"empty desktop -> idle (not stalled)", deskState == StateIdle},
		{"keyword fallback catches proceed? prompt", kwPerm == StateWaitingInput},
		{"keyword fallback catches 'stopped working'", kwErr == StateErrorDialog},
		{"bare shell prompt is NOT a stall", bareState != StateWaitingInput && bareState != StateErrorDialog},
		{"stalled == waiting_input|error_dialog", stalledFor(StateWaitingInput) && stalledFor(StateErrorDialog) && !stalledFor(StateIdle) && !stalledFor(StateActive)},
		{"--json parsed after a positional image", o1.Image == "shot.png" && j1},
		{"--selftest recognized", s2 && o2.Image == ""},
		{"--capture sets Capture with no image needed", o3.Capture && e3 == ""},
		{"no args -> usage error", e4 != ""},
		{"positional+json has no usage error", e1 == ""},
		{"baked-in config = the 1.6B winning dir", strings.Contains(DefaultModelDir, "1.6b")},
		{"vision reuse compiles (DefaultBin present)", vision.DefaultBin != ""},
	}

	failed := 0
	for _, c := range checks {
		status := "PASS"
		if !c.ok {
			status = "FAIL"
			failed++
		}
		fmt.Printf("[%s] %s\n", status, c.name)
	}
	fmt.Println()
	if failed == 0 {
		fmt.Printf("becky-screenwatch selftest: PASS (%d/%d checks)\n", len(checks), len(checks))
		return 0
	}
	fmt.Printf("becky-screenwatch selftest: FAIL (%d/%d checks failed)\n", failed, len(checks))
	return 1
}

// stalledFor mirrors Watch's stalled rule so the selftest pins the contract.
func stalledFor(state string) bool {
	return state == StateWaitingInput || state == StateErrorDialog
}
