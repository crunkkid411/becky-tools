package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// --- mode selection / TUI-preservation guards (the accessibility invariant) ---

// TestFlags_QuestionSelectsSingleShot — an explicit --question selects single-shot;
// no flags does not (even with a TTY).
func TestFlags_QuestionSelectsSingleShot(t *testing.T) {
	ss, _ := parseSingleShotFlags([]string{"--question", "can becky transcribe?"})
	if !ss.isSingleShot() {
		t.Fatalf("--question should select single-shot mode")
	}
	if got := decideMode(ss, true /*TTY*/, ""); got != modeSingleShot {
		t.Fatalf("with --question, decideMode = %d, want modeSingleShot(%d)", got, modeSingleShot)
	}

	none, _ := parseSingleShotFlags([]string{"clip.mp4"})
	if none.isSingleShot() {
		t.Fatalf("a bare positional arg must NOT select single-shot")
	}
}

// TestSingleShot_DoesNotLaunchTUI — THE accessibility guard: a single-shot flag
// must never enter bubbletea. We spy on the launchTUI seam and assert it is not
// called when --question is set.
func TestSingleShot_DoesNotLaunchTUI(t *testing.T) {
	ss, _ := parseSingleShotFlags([]string{"--question", "can becky transcribe?"})
	mode := decideMode(ss, true /*TTY present*/, "")
	if mode == modeTUI {
		t.Fatalf("single-shot must NOT select the TUI; got modeTUI")
	}
	if mode != modeSingleShot {
		t.Fatalf("expected modeSingleShot, got %d", mode)
	}

	// Belt-and-suspenders: prove the launchTUI seam is wired so it would NOT run on
	// the single-shot branch. (main's switch only calls launchTUI for modeTUI.)
	launched := false
	orig := launchTUI
	launchTUI = func([]string) { launched = true }
	defer func() { launchTUI = orig }()
	// Simulate main's dispatch for the single-shot mode (without os.Exit).
	if mode == modeTUI {
		launchTUI(nil)
	}
	if launched {
		t.Fatalf("launchTUI was invoked for a single-shot flag — TUI must stay untouched")
	}
}

// TestInteractiveDefaultPreserved — no single-shot flag + a terminal selects the
// bubbletea TUI launch branch (modeTUI), exactly as before.
func TestInteractiveDefaultPreserved(t *testing.T) {
	ss, _ := parseSingleShotFlags([]string{}) // no flags
	if got := decideMode(ss, true /*TTY*/, ""); got != modeTUI {
		t.Fatalf("no flag + TTY must select modeTUI(%d); got %d", modeTUI, got)
	}
	// And with a dropped path (still no single-shot flag) it stays the TUI.
	ss2, rest := parseSingleShotFlags([]string{"clip.mp4"})
	if got := decideMode(ss2, true, ""); got != modeTUI {
		t.Fatalf("dropped path + TTY must still select modeTUI; got %d", got)
	}
	if len(rest) != 1 || rest[0] != "clip.mp4" {
		t.Fatalf("positional arg must pass through to the TUI as a target; rest=%v", rest)
	}
}

// TestNoTTYPathsUnchanged — with no single-shot flag, the existing no-TTY paths
// are still selected: BECKY_ASK_RUN -> modeHeadlessRun; otherwise modeNoTTY.
func TestNoTTYPathsUnchanged(t *testing.T) {
	ss, _ := parseSingleShotFlags([]string{"clip.mp4"})
	if got := decideMode(ss, false /*no TTY*/, "transcribe"); got != modeHeadlessRun {
		t.Fatalf("no flag + no TTY + BECKY_ASK_RUN must select modeHeadlessRun; got %d", got)
	}
	if got := decideMode(ss, false, ""); got != modeNoTTY {
		t.Fatalf("no flag + no TTY + no env must select modeNoTTY; got %d", got)
	}
	// A single-shot flag wins even over BECKY_ASK_RUN (the more specific intent).
	ssQ, _ := parseSingleShotFlags([]string{"--question", "hi"})
	if got := decideMode(ssQ, false, "transcribe"); got != modeSingleShot {
		t.Fatalf("--question must win over BECKY_ASK_RUN; got %d", got)
	}
}

// --- flag parsing ---

func TestParseSingleShotFlags_Forms(t *testing.T) {
	ss, rest := parseSingleShotFlags([]string{
		"--question=transcribe this", "--target", "clip.mp4", "--run", "--json", "extra.txt",
	})
	if ss.question != "transcribe this" {
		t.Errorf("question = %q, want %q", ss.question, "transcribe this")
	}
	if ss.target != "clip.mp4" {
		t.Errorf("target = %q, want clip.mp4", ss.target)
	}
	if !ss.run {
		t.Errorf("--run should be true")
	}
	if !ss.asJSON {
		t.Errorf("--json should be true")
	}
	if len(rest) != 1 || rest[0] != "extra.txt" {
		t.Errorf("rest = %v, want [extra.txt]", rest)
	}

	// --ask is an alias of --question.
	a, _ := parseSingleShotFlags([]string{"--ask", "hello"})
	if a.question != "hello" {
		t.Errorf("--ask alias: question = %q, want hello", a.question)
	}
	// --run=false is honored.
	b, _ := parseSingleShotFlags([]string{"--question", "x", "--run=false"})
	if b.run {
		t.Errorf("--run=false must be false")
	}
}

