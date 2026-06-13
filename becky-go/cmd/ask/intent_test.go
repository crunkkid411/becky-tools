// intent_test.go — proves FEATURE 4 (ACT on clear intent, DISCUSS when unsure).
// These tests pin the corroboration/fact gate: a target + an unambiguous action
// runs the exact becky-* command; a question / idea / ambiguity / missing target
// never produces a tool call. The model layer's parse + reconcile (the bits that
// don't need a live server) are tested directly; the live model is exercised
// separately under -tags=llm.
package main

import (
	"context"
	"strings"
	"testing"
)

// videoTarget is a single dropped video used across the routing tests.
func videoTarget(t *testing.T) Target {
	t.Helper()
	return resolveTarget([]string{makeFile(t, t.TempDir(), "clip.mp4")})
}

// --- the two poster cases from the brief ---

func TestGate_ClearActionWithTarget_Runs(t *testing.T) {
	// "if i give it a file and say 'transcribe this' i want it to just do the thing."
	tgt := videoTarget(t)

	d := classifyDeterministic("transcribe this", tgt)

	if d.Kind != decideAct {
		t.Fatalf("'transcribe this' + target should ACT, got kind=%d (%s)", d.Kind, d.Rationale)
	}
	want := []string{"becky-transcribe", tgt.Primary()}
	if !equalArgv(d.Command, want) {
		t.Errorf("act command = %v, want %v", d.Command, want)
	}
}

func TestGate_AmbiguousQuestion_NoToolCall(t *testing.T) {
	// "if i'm asking a question i don't want it to launch a bunch of tool calls"
	tgt := videoTarget(t)

	d := classifyDeterministic("can becky figure out where this was filmed?", tgt)

	if d.Kind == decideAct {
		t.Fatalf("a capability question must NOT act; got an act decision: %v", d.Command)
	}
	if len(d.Command) != 0 {
		t.Errorf("a question must carry no command, got %v", d.Command)
	}
}

func TestGate_ActionButNoTarget_Clarifies(t *testing.T) {
	// A bare command with nothing to run on -> ask the ONE missing thing, no call.
	d := classifyDeterministic("transcribe this", Target{})

	if d.Kind != decideClarify {
		t.Fatalf("action with no target should CLARIFY, got kind=%d", d.Kind)
	}
	if len(d.Command) != 0 {
		t.Errorf("clarify must carry no command, got %v", d.Command)
	}
}

func TestGate_CapabilityHowToQuestion_Discusses(t *testing.T) {
	// "how do I transcribe a video?" is a question even though it names an op.
	d := classifyDeterministic("how do I transcribe a video?", Target{})
	if d.Kind != decideQuestion {
		t.Errorf("how-to question should DISCUSS, got kind=%d (%s)", d.Kind, d.Rationale)
	}
}

func TestGate_IdeaIsNewToolNotAction(t *testing.T) {
	// An idea ("I wish becky could …") is pitch territory, never an action — even
	// with a target present.
	tgt := videoTarget(t)
	d := classifyDeterministic("I wish becky could blur faces so I can share this", tgt)
	if d.Kind != decideNewTool {
		t.Errorf("an idea should route to new_tool, got kind=%d (%s)", d.Kind, d.Rationale)
	}
	if d.Kind == decideAct {
		t.Error("an idea must never act")
	}
}

func TestGate_PoliteCommandWithTarget_Runs(t *testing.T) {
	// "please identify who is in this" is a polite command, not a probe -> act.
	tgt := videoTarget(t)
	d := classifyDeterministic("please identify who is in this", tgt)
	if d.Kind != decideAct || d.Action != actIdentify {
		t.Fatalf("polite command should ACT(identify), got kind=%d action=%s", d.Kind, d.Action)
	}
}

func TestGate_OCROnVideoDegradesToClarify(t *testing.T) {
	// "ocr this" on a raw video can't run directly (OCR needs frames). The gate
	// must NOT fabricate a command — it clarifies instead.
	tgt := videoTarget(t)
	d := classifyDeterministic("ocr this", tgt)
	if d.Kind == decideAct {
		t.Errorf("OCR on a raw video should not act (no frames); got %v", d.Command)
	}
}

// --- route() end-to-end (no model): act stages a Pending command ---

