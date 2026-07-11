package main

import "testing"

// TestClassify pins the stall-detection decision against the four fixture cases
// (and the traps between them) using canned model answers — no GPU, no model.
func TestClassify(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		state string
	}{
		// --- labelled answers (the model followed the one-word format) ---
		{"perm prompt labelled", "WAITING\nUse skill \"claude-in-chrome\"? Do you want to proceed? 1. Yes 3. No", StateWaitingInput},
		{"error dialog labelled", "ERROR\nMissionControl.exe has stopped working. OK", StateErrorDialog},
		{"idle terminal labelled", "IDLE\nA completed build log then a bare command prompt.", StateIdle},
		{"empty desktop labelled", "IDLE\nAn empty desktop with a taskbar, no windows.", StateIdle},
		{"active labelled", "ACTIVE\nA text editor with a document open, nothing waiting.", StateActive},
		{"label tolerates dash+markdown", "**ERROR** - a crash dialog is shown", StateErrorDialog},

		// --- keyword fallback (model ignored the label format) ---
		{"fallback proceed prompt", "The screen shows a prompt asking 'Do you want to proceed?' with 1. Yes and 3. No.", StateWaitingInput},
		{"fallback stopped working", "A message box says the application has stopped working.", StateErrorDialog},
		{"fallback empty desktop", "Just an empty desktop with wallpaper and an empty taskbar.", StateIdle},

		// --- the trap: a normal shell prompt 'waits' but is NOT a stall ---
		// ("ready for a command" is an idle signal; idle and active are both not-stalled)
		{"bare shell prompt not a stall", "A terminal sitting at a command prompt C:\\> ready for a command.", StateIdle},

		// --- error precedence over the OK-button wait signal ---
		{"error box with OK button -> error, not waiting", "An error dialog: a problem caused the app to close. Click OK.", StateErrorDialog},
		// the tiny model mislabels a crash box as WAITING (it waits for OK); the
		// unambiguous error body must still resolve to error_dialog (fixture 2).
		{"WAITING label but crash body -> error_dialog", "WAITING\nMissionControl.exe has stopped working. A problem caused the application to close. OK", StateErrorDialog},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			state, conf, reason := Classify(c.text)
			if state != c.state {
				t.Fatalf("Classify(%q) state = %q, want %q", c.text, state, c.state)
			}
			if conf <= 0 || conf > 1 {
				t.Fatalf("confidence out of range: %v", conf)
			}
			if reason == "" {
				t.Fatalf("empty reason")
			}
		})
	}
}

// TestStalledContract: stalled is exactly the two blocking states.
func TestStalledContract(t *testing.T) {
	for state, want := range map[string]bool{
		StateWaitingInput: true,
		StateErrorDialog:  true,
		StateIdle:         false,
		StateActive:       false,
		StateUnknown:      false,
	} {
		v := Verdict{State: state, Stalled: state == StateWaitingInput || state == StateErrorDialog}
		if v.Stalled != want {
			t.Errorf("state %q stalled=%v, want %v", state, v.Stalled, want)
		}
	}
}

// TestParseArgs covers position-independent flags and the --json-after-positional
// bug that Go's stdlib flag package has (the reason parseArgs is hand-rolled).
func TestParseArgs(t *testing.T) {
	o, asJSON, _, e := parseArgs([]string{"shot.png", "--json"})
	if e != "" || o.Image != "shot.png" || !asJSON {
		t.Fatalf("positional+--json: img=%q json=%v err=%q", o.Image, asJSON, e)
	}
	o, _, _, e = parseArgs([]string{"--image", "a.png", "--prompt", "look", "--dir", "d", "--bin", "b"})
	if e != "" || o.Image != "a.png" || o.Prompt != "look" || o.ModelDir != "d" || o.Bin != "b" {
		t.Fatalf("flagged form parsed wrong: %+v err=%q", o, e)
	}
	if _, _, _, e := parseArgs([]string{}); e == "" {
		t.Fatalf("no args should be a usage error")
	}
	if o, _, _, e := parseArgs([]string{"--capture"}); e != "" || !o.Capture {
		t.Fatalf("--capture alone should be valid: cap=%v err=%q", o.Capture, e)
	}
	if _, _, _, e := parseArgs([]string{"--bogus"}); e == "" {
		t.Fatalf("unknown flag should error")
	}
	if _, _, s, _ := parseArgs([]string{"--selftest"}); !s {
		t.Fatalf("--selftest not recognized")
	}
}