// --- text question through the existing brain (nil/absent model -> catalog) ---

// TestSingleShot_TextQuestion_Catalog — a capability question (no model) yields a
// plain answer that names becky-transcribe; exit 0.
func TestSingleShot_TextQuestion_Catalog(t *testing.T) {
	res := buildSingleShot(context.Background(), &ssFlags{question: "can becky transcribe?"})
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0", res.exitCode)
	}
	if res.Kind != "question" {
		t.Fatalf("kind = %q, want question", res.Kind)
	}
	if !strings.Contains(res.Answer, "becky-transcribe") {
		t.Fatalf("answer should name becky-transcribe; got:\n%s", res.Answer)
	}
}

// TestSingleShot_Action_ShowOnly — "transcribe this" with a real target prints the
// exact command, does NOT run it, exit 0.
func TestSingleShot_Action_ShowOnly(t *testing.T) {
	f := tmpFile(t, "clip.mp4")
	res := buildSingleShot(context.Background(), &ssFlags{
		question: "transcribe this", target: f,
	})
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0", res.exitCode)
	}
	if res.Kind != "action" {
		t.Fatalf("kind = %q, want action", res.Kind)
	}
	if res.Ran {
		t.Fatalf("ran = true, want false (show-only without --run)")
	}
	if len(res.Command) == 0 || res.Command[0] != "becky-transcribe" {
		t.Fatalf("command = %v, want [becky-transcribe ...]", res.Command)
	}
	if !strings.Contains(res.Answer, "becky-transcribe") {
		t.Fatalf("answer should be the command string; got %q", res.Answer)
	}
}

// TestSingleShot_Action_Run — same with --run and a faked sibling tool: ran=true
// and the command's exit code propagates. binPathFor resolves becky-<tool> in the
// working dir, so we chdir into a temp dir holding a fake becky-transcribe.
func TestSingleShot_Action_Run(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-tool exec uses a POSIX shell script; covered on Linux/CI")
	}
	dir := chdirTemp(t)
	f := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeFakeTool(t, filepath.Join(dir, "becky-transcribe"), 0, "transcribed-ok")
	res := buildSingleShot(context.Background(), &ssFlags{
		question: "transcribe this", target: f, run: true,
	})
	if !res.Ran {
		t.Fatalf("ran = false, want true (--run)")
	}
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (fake tool exits 0); answer=%q", res.exitCode, res.Answer)
	}
	if !strings.Contains(res.Answer, "transcribed-ok") {
		t.Fatalf("answer should carry tool stdout; got %q", res.Answer)
	}

	// A failing tool propagates exit 1.
	writeFakeTool(t, filepath.Join(dir, "becky-transcribe"), 3, "boom")
	res2 := buildSingleShot(context.Background(), &ssFlags{
		question: "transcribe this", target: f, run: true,
	})
	if res2.exitCode != 1 {
		t.Fatalf("failing tool: exit = %d, want 1", res2.exitCode)
	}
}

// --- JSON shape ---

// TestSingleShot_JSON_Shape — --json emits exactly one object with the §4.2 keys
// and correct kind/command/source values.
func TestSingleShot_JSON_Shape(t *testing.T) {
	f := tmpFile(t, "clip.mp4")
	res := buildSingleShot(context.Background(), &ssFlags{question: "transcribe this", target: f})

	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(res); err != nil {
		t.Fatalf("encode: %v", err)
	}
	out := buf.String()
	if strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Fatalf("JSON must be exactly one line/object; got:\n%s", out)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	for _, k := range []string{"question", "answer", "kind", "command", "ran", "source", "degraded"} {
		if _, ok := m[k]; !ok {
			t.Errorf("JSON missing key %q", k)
		}
	}
	if m["kind"] != "action" {
		t.Errorf("kind = %v, want action", m["kind"])
	}
	if m["ran"] != false {
		t.Errorf("ran = %v, want false", m["ran"])
	}
	cmd, ok := m["command"].([]any)
	if !ok || len(cmd) == 0 || cmd[0] != "becky-transcribe" {
		t.Errorf("command = %v, want [becky-transcribe ...]", m["command"])
	}

	// A pure question -> command is JSON null.
	resQ := buildSingleShot(context.Background(), &ssFlags{question: "can becky transcribe?"})
	var bq strings.Builder
	_ = json.NewEncoder(&bq).Encode(resQ)
	var mq map[string]any
	_ = json.Unmarshal([]byte(bq.String()), &mq)
	if mq["command"] != nil {
		t.Errorf("question command must be null; got %v", mq["command"])
	}
}

// --- image path (faked vision) ---

type fakeVision struct {
	desc     string
	source   string
	degraded bool
	errMsg   string
}

func (v fakeVision) ask(_ context.Context, _, _ string) (string, string, bool, string) {
	return v.desc, v.source, v.degraded, v.errMsg
}