func TestRoute_ActStagesPendingCommand(t *testing.T) {
	tgt := videoTarget(t)
	r := route(context.Background(), nil, "transcribe this", tgt)
	if len(r.Pending) == 0 {
		t.Fatalf("a clear action should stage a Pending command for confirmation")
	}
	if r.Pending[0] != "becky-transcribe" {
		t.Errorf("Pending[0] = %q, want becky-transcribe", r.Pending[0])
	}
}

func TestRoute_QuestionStagesNothing(t *testing.T) {
	tgt := videoTarget(t)
	r := route(context.Background(), nil, "can becky tell me who is in videos?", tgt)
	if len(r.Pending) != 0 {
		t.Errorf("a question must stage no command, got %v", r.Pending)
	}
}

// --- the model layer: parse + reconcile (no live server needed) ---

func TestParseModelIntent_StripsFenceAndProse(t *testing.T) {
	raw := "Sure!\n```json\n{\"kind\":\"action\",\"action\":\"transcribe\",\"confidence\":0.9}\n```"
	mi, err := parseModelIntent(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if mi.Kind != "action" || mi.Action != "transcribe" {
		t.Errorf("parsed = %+v, want action/transcribe", mi)
	}
}

func TestReconcile_TargetGate_ModelActionWithoutTargetCannotRun(t *testing.T) {
	// The model says "action", but there is NO target. The Go gate must refuse to
	// produce an act decision (the model can't override the fact gate).
	det := decision{Kind: decideQuestion, Confidence: 0.4}
	mi := modelIntent{Kind: "action", Action: "transcribe", Confidence: 0.99}
	d := reconcile(det, mi, Target{})
	if d.Kind == decideAct {
		t.Fatal("model 'action' with no target must NOT act")
	}
	if d.Kind != decideClarify {
		t.Errorf("expected clarify when model acts without a target, got kind=%d", d.Kind)
	}
}

func TestReconcile_ModelActionWithTargetBuildsExactCommand(t *testing.T) {
	tgt := videoTarget(t)
	det := decision{Kind: decideQuestion, Confidence: 0.4} // uncertain det
	mi := modelIntent{Kind: "action", Action: "identify", Confidence: 0.95}
	d := reconcile(det, mi, tgt)
	if d.Kind != decideAct {
		t.Fatalf("model action + target should ACT, got kind=%d", d.Kind)
	}
	want := []string{"becky-identify", tgt.Primary(), "--kb", "kb-final"}
	if !equalArgv(d.Command, want) {
		t.Errorf("reconciled command = %v, want %v", d.Command, want)
	}
}

func TestReconcile_ModelDowngradesToQuestion(t *testing.T) {
	// The model can downgrade an uncertain action to a question (no tool call).
	tgt := videoTarget(t)
	det := decision{Kind: decideAct, Action: actTranscribe, Command: []string{"becky-transcribe", tgt.Primary()}, Confidence: 0.5}
	mi := modelIntent{Kind: "question", Confidence: 0.9}
	d := reconcile(det, mi, tgt)
	if d.Kind != decideQuestion {
		t.Errorf("model 'question' should downgrade to discuss, got kind=%d", d.Kind)
	}
	if len(d.Command) != 0 {
		t.Errorf("a downgraded question must carry no command, got %v", d.Command)
	}
}

// --- model-unavailable degrade (no GGUF / no binary) ---

func TestClassify_DegradesWhenModelMissing(t *testing.T) {
	// A client pointed at a non-existent GGUF must NOT be consulted; classify falls
	// back to the deterministic reading and never errors.
	tgt := videoTarget(t)
	cli := newLlamaClient(`X:\nope\does-not-exist.gguf`, `X:\nope\llama-server.exe`, nil)

	// An uncertain phrase would normally trigger the model; here it must degrade.
	d := classify(context.Background(), cli, "hmm what about this", tgt)
	if d.Kind == decideAct {
		t.Errorf("ambiguous + missing model should not act, got %v", d.Command)
	}
	if !strings.Contains(d.Source, "deterministic") {
		t.Errorf("Source should note the deterministic fallback, got %q", d.Source)
	}
}

func TestLlamaClient_ReadyReportsMissing(t *testing.T) {
	cli := newLlamaClient(`X:\nope\missing.gguf`, `X:\nope\llama-server.exe`, nil)
	if err := cli.Ready(); err == nil {
		t.Error("Ready() should error when the model GGUF is absent")
	}
}
