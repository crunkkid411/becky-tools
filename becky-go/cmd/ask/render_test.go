// render_test.go — covers the chat-reply renderers and the run-result block so the
// human-facing copy (the act prompt, the clarify question, the new-tool pitch, the
// done/error block) is exercised, not just the routing logic.
package main

import (
	"context"
	"strings"
	"testing"
)

func TestActReply_ShowsCommandAndConfirm(t *testing.T) {
	tgt := resolveTarget([]string{makeFile(t, t.TempDir(), "clip.mp4")})
	d := buildActDecision(actTranscribe, tgt, 0.9, "deterministic", "clear action")
	out := actReply(d, tgt)
	if !strings.Contains(out, "becky-transcribe") {
		t.Errorf("act reply should show the command; got:\n%s", out)
	}
	if !strings.Contains(out, "Run it?") {
		t.Errorf("act reply should ask for confirmation; got:\n%s", out)
	}
}

func TestClarifyReply_AsksForFileWhenNoTarget(t *testing.T) {
	d := classifyDeterministic("transcribe this", Target{})
	out := clarifyReply(d, "transcribe this", Target{})
	if !strings.Contains(strings.ToLower(out), "which file or folder") {
		t.Errorf("clarify should ask which file/folder; got:\n%s", out)
	}
}

func TestNewToolReply_NamesItAnIdeaWhenNoCatalogMatch(t *testing.T) {
	// A genuinely novel idea (no catalog keyword) -> the pitch copy.
	out := newToolReply(decision{Kind: decideNewTool}, "make me a sandwich please")
	if !strings.Contains(out, "NEW capability") || !strings.Contains(out, "becky-new-tool") {
		t.Errorf("new-tool reply should name it a new capability + pitch; got:\n%s", out)
	}
	if !strings.Contains(out, "won't run anything") {
		t.Errorf("new-tool reply must be honest that nothing runs now; got:\n%s", out)
	}
}

func TestVerbPhrase_AllActions(t *testing.T) {
	for _, id := range []actionID{actTranscribe, actIdentify, actDescribe, actOCR, actCut} {
		if verbPhrase(id) == "" || verbPhrase(id) == "process" {
			t.Errorf("verbPhrase(%q) should be a specific phrase, got %q", id, verbPhrase(id))
		}
	}
	if verbPhrase(actionID("bogus")) != "process" {
		t.Errorf("unknown action should fall back to 'process'")
	}
}

func TestRunResultBlock_SuccessAndError(t *testing.T) {
	ok := runResultBlock(runResult{Command: []string{"becky-transcribe", "x.mp4"}, Stdout: `{"ok":true}`})
	if !strings.Contains(ok, "Done:") || !strings.Contains(ok, `{"ok":true}`) {
		t.Errorf("success block should show Done + stdout; got:\n%s", ok)
	}
	bad := runResultBlock(runResult{Command: []string{"becky-x", "y"}, Err: context.DeadlineExceeded})
	if !strings.Contains(bad, "Couldn't finish") {
		t.Errorf("error block should say it couldn't finish; got:\n%s", bad)
	}
}

func TestRunCommand_MissingBinaryErrorsNotPanics(t *testing.T) {
	// Running a tool that isn't built must come back as an error result, never panic.
	res := runCommand(context.Background(), []string{"becky-not-a-real-tool", "arg"})
	if res.Err == nil {
		t.Errorf("expected an error result for a missing binary")
	}
}

func TestRunCommand_EmptyIsError(t *testing.T) {
	if res := runCommand(context.Background(), nil); res.Err == nil {
		t.Errorf("empty command should be an error")
	}
}

func TestRoute_NewToolIdeaRendersPitch(t *testing.T) {
	// End-to-end: a novel idea routes to the factory pitch and stages becky-new-tool.
	// Phase 3 behaviour: Pending holds the factory command; reply shows the summary.
	tgt := resolveTarget([]string{makeFile(t, t.TempDir(), "clip.mp4")})
	r := route(context.Background(), nil, "I wish becky could teleport this clip somewhere", tgt)
	if len(r.Pending) == 0 {
		t.Fatalf("an idea should stage the factory command; reply:\n%s", r.Reply)
	}
	if r.Pending[0] != "becky-new-tool" {
		t.Errorf("pending[0] = %q, want becky-new-tool", r.Pending[0])
	}
	if !strings.Contains(r.Reply, "factory") {
		t.Errorf("pitch reply should mention the factory; got:\n%s", r.Reply)
	}
}

func TestHelpReply_ListsOps(t *testing.T) {
	out := helpReply()
	if !strings.Contains(out, "profile") || !strings.Contains(out, "find") {
		t.Errorf("help should list orchestrator ops; got:\n%s", out)
	}
}

func TestLabel_MultiFileShowsCount(t *testing.T) {
	dir := t.TempDir()
	tgt := resolveTarget([]string{
		makeFile(t, dir, "a.mp4"), makeFile(t, dir, "b.mp4"),
		makeFile(t, dir, "c.mp4"), makeFile(t, dir, "d.mp4"),
	})
	if got := tgt.Label(); !strings.Contains(got, "more") || !strings.Contains(got, "4 files") {
		t.Errorf("multi-file label should summarize the count; got %q", got)
	}
}