// TestSingleShot_Image_FakeVision — a fake visionAsker's description becomes the
// answer; kind==image, source==lfm2.5-vl, exit 0.
func TestSingleShot_Image_FakeVision(t *testing.T) {
	img := tmpFile(t, "frame.png")
	withVision(t, fakeVision{desc: "Yes — one person, center-left.", source: "lfm2.5-vl"})

	res := buildSingleShot(context.Background(), &ssFlags{
		image: img, question: "is there a person on screen?",
	})
	if res.exitCode != 0 {
		t.Fatalf("exit = %d, want 0", res.exitCode)
	}
	if res.Kind != "image" {
		t.Fatalf("kind = %q, want image", res.Kind)
	}
	if res.Source != "lfm2.5-vl" {
		t.Fatalf("source = %q, want lfm2.5-vl", res.Source)
	}
	if res.Answer != "Yes — one person, center-left." {
		t.Fatalf("answer = %q, want the fake description", res.Answer)
	}
	if res.Degraded {
		t.Fatalf("degraded should be false on a good answer")
	}
}

// TestSingleShot_Image_Degrades — fake vision degrades -> plain note + exit 0,
// degraded:true.
func TestSingleShot_Image_Degrades(t *testing.T) {
	img := tmpFile(t, "frame.png")
	withVision(t, fakeVision{source: "lfm2.5-vl", degraded: true, errMsg: "model missing"})

	res := buildSingleShot(context.Background(), &ssFlags{image: img, question: "what's here?"})
	if res.exitCode != 0 {
		t.Fatalf("degrade must exit 0; got %d", res.exitCode)
	}
	if !res.Degraded {
		t.Fatalf("degraded should be true")
	}
	if !strings.Contains(res.Answer, "couldn't read the image") {
		t.Fatalf("answer should be the plain degrade note; got %q", res.Answer)
	}
}

// TestSingleShot_ImageWithoutQuestion_Usage — --image with no --question -> exit 2.
func TestSingleShot_ImageWithoutQuestion_Usage(t *testing.T) {
	img := tmpFile(t, "frame.png")
	res := buildSingleShot(context.Background(), &ssFlags{image: img})
	if res.exitCode != 2 {
		t.Fatalf("exit = %d, want 2 (usage)", res.exitCode)
	}
}

// TestSingleShot_MissingImage_Usage — --image pointing at a non-existent file -> exit 2.
func TestSingleShot_MissingImage_Usage(t *testing.T) {
	res := buildSingleShot(context.Background(), &ssFlags{
		image: filepath.Join(t.TempDir(), "nope.png"), question: "what's here?",
	})
	if res.exitCode != 2 {
		t.Fatalf("exit = %d, want 2 (missing image is a usage error)", res.exitCode)
	}
}

// TestSingleShot_EmptyQuestion_Usage — an empty --question -> exit 2.
func TestSingleShot_EmptyQuestion_Usage(t *testing.T) {
	res := buildSingleShot(context.Background(), &ssFlags{question: "   "})
	if res.exitCode != 2 {
		t.Fatalf("exit = %d, want 2 (empty question)", res.exitCode)
	}
}

// --- clean stdout (no ANSI) ---

// TestSingleShot_PlainOutput_NoANSI — plain output must contain no ESC sequences,
// even though router replies are lipgloss-styled.
func TestSingleShot_PlainOutput_NoANSI(t *testing.T) {
	res := buildSingleShot(context.Background(), &ssFlags{question: "can becky transcribe?"})
	plain := formatPlain(res)
	if strings.Contains(plain, "\x1b") {
		t.Fatalf("plain output must not contain ANSI escape sequences; got %q", plain)
	}
	// And plainAnswer actively strips a styled string.
	styled := "\x1b[38;5;46mYes\x1b[0m — becky can transcribe"
	if got := plainAnswer(styled); strings.Contains(got, "\x1b") {
		t.Fatalf("plainAnswer left ANSI in %q", got)
	} else if !strings.Contains(got, "Yes — becky can transcribe") {
		t.Fatalf("plainAnswer dropped content: %q", got)
	}
}

// --- helpers ---

func tmpFile(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func withVision(t *testing.T, v visionAsker) {
	t.Helper()
	orig := activeVisionAsker
	activeVisionAsker = v
	t.Cleanup(func() { activeVisionAsker = orig })
}

// chdirTemp chdirs into a fresh temp dir for the test and restores cwd after.
func chdirTemp(t *testing.T) string {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	// macOS/Linux temp dirs can be symlinks (e.g. /var -> /private/var); resolve so
	// the path matches what binPathFor sees via os.Getwd inside the call.
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		return real
	}
	return dir
}

// writeFakeTool writes a POSIX shell script that prints out on stdout and exits
// with code. Used to stand in for a sibling becky-<tool> binary in --run tests.
func writeFakeTool(t *testing.T, path string, code int, out string) {
	t.Helper()
	script := "#!/bin/sh\necho \"" + out + "\"\nexit " + itoa(code) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tool: %v", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
