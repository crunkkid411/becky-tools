package main

// bridge_test.go covers the JS↔Go control surface (beckyCall): the default-deny
// dispatch table, the {ok,data,error} envelope, the arg coercers, and the pure
// time/path helpers. Pure data-in/data-out — no window. Synthetic fixtures.

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// callEnv runs App.Call and decodes the envelope for assertions.
func callEnv(t *testing.T, app *App, verb, argsJSON string) callReply {
	t.Helper()
	var r callReply
	if err := json.Unmarshal([]byte(app.Call(verb, argsJSON)), &r); err != nil {
		t.Fatalf("decode reply for %s: %v", verb, err)
	}
	return r
}

func TestCallUnknownVerbIsRejected(t *testing.T) {
	app := NewApp()
	r := callEnv(t, app, "rm_-rf", `{}`)
	if r.OK {
		t.Error("unknown verb must be rejected (default-deny)")
	}
	if r.Error == "" {
		t.Error("rejection should carry a message")
	}
}

func TestCallBadArgsIsRejected(t *testing.T) {
	app := NewApp()
	r := callEnv(t, app, "search", `{not json`)
	if r.OK {
		t.Error("malformed args must be rejected, not panic")
	}
}

func TestCallOpenFolderAndSearch(t *testing.T) {
	app := NewApp()
	app.workDir = t.TempDir()
	dir := fixtureFolder(t)

	r := callEnv(t, app, "open_folder", `{"folder":`+jsonStr(dir)+`}`)
	if !r.OK {
		t.Fatalf("open_folder failed: %s", r.Error)
	}

	r = callEnv(t, app, "search", `{"query":"money"}`)
	if !r.OK {
		t.Fatalf("search failed: %s", r.Error)
	}
	// data is a []SearchResult — round-trip through JSON to count.
	var hits []SearchResult
	remarshal(t, r.Data, &hits)
	if len(hits) == 0 {
		t.Error("expected search hits via the bridge")
	}
}

func TestCallAddAndTimelineFlow(t *testing.T) {
	app := NewApp()
	app.workDir = t.TempDir()
	dir := fixtureFolder(t)
	callEnv(t, app, "open_folder", `{"folder":`+jsonStr(dir)+`}`)

	ring := filepath.Join(dir, "ring.mp4")
	r := callEnv(t, app, "add_clip", `{"source":`+jsonStr(ring)+`,"in":1,"out":3,"label":"x"}`)
	if !r.OK {
		t.Fatalf("add_clip failed: %s", r.Error)
	}
	var tl TimelineView
	remarshal(t, r.Data, &tl)
	if len(tl.Clips) != 1 {
		t.Fatalf("want 1 clip after add, got %d", len(tl.Clips))
	}

	// overlay toggle through the bridge.
	r = callEnv(t, app, "set_overlay", `{"field":"enabled","value":true}`)
	remarshal(t, r.Data, &tl)
	if !tl.Overlay.Enabled {
		t.Error("set_overlay enabled did not stick")
	}

	// remove through the bridge.
	r = callEnv(t, app, "remove_clip", `{"id":"c1"}`)
	remarshal(t, r.Data, &tl)
	if len(tl.Clips) != 0 {
		t.Errorf("want 0 clips after remove, got %d", len(tl.Clips))
	}
}

func TestCallExportEmptyTimelineErrors(t *testing.T) {
	app := NewApp()
	app.workDir = t.TempDir()
	r := callEnv(t, app, "export", `{}`)
	if r.OK {
		t.Error("export on an empty timeline should fail with a clear message")
	}
}

// ---- arg coercers ----

func TestArgCoercers(t *testing.T) {
	m := map[string]any{
		"s": "hi", "n": float64(42), "f": 1.5, "b": true, "bs": "yes", "ns": "7",
	}
	if argString(m, "s") != "hi" {
		t.Error("argString string")
	}
	if argString(m, "n") != "42" {
		t.Error("argString int-from-float should not be scientific")
	}
	if argFloat(m, "f") != 1.5 {
		t.Error("argFloat number")
	}
	if argFloat(m, "ns") != 7 {
		t.Error("argFloat numeric string")
	}
	if argInt(m, "n") != 42 {
		t.Error("argInt number")
	}
	if !argBool(m, "b") || !argBool(m, "bs") {
		t.Error("argBool bool/truthy-string")
	}
	if argBool(m, "missing") {
		t.Error("missing bool should be false")
	}
}

// ---- pure helpers ----

func TestTcOrSeconds(t *testing.T) {
	cases := map[string]float64{
		"":             0,
		"12.4":         12.4,
		"0:30":         30,
		"1:02":         62,
		"00:00:12,400": 12.4,
		"00:00:12.400": 12.4,
		"01:02:03":     3723,
		"-5":           0, // clamped
	}
	for in, want := range cases {
		if got := tcOrSeconds(in); got != want {
			t.Errorf("tcOrSeconds(%q)=%v want %v", in, got, want)
		}
	}
}

func TestMmssAndSlug(t *testing.T) {
	if mmss(0) != "0:00" || mmss(65) != "1:05" || mmss(-3) != "0:00" {
		t.Errorf("mmss wrong: %q %q %q", mmss(0), mmss(65), mmss(-3))
	}
	if slugName("Case File #3!") != "case-file-3" {
		t.Errorf("slugName wrong: %q", slugName("Case File #3!"))
	}
	if slugName("") != "becky" {
		t.Errorf("slugName empty want becky, got %q", slugName(""))
	}
}

func TestTruthy(t *testing.T) {
	for _, s := range []string{"true", "1", "yes", "on", "Y"} {
		if !truthy(s) {
			t.Errorf("truthy(%q) should be true", s)
		}
	}
	for _, s := range []string{"", "false", "0", "no", "maybe"} {
		if truthy(s) {
			t.Errorf("truthy(%q) should be false", s)
		}
	}
}

// ---- test helpers ----

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func remarshal(t *testing.T, v any, dst any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}
