package main

// bridge_test.go covers the JS↔Go control surface (beckyCall): the default-deny
// dispatch table, the {ok,data,error} envelope, the arg coercers, and the pure
// time/path helpers. Pure data-in/data-out — no window. Synthetic fixtures.

import (
	"encoding/json"
	"os"
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
	// I-2 wire-protocol fix (cycle 27): add_clip's reply is now a delta - ONLY the
	// one new clip, not the whole TimelineView (see bridge.go addClipReply). This
	// is the exact shape main.cpp's applyAddClipDelta consumes.
	var delta struct {
		Clip      ClipView `json:"clip"`
		Index     int      `json:"index"`
		ClipCount int      `json:"clip_count"`
	}
	remarshal(t, r.Data, &delta)
	if delta.Clip.ID == "" {
		t.Fatalf("add_clip delta missing the new clip")
	}
	if delta.ClipCount != 1 {
		t.Fatalf("want clip_count 1 after add, got %d", delta.ClipCount)
	}

	var tl TimelineView
	r = callEnv(t, app, "timeline", "")
	remarshal(t, r.Data, &tl)
	if len(tl.Clips) != 1 {
		t.Fatalf("want 1 clip after add (via timeline verb), got %d", len(tl.Clips))
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

// TestSortSearchByDate: search hits sort by file-name date NEWEST first; undated
// hits fall to the bottom; within a date, playable precedes transcript-only.
func TestSortSearchByDate(t *testing.T) {
	out := []SearchResult{
		{Name: "no-date.mp4", Date: "", Start: 1},
		{Name: "old.mp4", Date: "2026-06-20", Start: 1},
		{Name: "today-orphan.srt", Date: "2026-06-29", Start: 1, TranscriptOnly: true},
		{Name: "today.mp4", Date: "2026-06-29", Start: 5},
		{Name: "today.mp4", Date: "2026-06-29", Start: 2},
	}
	sortSearchByDate(out)
	gotOrder := []string{}
	for _, r := range out {
		gotOrder = append(gotOrder, r.Name)
	}
	// 2026-06-29 first (playable today.mp4 @2 then @5, then the orphan), then the
	// 2026-06-20 hit, then the undated one last.
	want := []string{"today.mp4", "today.mp4", "today-orphan.srt", "old.mp4", "no-date.mp4"}
	for i := range want {
		if gotOrder[i] != want[i] {
			t.Fatalf("sort order[%d] = %q, want %q (full: %v)", i, gotOrder[i], want[i], gotOrder)
		}
	}
	// And the two today.mp4 hits stay chronological (Start 2 before 5).
	if out[0].Start != 2 || out[1].Start != 5 {
		t.Fatalf("same-file hits should be chronological, got %.0f then %.0f", out[0].Start, out[1].Start)
	}
}

// TestNextSequencedPath: a re-export must never overwrite — each call returns the
// next free <base>_NNNN.mp4 (0001, then 0002 once 0001 exists, ...).
func TestNextSequencedPath(t *testing.T) {
	dir := t.TempDir()
	first := nextSequencedPath(dir, "case_reel", ".mp4")
	if filepath.Base(first) != "case_reel_0001.mp4" {
		t.Fatalf("first export should be _0001, got %q", filepath.Base(first))
	}
	if err := os.WriteFile(first, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := nextSequencedPath(dir, "case_reel", ".mp4")
	if filepath.Base(second) != "case_reel_0002.mp4" {
		t.Fatalf("with _0001 present, next must be _0002, got %q", filepath.Base(second))
	}
	if err := os.WriteFile(second, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if third := nextSequencedPath(dir, "case_reel", ".mp4"); filepath.Base(third) != "case_reel_0003.mp4" {
		t.Fatalf("with _0001+_0002 present, next must be _0003, got %q", filepath.Base(third))
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

func TestTruncateText(t *testing.T) {
	if got := truncateText("short", 80); got != "short" {
		t.Errorf("short string should pass through unchanged, got %q", got)
	}
	if got := truncateText("abcdef", 3); got != "abc…" {
		t.Errorf("truncateText(6 runes, max 3) = %q, want %q", got, "abc…")
	}
	if got := truncateText("abc", 3); got != "abc" {
		t.Errorf("exact-length input must not gain an ellipsis, got %q", got)
	}
	if got := truncateText("abcdef", 0); got != "abcdef" {
		t.Errorf("max<=0 should disable truncation, got %q", got)
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

// TestProbeVerbDegrades: the probe verb returns the {duration} contract and
// degrades to 0 for an un-probeable / unknown source (no ffprobe needed). The
// fixture videos are fake bytes, so ffprobe (if present) also yields 0 — either
// way the contract holds and there is no error.
func TestProbeVerbDegrades(t *testing.T) {
	app, _ := openFixture(t)

	// Known source, but fake bytes → duration 0 (ffprobe can't read it), ok=true.
	r := callEnv(t, app, "probe", `{"source":"ring.mp4"}`)
	if !r.OK {
		t.Fatalf("probe verb should not error: %s", r.Error)
	}
	var pr ProbeResult
	remarshal(t, r.Data, &pr)
	if pr.Duration != 0 {
		t.Fatalf("fake-byte video should probe to 0, got %v", pr.Duration)
	}
	if pr.Fps != 0 {
		t.Fatalf("fake-byte video should probe fps to 0, got %v", pr.Fps)
	}

	// Unknown source → also {duration:0}, ok=true (degrade, not an error).
	r = callEnv(t, app, "probe", `{"source":"nope.mp4"}`)
	if !r.OK {
		t.Fatalf("probe of an unknown source should degrade, not error: %s", r.Error)
	}
	remarshal(t, r.Data, &pr)
	if pr.Duration != 0 {
		t.Fatalf("unknown source should probe to 0, got %v", pr.Duration)
	}
}

// TestProbeUnknownSourceDirect checks App.Probe directly returns 0 for a source
// outside the open folder (path security: probe only touches indexed originals).
func TestProbeUnknownSourceDirect(t *testing.T) {
	app, _ := openFixture(t)
	if got := app.Probe("/etc/passwd").Duration; got != 0 {
		t.Fatalf("probe of an out-of-folder path must be 0, got %v", got)
	}
}

// TestScrubSegmentVerbAndDirectDegrade proves scrub_segment is wired into
// dispatch (default-deny table) and that an out-of-folder source degrades to
// {path:""} through BOTH the bridge and the direct App call — deterministic,
// no ffmpeg required (fixture videos are fake bytes; path security rejects
// the source before ffmpeg would ever run).
func TestScrubSegmentVerbAndDirectDegrade(t *testing.T) {
	app, _ := openFixture(t)

	r := callEnv(t, app, "scrub_segment", `{"source":"nope.mp4","in":1,"out":3}`)
	if !r.OK {
		t.Fatalf("scrub_segment verb should degrade, not error: %s", r.Error)
	}
	var sr ScrubSegmentResult
	remarshal(t, r.Data, &sr)
	if sr.Path != "" {
		t.Fatalf("unknown source should degrade to path=\"\", got %q", sr.Path)
	}

	if got := app.ScrubSegment("nope.mp4", 1, 3).Path; got != "" {
		t.Fatalf("direct ScrubSegment on unknown source = %q, want \"\"", got)
	}
}

// TestPeaksAndAutoCutVerbsWireThroughDispatch proves peaks and autocut_silence
// are both wired into dispatch and return their documented degrade shapes for
// deterministic (no ffmpeg/becky-cut) inputs.
func TestPeaksAndAutoCutVerbsWireThroughDispatch(t *testing.T) {
	app, _ := openFixture(t)

	r := callEnv(t, app, "peaks", `{"source":"nope.mp4","in":0,"out":5,"buckets":50}`)
	if !r.OK {
		t.Fatalf("peaks verb should degrade, not error: %s", r.Error)
	}
	var pr PeaksResult
	remarshal(t, r.Data, &pr)
	if pr.Count != 0 || len(pr.Peaks) != 0 {
		t.Fatalf("unknown source peaks should degrade to {peaks:[],count:0}, got %+v", pr)
	}

	r = callEnv(t, app, "autocut_silence", `{"name":"nope.mp4"}`)
	if !r.OK {
		t.Fatalf("autocut_silence verb should degrade, not error: %s", r.Error)
	}
	var ar AutoCutResult
	remarshal(t, r.Data, &ar)
	if len(ar.Segments) != 0 {
		t.Fatalf("unknown video autocut should degrade to empty segments, got %+v", ar.Segments)
	}
	if ar.Note == "" {
		t.Error("degrade should carry a plain-language note")
	}
}

// TestCallH1SharedStateVerbs covers the UI→engine telemetry verbs. main.cpp
// fires seek/set_select/set_threshold from three worker threads; before these
// existed each fell through to default: → ok:false with the reply discarded,
// so the engine — and through Context.Timeline the AI — never learned the
// playhead, the selection or the threshold (the H-1 dead code found by the
// 2026-07-20 audit).
func TestCallH1SharedStateVerbs(t *testing.T) {
	app := NewApp()

	if r := callEnv(t, app, "seek", `{"t":14.7,"quiet":true}`); !r.OK {
		t.Fatalf("seek failed: %s", r.Error)
	}
	if r := callEnv(t, app, "set_select", `{"ids":["c2","c5"]}`); !r.OK {
		t.Fatalf("set_select failed: %s", r.Error)
	}
	if r := callEnv(t, app, "set_threshold", `{"on":true,"level":-17}`); !r.OK {
		t.Fatalf("set_threshold failed: %s", r.Error)
	}

	app.mu.Lock()
	ts := app.timelineStateLocked()
	app.mu.Unlock()
	if ts.Playhead != 14.7 {
		t.Errorf("playhead = %v, want 14.7", ts.Playhead)
	}
	if len(ts.Selected) != 2 || ts.Selected[0] != "c2" || ts.Selected[1] != "c5" {
		t.Errorf("selected = %v, want [c2 c5]", ts.Selected)
	}
	if !ts.SkipQuietOn || ts.SkipQuietDB != -17 {
		t.Errorf("threshold = on:%v level:%v, want on:true level:-17", ts.SkipQuietOn, ts.SkipQuietDB)
	}

	// A cleared selection is a real state, and a negative playhead clamps to 0.
	callEnv(t, app, "set_select", `{"ids":[]}`)
	callEnv(t, app, "seek", `{"t":-3}`)
	app.mu.Lock()
	ts = app.timelineStateLocked()
	app.mu.Unlock()
	if len(ts.Selected) != 0 {
		t.Errorf("selected after clear = %v, want empty", ts.Selected)
	}
	if ts.Playhead != 0 {
		t.Errorf("playhead after negative seek = %v, want 0 (clamped)", ts.Playhead)
	}
}
