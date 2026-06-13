// coverage_test.go — exercises the remaining pure / headless-observable branches
// (prompt building, the reconcile fall-throughs, the target-intro variants, small
// helpers) so the non-TTY logic is well covered without a terminal. The bubbletea
// Update/View loop and the live llama transport are validated separately (the
// running becky-ask.exe runs and the -tags=llm test).
package main

import (
	"context"
	"strings"
	"testing"
)

func TestBuildIntentUserPrompt_AllTargetKinds(t *testing.T) {
	dir := t.TempDir()
	file := resolveTarget([]string{makeFile(t, dir, "clip.mp4")})
	if p := buildIntentUserPrompt("transcribe this", file); !strings.Contains(p, "TARGET FILE:") || !strings.Contains(p, "REQUEST: transcribe this") {
		t.Errorf("file prompt malformed:\n%s", p)
	}
	folder := resolveTarget([]string{dir})
	if p := buildIntentUserPrompt("ocr it", folder); !strings.Contains(p, "TARGET FOLDER:") {
		t.Errorf("folder prompt should say TARGET FOLDER:\n%s", p)
	}
	multi := resolveTarget([]string{makeFile(t, dir, "a.mp4"), makeFile(t, dir, "b.mp4")})
	if p := buildIntentUserPrompt("x", multi); !strings.Contains(p, "TARGET FILES:") {
		t.Errorf("multi prompt should say TARGET FILES:\n%s", p)
	}
	none := buildIntentUserPrompt("hello", Target{})
	if !strings.Contains(none, "TARGET: (none set)") {
		t.Errorf("no-target prompt should say none set:\n%s", none)
	}
}

func TestReconcile_NewToolAndClarifyAndUnknown(t *testing.T) {
	det := decision{Kind: decideQuestion, Confidence: 0.4}
	if d := reconcile(det, modelIntent{Kind: "new_tool", Confidence: 0.8}, Target{}); d.Kind != decideNewTool {
		t.Errorf("model new_tool should map to decideNewTool, got %d", d.Kind)
	}
	if d := reconcile(det, modelIntent{Kind: "clarify", Confidence: 0.8}, Target{}); d.Kind != decideClarify {
		t.Errorf("model clarify should map to decideClarify, got %d", d.Kind)
	}
	// Unknown kind -> trust the deterministic reading.
	if d := reconcile(det, modelIntent{Kind: "wat", Confidence: 0.8}, Target{}); d.Kind != decideQuestion {
		t.Errorf("unknown model kind should fall back to det, got %d", d.Kind)
	}
	// "action" with an unusable op but det already act -> keep det's command.
	tgt := resolveTarget([]string{makeFile(t, t.TempDir(), "clip.mp4")})
	detAct := buildActDecision(actTranscribe, tgt, 0.9, "deterministic", "x")
	if d := reconcile(detAct, modelIntent{Kind: "action", Action: "", Confidence: 0.9}, tgt); d.Kind != decideAct {
		t.Errorf("action with empty op but det-act should keep det's act, got %d", d.Kind)
	}
}

func TestTargetIntro_FolderAndNoAction(t *testing.T) {
	dir := t.TempDir()
	folder := resolveTarget([]string{dir})
	if out := targetIntro(folder, quickActionsFor(folder)); !strings.Contains(out, "OCR") {
		t.Errorf("folder intro should list OCR; got:\n%s", out)
	}
	// A target whose only resolved path is a non-media file -> no actions.
	noAct := resolveTarget([]string{makeFile(t, dir, "notes.txt")})
	if out := targetIntro(noAct, quickActionsFor(noAct)); !strings.Contains(out, "No one-key action") {
		t.Errorf("non-media file should say no one-key action; got:\n%s", out)
	}
}

func TestTargetIntro_NotesMissingPaths(t *testing.T) {
	dir := t.TempDir()
	// One good file + one missing arg -> the intro should note the missing one.
	good := makeFile(t, dir, "clip.mp4")
	tgt := resolveTarget([]string{good, "X:\\nope\\ghost.mp4"})
	out := targetIntro(tgt, quickActionsFor(tgt))
	if !strings.Contains(out, "couldn't find") {
		t.Errorf("intro should note the missing path; got:\n%s", out)
	}
}

func TestHelpLine_BusyAndPending(t *testing.T) {
	m := sized(t)
	m.busy = true
	if !strings.Contains(m.helpLine(), "Running a tool") {
		t.Errorf("busy help line wrong: %q", m.helpLine())
	}
	m.busy = false
	m.pending = []string{"becky-transcribe", "x.mp4"}
	if !strings.Contains(m.helpLine(), "y to run") {
		t.Errorf("pending help line wrong: %q", m.helpLine())
	}
}

func TestDigitIndex(t *testing.T) {
	if i, ok := digitIndex("3"); !ok || i != 2 {
		t.Errorf("digitIndex(3) = %d,%v want 2,true", i, ok)
	}
	if _, ok := digitIndex("0"); ok {
		t.Errorf("0 is not a valid 1-based action key")
	}
	if _, ok := digitIndex("12"); ok {
		t.Errorf("multi-char is not a digit key")
	}
	if _, ok := digitIndex("a"); ok {
		t.Errorf("letter is not a digit key")
	}
}

func TestHeadOf_Truncates(t *testing.T) {
	if headOf("short", 10) != "short" {
		t.Errorf("short string should pass through")
	}
	long := strings.Repeat("x", 50)
	if out := headOf(long, 10); !strings.Contains(out, "truncated") || len(out) <= 10 {
		t.Errorf("long string should be truncated with a marker; got %q", out)
	}
}

func TestPrimary_EmptyTarget(t *testing.T) {
	if (Target{}).Primary() != "" {
		t.Errorf("empty target Primary() should be empty")
	}
	if (Target{}).Label() != "" {
		t.Errorf("empty target Label() should be empty")
	}
}

func TestRoute_EmptyQuestionIsNoOp(t *testing.T) {
	r := route(context.Background(), nil, "   ", Target{})
	if r.Reply != "" || len(r.Pending) != 0 {
		t.Errorf("empty question should produce nothing, got %+v", r)
	}
}

func TestRoute_HelpBuiltin(t *testing.T) {
	r := route(context.Background(), nil, "?", Target{})
	if !strings.Contains(r.Reply, "becky can do these things") {
		t.Errorf("? should show help; got:\n%s", r.Reply)
	}
}
