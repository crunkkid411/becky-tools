// selftest.go - becky-click's one-command, OFFLINE proof of the real decision
// paths: the locate-then-click orchestration (UIA-first, win32 fallback,
// not-found envelope) and the becky-ocr verify decision. It uses FAKE backends
// (no real window, no PowerShell, no python, no GPU) so it is the provable-
// handoff gate that runs identically on this machine and on CI. The live
// GUI proof (a real scratch window, method + OCR confirmation) is a separate
// step in the WORKLOG, not something a hermetic selftest can assert.
package main

import "fmt"

func runSelftest() int {
	type check struct {
		name string
		ok   bool
	}
	var checks []check
	add := func(name string, ok bool) { checks = append(checks, check{name, ok}) }

	// --- fake backends -------------------------------------------------------
	uiaHit := func(_, _, _ string) located {
		return located{found: true, method: "uia", rect: rect{0, 0, 100, 50}}
	}
	uiaMiss := func(_, _, _ string) located {
		return located{found: false, method: "uia", err: "no invokable UIA control matched name"}
	}
	win32Hit := func(_, _, _ string) located {
		return located{found: true, method: "win32", rect: rect{0, 0, 100, 50}}
	}
	win32Miss := func(_, _, _ string) located {
		return located{found: false, method: "win32", err: "control not found by name"}
	}
	clickOK := func(_, _, _ string) (bool, string) { return true, "" }
	clickFail := func(_, _, _ string) (bool, string) { return false, "invoke failed" }
	okShot := func(_ rect, _ string) error { return nil }
	// OCR fakes: "before" reads NOT_CLICKED, "after" reads CLICKED_OK.
	ocrAfter := func(_ string) (string, error) { return `{"lines":[{"text":"CLICKED_OK"}]}`, nil }

	// UIA winner path
	r := runClick(options{window: "W", name: "N"}, deps{
		locateUIA: uiaHit, locateWin32: win32Miss, clickUIA: clickOK, clickWin32: clickFail, shot: okShot, ocr: ocrAfter,
	})
	add("UIA-native control clicks via the UIA backend", r.OK && r.Clicked && r.Method == "uia")

	// win32 fallback path (UIA misses, win32 hits)
	r = runClick(options{window: "W", name: "N"}, deps{
		locateUIA: uiaMiss, locateWin32: win32Hit, clickUIA: clickFail, clickWin32: clickOK, shot: okShot, ocr: ocrAfter,
	})
	add("classic Win32 control falls back to the win32 backend", r.OK && r.Clicked && r.Method == "win32")

	// neither backend finds it -> ok:false, not found, no crash
	r = runClick(options{window: "W", name: "N"}, deps{
		locateUIA: uiaMiss, locateWin32: win32Miss, clickUIA: clickFail, clickWin32: clickFail, shot: okShot, ocr: ocrAfter,
	})
	add("control not found -> ok:false with an honest error", !r.OK && !r.Found && r.Error != "")

	// found but the click does not register -> ok:false, found:true
	r = runClick(options{window: "W", name: "N"}, deps{
		locateUIA: uiaHit, locateWin32: win32Miss, clickUIA: clickFail, clickWin32: clickFail, shot: okShot, ocr: ocrAfter,
	})
	add("found but click did not register -> ok:false, found:true", !r.OK && r.Found && !r.Clicked)

	// verify with --expect present in the post-click OCR -> verified true
	r = runClick(options{window: "W", name: "N", verify: true, expect: "CLICKED"}, deps{
		locateUIA: uiaHit, locateWin32: win32Miss, clickUIA: clickOK, clickWin32: clickFail, shot: okShot, ocr: ocrAfter,
	})
	add("verify --expect matches post-click OCR -> verified", r.OK && r.Verified)

	// verify with --expect ABSENT from OCR -> verified false (honest)
	r = runClick(options{window: "W", name: "N", verify: true, expect: "SAVED"}, deps{
		locateUIA: uiaHit, locateWin32: win32Miss, clickUIA: clickOK, clickWin32: clickFail, shot: okShot, ocr: ocrAfter,
	})
	add("verify --expect absent from OCR -> NOT verified", r.OK && r.Clicked && !r.Verified)

	// verify change-detection: before != after -> verified true (no --expect)
	beforeThenAfter := makeSeqOCR(`{"lines":[{"text":"NOT_CLICKED"}]}`, `{"lines":[{"text":"CLICKED_OK"}]}`)
	r = runClick(options{window: "W", name: "N", verify: true}, deps{
		locateUIA: uiaHit, locateWin32: win32Miss, clickUIA: clickOK, clickWin32: clickFail, shot: okShot, ocr: beforeThenAfter,
	})
	add("verify (no --expect) sees the render change -> verified", r.OK && r.Verified)

	// verify change-detection: before == after -> verified false
	sameTwice := makeSeqOCR(`{"lines":[{"text":"NOT_CLICKED"}]}`, `{"lines":[{"text":"NOT_CLICKED"}]}`)
	r = runClick(options{window: "W", name: "N", verify: true}, deps{
		locateUIA: uiaHit, locateWin32: win32Miss, clickUIA: clickOK, clickWin32: clickFail, shot: okShot, ocr: sameTwice,
	})
	add("verify (no --expect) with no render change -> NOT verified", r.OK && r.Clicked && !r.Verified)

	// verifyDecision unit checks
	add("verifyDecision: expect present", verifyDecision("NOT_CLICKED", "CLICKED_OK", "clicked"))
	add("verifyDecision: expect absent", !verifyDecision("NOT_CLICKED", "CLICKED_OK", "saved"))
	add("verifyDecision: change without expect", verifyDecision("a", "b", ""))
	add("verifyDecision: no change without expect", !verifyDecision("same", "same", ""))

	// arg parsing: position-independent flags
	o, asJSON, st, uerr := parseArgs([]string{"--window", "Notepad", "--json", "--name", "Save", "--verify"})
	add("parseArgs: flags in any order (--json before --name)", uerr == "" && asJSON && !st && o.window == "Notepad" && o.name == "Save" && o.verify)

	o, _, _, uerr = parseArgs([]string{"--name", "Save"})
	add("parseArgs: missing --window is a usage error", uerr != "" && o.window == "")

	_, _, st, uerr = parseArgs([]string{"--selftest"})
	add("parseArgs: --selftest needs no window/name", st && uerr == "")

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
		fmt.Printf("becky-click selftest: PASS (%d/%d checks)\n", len(checks), len(checks))
		return 0
	}
	fmt.Printf("becky-click selftest: FAIL (%d/%d checks failed)\n", failed, len(checks))
	return 1
}

// makeSeqOCR returns an ocr fake that yields the given texts on successive calls
// (first call = before-frame, second = after-frame), so change-detection can be
// proven offline.
func makeSeqOCR(texts ...string) func(string) (string, error) {
	i := 0
	return func(_ string) (string, error) {
		t := texts[len(texts)-1]
		if i < len(texts) {
			t = texts[i]
		}
		i++
		return t, nil
	}
}
