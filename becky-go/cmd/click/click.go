// click.go - the pure, testable core of becky-click: the locate-then-click
// orchestration (UIA first, pywinauto win32 fallback), and the becky-ocr
// verify decision. Every backend the orchestration calls is a function field on
// deps, so the whole flow can be exercised OFFLINE with fakes (selftest + unit
// tests) with no real window, no PowerShell, no python, no GPU - which is what
// keeps this green on the ubuntu CI the repo builds on. The real backends live
// in backends.go; main.go is only flag parsing + wiring.
//
// Design note (why locate-then-click, not one call): --verify needs a BEFORE
// screenshot, which must happen before the click. So we always locate first
// (which also decides the method + returns the window rect), optionally shoot
// the before frame, then click via the same backend, optionally shoot the after
// frame, then OCR-compare. The double find is cheap and keeps one clean flow for
// both backends. ponytail: a window that MOVES between locate and click would
// shift the rect; negligible for the safe scratch/authorized-app targets this is
// scoped to - revisit only if a real target proves flaky.
package main

import (
	"path/filepath"
	"strings"
)

// rect is a screen rectangle in pixels (the whole target window, so state labels
// around the clicked control are captured for OCR).
type rect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// located is what a locate backend returns.
type located struct {
	found  bool
	rect   rect
	method string // "uia" | "win32"
	err    string
}

// deps holds every side-effecting backend as an injectable function, so the
// orchestration is pure and offline-testable. Real wiring is in backends.go.
type deps struct {
	locateUIA   func(window, name, controlType string) located
	locateWin32 func(window, name, controlType string) located
	clickUIA    func(window, name, controlType string) (clicked bool, errStr string)
	clickWin32  func(window, name, controlType string) (clicked bool, errStr string)
	shot        func(r rect, outPath string) error
	ocr         func(pngPath string) (text string, err error)
	// settle pauses briefly after the click so the target can repaint before the
	// after-frame is captured (WPF/Win32 repaint is async - the proven probe
	// waited ~400ms). Real deps sleep; tests/selftest inject a no-op so they stay
	// instant. ponytail: a real UI needs settle time a pure model can't see - this
	// is the calibration knob, not just less code.
	settle func()
	tmpDir string
}

// options carries the parsed flags into runClick().
type options struct {
	window      string
	name        string
	controlType string
	expect      string
	verify      bool
}

// Result is becky-click's stdout JSON envelope. The card's required minimum is
// {ok, clicked, method, verified} / {ok:false, error}; window/name/found/
// verify_text are extra context and never load-bearing.
type Result struct {
	OK         bool   `json:"ok"`
	Window     string `json:"window,omitempty"`
	Name       string `json:"name,omitempty"`
	Found      bool   `json:"found"`
	Clicked    bool   `json:"clicked"`
	Method     string `json:"method,omitempty"`
	Verified   bool   `json:"verified"`
	VerifyText string `json:"verify_text,omitempty"`
	Error      string `json:"error,omitempty"`
}

// runClick is the single entry point main() calls and the one selftest/unit
// tests drive directly with fake deps.
func runClick(o options, d deps) Result {
	res := Result{Window: o.window, Name: o.name}

	// 1. locate: UIA (the winner for modern/WPF/UWP/Chromium) first, then the
	//    pywinauto win32 backend for classic Win32/Notepad-class Panes.
	loc := d.locateUIA(o.window, o.name, o.controlType)
	if !loc.found {
		loc2 := d.locateWin32(o.window, o.name, o.controlType)
		if loc2.found {
			loc = loc2
		} else {
			res.OK = false
			res.Error = combineErr("control not found by name (tried UIA then win32)", loc.err, loc2.err)
			return res
		}
	}
	res.Found = true
	res.Method = loc.method

	// 2. before-frame (only when verifying and we can screenshot)
	var beforeText string
	if o.verify {
		bp := filepath.Join(d.tmpDir, "before.png")
		if err := d.shot(loc.rect, bp); err == nil {
			beforeText, _ = d.ocr(bp)
		}
	}

	// 3. click via the SAME backend that located it
	var clicked bool
	var cerr string
	if loc.method == "win32" {
		clicked, cerr = d.clickWin32(o.window, o.name, o.controlType)
	} else {
		clicked, cerr = d.clickUIA(o.window, o.name, o.controlType)
	}
	if !clicked {
		res.OK = false
		res.Error = nonEmpty(cerr, "found the control but the click did not register")
		return res
	}
	res.OK = true
	res.Clicked = true

	// 4. verify: after-frame + becky-ocr render check
	if o.verify {
		if d.settle != nil {
			d.settle() // let the target repaint before capturing the after-frame
		}
		ap := filepath.Join(d.tmpDir, "after.png")
		if err := d.shot(loc.rect, ap); err != nil {
			res.VerifyText = "verify skipped: screenshot failed: " + err.Error()
			return res
		}
		afterText, oerr := d.ocr(ap)
		if oerr != nil {
			res.VerifyText = "verify skipped: ocr failed: " + oerr.Error()
			return res
		}
		res.Verified = verifyDecision(beforeText, afterText, o.expect)
		res.VerifyText = snippet(afterText)
	}
	return res
}

// verifyDecision is the becky-ocr render check. With --expect, the render is
// verified when the expected text is present in the post-click OCR (the
// deterministic "the expected state appeared" check). Without --expect, it is
// verified when the rendered text actually CHANGED after the click (a genuine
// repaint), guarding against "clicked but nothing happened".
//
// ponytail: matches against the raw becky-ocr stdout (OCR'd words appear as JSON
// string values, so both contains and change-detection work without coupling to
// becky-ocr's exact schema). Upgrade to parsing .lines[].text only if raw
// matching ever yields a false positive.
func verifyDecision(before, after, expect string) bool {
	a := strings.ToLower(after)
	if strings.TrimSpace(expect) != "" {
		return strings.Contains(a, strings.ToLower(strings.TrimSpace(expect)))
	}
	return strings.TrimSpace(after) != "" && normalizeWS(before) != normalizeWS(after)
}

// normalizeWS lowercases and collapses runs of whitespace so trivial OCR spacing
// jitter does not by itself read as a state change.
func normalizeWS(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// snippet trims OCR text for the JSON envelope so it stays readable.
func snippet(s string) string {
	s = strings.TrimSpace(s)
	const max = 240
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

// combineErr joins the lead message with any non-empty backend errors for a
// single honest explanation of why nothing was found.
func combineErr(lead string, errs ...string) string {
	parts := []string{lead}
	for _, e := range errs {
		if strings.TrimSpace(e) != "" {
			parts = append(parts, e)
		}
	}
	return strings.Join(parts, "; ")
}
