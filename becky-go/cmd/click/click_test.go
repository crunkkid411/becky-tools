package main

import "testing"

// fakes reused across tests.
func locHit(method string) func(_, _, _ string) located {
	return func(_, _, _ string) located { return located{found: true, method: method, rect: rect{0, 0, 10, 10}} }
}
func locMiss(method, err string) func(_, _, _ string) located {
	return func(_, _, _ string) located { return located{found: false, method: method, err: err} }
}
func clk(ok bool, err string) func(_, _, _ string) (bool, string) {
	return func(_, _, _ string) (bool, string) { return ok, err }
}
func noShot(_ rect, _ string) error  { return nil }
func noOCR(_ string) (string, error) { return "", nil }
func fixedOCR(s string) func(string) (string, error) {
	return func(string) (string, error) { return s, nil }
}

func TestUIAWins(t *testing.T) {
	r := runClick(options{window: "w", name: "n"}, deps{
		locateUIA: locHit("uia"), locateWin32: locMiss("win32", "x"),
		clickUIA: clk(true, ""), clickWin32: clk(false, ""), shot: noShot, ocr: noOCR,
	})
	if !r.OK || !r.Clicked || r.Method != "uia" {
		t.Fatalf("expected uia click ok, got %+v", r)
	}
}

func TestWin32Fallback(t *testing.T) {
	r := runClick(options{window: "w", name: "n"}, deps{
		locateUIA: locMiss("uia", "no invokable control"), locateWin32: locHit("win32"),
		clickUIA: clk(false, ""), clickWin32: clk(true, ""), shot: noShot, ocr: noOCR,
	})
	if !r.OK || r.Method != "win32" {
		t.Fatalf("expected win32 fallback, got %+v", r)
	}
}

func TestNotFound(t *testing.T) {
	r := runClick(options{window: "w", name: "n"}, deps{
		locateUIA: locMiss("uia", "a"), locateWin32: locMiss("win32", "b"),
		clickUIA: clk(false, ""), clickWin32: clk(false, ""), shot: noShot, ocr: noOCR,
	})
	if r.OK || r.Found || r.Error == "" {
		t.Fatalf("expected not-found failure, got %+v", r)
	}
}

func TestFoundButClickFails(t *testing.T) {
	r := runClick(options{window: "w", name: "n"}, deps{
		locateUIA: locHit("uia"), locateWin32: locMiss("win32", ""),
		clickUIA: clk(false, "invoke failed"), clickWin32: clk(false, ""), shot: noShot, ocr: noOCR,
	})
	if r.OK || !r.Found || r.Clicked {
		t.Fatalf("expected found-but-not-clicked, got %+v", r)
	}
}

func TestVerifyExpectPresent(t *testing.T) {
	r := runClick(options{window: "w", name: "n", verify: true, expect: "clicked"}, deps{
		locateUIA: locHit("uia"), locateWin32: locMiss("win32", ""),
		clickUIA: clk(true, ""), clickWin32: clk(false, ""), shot: noShot,
		ocr: fixedOCR(`{"lines":[{"text":"CLICKED_OK"}]}`),
	})
	if !r.OK || !r.Verified {
		t.Fatalf("expected verified true, got %+v", r)
	}
}

func TestVerifyExpectAbsent(t *testing.T) {
	r := runClick(options{window: "w", name: "n", verify: true, expect: "saved"}, deps{
		locateUIA: locHit("uia"), locateWin32: locMiss("win32", ""),
		clickUIA: clk(true, ""), clickWin32: clk(false, ""), shot: noShot,
		ocr: fixedOCR(`{"lines":[{"text":"CLICKED_OK"}]}`),
	})
	if !r.OK || r.Verified {
		t.Fatalf("expected verified false (expect absent), got %+v", r)
	}
}

func TestVerifyChangeDetection(t *testing.T) {
	seq := makeSeqOCR("NOT_CLICKED", "CLICKED_OK")
	r := runClick(options{window: "w", name: "n", verify: true}, deps{
		locateUIA: locHit("uia"), locateWin32: locMiss("win32", ""),
		clickUIA: clk(true, ""), clickWin32: clk(false, ""), shot: noShot, ocr: seq,
	})
	if !r.OK || !r.Verified {
		t.Fatalf("expected verified true on render change, got %+v", r)
	}
}

func TestVerifyNoChange(t *testing.T) {
	seq := makeSeqOCR("SAME", "SAME")
	r := runClick(options{window: "w", name: "n", verify: true}, deps{
		locateUIA: locHit("uia"), locateWin32: locMiss("win32", ""),
		clickUIA: clk(true, ""), clickWin32: clk(false, ""), shot: noShot, ocr: seq,
	})
	if !r.OK || r.Verified {
		t.Fatalf("expected verified false on no render change, got %+v", r)
	}
}

func TestVerifyDecisionTable(t *testing.T) {
	cases := []struct {
		before, after, expect string
		want                  bool
	}{
		{"NOT_CLICKED", "CLICKED_OK", "clicked", true},
		{"NOT_CLICKED", "CLICKED_OK", "SAVED", false},
		{"a", "b", "", true},
		{"same", "same", "", false},
		{"x", "", "", false}, // empty after is never a valid render
	}
	for _, c := range cases {
		if got := verifyDecision(c.before, c.after, c.expect); got != c.want {
			t.Errorf("verifyDecision(%q,%q,%q)=%v want %v", c.before, c.after, c.expect, got, c.want)
		}
	}
}

func TestParseArgsPositionIndependent(t *testing.T) {
	o, asJSON, st, uerr := parseArgs([]string{"--window", "Notepad", "--json", "--name", "Save", "--control-type", "Button", "--verify", "--expect", "Saved"})
	if uerr != "" || !asJSON || st {
		t.Fatalf("unexpected: uerr=%q asJSON=%v st=%v", uerr, asJSON, st)
	}
	if o.window != "Notepad" || o.name != "Save" || o.controlType != "Button" || !o.verify || o.expect != "Saved" {
		t.Fatalf("bad parse: %+v", o)
	}
}

func TestParseArgsRequiresWindowAndName(t *testing.T) {
	if _, _, _, uerr := parseArgs([]string{"--name", "Save"}); uerr == "" {
		t.Fatal("expected usage error for missing --window")
	}
	if _, _, _, uerr := parseArgs([]string{"--window", "W"}); uerr == "" {
		t.Fatal("expected usage error for missing --name")
	}
	if _, _, _, uerr := parseArgs([]string{"--bogus"}); uerr == "" {
		t.Fatal("expected usage error for unknown flag")
	}
}

func TestLastJSONObject(t *testing.T) {
	out := []byte("WARNING: something\r\n{\"ok\":true,\"found\":true,\"method\":\"uia\",\"clicked\":true,\"rect\":{\"x\":1,\"y\":2,\"w\":3,\"h\":4}}\r\n")
	o, err := lastJSONObject(out)
	if err != nil || o == nil || !o.OK || !o.Clicked || o.Rect == nil || o.Rect.W != 3 {
		t.Fatalf("bad parse: obj=%+v err=%v", o, err)
	}
}
