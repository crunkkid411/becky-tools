package assistant

import (
	"context"
	"strings"
	"testing"
)

// assist_test.go covers Router.Assist — the becky-clip CHAT entry point (distinct
// from Handle, which the action router tests cover). Assist makes "becky" a REAL
// assistant: Tier-0 commands still run instantly with no model, but any other
// message (a question, a fuzzy request) is answered by the best available capable
// model (Claude CLI/API, else the local model) instead of silently degrading to a
// keyword grep. With NO model at all it falls back to an honest keyword search.

// TestAssistConversesAGeneralQuestion: a plain question with a capable backend
// available is ANSWERED by the model (prose), not turned into a keyword grep.
func TestAssistConversesAGeneralQuestion(t *testing.T) {
	cli := &fakeBackend{name: "claude-cli", available: true,
		reply: "Search reads the .srt transcripts beside each video; if a video has no transcript yet, hit the transcribe button."}
	r := NewRouter(Options{ClaudeCLI: cli, Log: func(string, ...any) {}})

	p, err := r.Assist(context.Background(), "why does search only show videos and not quotes?", onlineCtx(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if cli.callCount() != 1 {
		t.Fatalf("a general question should call the capable backend once, got %d calls", cli.callCount())
	}
	if !strings.Contains(p.PreviewText, "transcripts") {
		t.Fatalf("answer should be the model's prose, got %q", p.PreviewText)
	}
	if len(p.Actions) != 0 {
		t.Fatalf("a prose answer must NOT be mangled into actions, got %+v", p.Actions)
	}
	if p.Note == "" || !strings.Contains(strings.ToLower(p.Note), "claude") {
		t.Fatalf("the note should name the backend so the user can SEE which model answered, got %q", p.Note)
	}
}

// TestAssistTier0CommandRunsWithoutModel: an explicit command is still executed
// deterministically (no tokens spent) — Assist must not send everything to Claude.
func TestAssistTier0CommandRunsWithoutModel(t *testing.T) {
	cli := &fakeBackend{name: "claude-cli", available: true, reply: "should-not-be-called"}
	r := NewRouter(Options{ClaudeCLI: cli})

	p, err := r.Assist(context.Background(), "export the compilation", onlineCtx(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if cli.callCount() != 0 {
		t.Fatal("a Tier-0 command must not call the model")
	}
	if len(p.Actions) != 1 || p.Actions[0].Verb != VerbExport {
		t.Fatalf("actions = %+v, want one export", p.Actions)
	}
}

// TestAssistConverseEmitsActions: when the user asks the model to change the
// timeline, the conversational reply's prose is shown AND the appended allowlisted
// action is parsed into a real proposal (so chat can both talk and act).
func TestAssistConverseEmitsActions(t *testing.T) {
	cli := &fakeBackend{name: "claude-cli", available: true,
		reply: "Sure — I'll turn the forensic lower-third on.\nset_overlay field=enabled value=true"}
	r := NewRouter(Options{ClaudeCLI: cli})

	p, err := r.Assist(context.Background(), "please turn on the lower third for me", onlineCtx(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.PreviewText, "lower-third") {
		t.Fatalf("prose should be preserved, got %q", p.PreviewText)
	}
	if len(p.Actions) != 1 || p.Actions[0].Verb != VerbSetOverlay {
		t.Fatalf("the appended action should parse, got %+v", p.Actions)
	}
	if !p.Mutates {
		t.Fatal("a set_overlay proposal must be flagged Mutates for the ✓/✗ gate")
	}
}

// TestAssistNoModelDegradesToKeywordSearch: with NO capable backend, Assist still
// produces a usable keyword search + an honest note telling the user HOW to enable
// the real assistant (sign into Claude / set a key) — never a dead chat.
func TestAssistNoModelDegradesToKeywordSearch(t *testing.T) {
	r := NewRouter(Options{Log: func(string, ...any) {}}) // no backends at all

	p, err := r.Assist(context.Background(), "what can you do for me?", onlineCtx(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) == 0 || p.Actions[0].Verb != VerbSearch {
		t.Fatalf("no-model fallback should propose a keyword search, got %+v", p.Actions)
	}
	if p.Note == "" || !strings.Contains(strings.ToLower(p.Note), "claude") {
		t.Fatalf("the fallback note should tell the user how to enable Claude, got %q", p.Note)
	}
}

// TestAssistRespectsOfflineToggle: with online OFF, Assist must NOT call a frontier
// backend (it falls to the local model, or the keyword floor) — the toggle is real.
func TestAssistRespectsOfflineToggle(t *testing.T) {
	cli := &fakeBackend{name: "claude-cli", available: true, reply: "[1]"}
	local := &fakeBackend{name: "local", available: true, reply: "I can search and assemble clips for you."}
	r := NewRouter(Options{Local: local, ClaudeCLI: cli})

	cx := onlineCtx(nil)
	cx.Online = false
	p, err := r.Assist(context.Background(), "what can you help me with?", cx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cli.callCount() != 0 {
		t.Fatal("frontier (Claude) must NOT be called when online is off")
	}
	if local.callCount() != 1 {
		t.Fatalf("offline chat should use the local model, got %d local calls", local.callCount())
	}
	if !strings.Contains(p.PreviewText, "assemble clips") {
		t.Fatalf("offline answer should be the local model's prose, got %q", p.PreviewText)
	}
}

// TestInvestigateRouting: an "investigate my notes/vault" ask routes to the
// agentic claude CLI (read-only file investigator), NOT the keyword floor — and a
// plain chat / clip command does NOT.
func TestInvestigateRouting(t *testing.T) {
	dir := t.TempDir() // a real folder so investigateDirs keeps it
	cli := &fakeBackend{name: "claude-cli", available: true,
		reply: "The $10,000 Penguin bounty is in 2026-05-19_..._[BaTnp3Jy0vc].mp4 at 00:09:03 (cited the .en.srt)."}
	r := NewRouter(Options{ClaudeCLI: cli, Log: func(string, ...any) {}})

	cx := Context{FolderRoot: dir, Online: true, Budget: &Budget{MaxTurns: 40}}
	p, err := r.Assist(context.Background(), "look in my notes and tell me which video the penguin bounty is documented in", cx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cli.callCount() != 1 {
		t.Fatalf("investigate should call the claude CLI once, got %d", cli.callCount())
	}
	// The agentic request must enable read tools over the case folder.
	req := cli.calls[0]
	if !req.Agentic {
		t.Fatal("investigate must send an Agentic request")
	}
	if len(req.AllowDirs) == 0 || req.AllowDirs[0] != dir {
		t.Fatalf("investigate must grant the case folder, got AllowDirs=%v", req.AllowDirs)
	}
	if !strings.Contains(p.PreviewText, "BaTnp3Jy0vc") {
		t.Fatalf("investigate answer should carry the model's cited finding, got %q", p.PreviewText)
	}

	// Offline (use-Claude off) → an honest note, not a silent grep, and no CLI call.
	cli2 := &fakeBackend{name: "claude-cli", available: true, reply: "x"}
	r2 := NewRouter(Options{ClaudeCLI: cli2})
	cxOff := Context{FolderRoot: dir, Online: false, Budget: &Budget{MaxTurns: 40}}
	p2, _ := r2.Assist(context.Background(), "find the documented evidence in my vault", cxOff, nil)
	if cli2.callCount() != 0 {
		t.Fatal("offline investigate must NOT call the CLI")
	}
	if !strings.Contains(strings.ToLower(p2.Note), "use claude") {
		t.Fatalf("offline investigate should explain how to enable it, got note %q", p2.Note)
	}
}

func TestInvestigateIntent(t *testing.T) {
	yes := []string{
		"which video is the penguin bounty in?",
		"look it up in my notes",
		"find the documented evidence in my vault",
		`search E:\TakingBack2007 for the bounty`,
		"where was the $10,000 offered",
	}
	for _, u := range yes {
		if !investigateIntent(u) {
			t.Fatalf("expected investigate intent for %q", u)
		}
	}
	no := []string{"add clip 3", "turn on the lower third", "what does export do?"}
	for _, u := range no {
		if investigateIntent(u) {
			t.Fatalf("did NOT expect investigate intent for %q", u)
		}
	}
}

// TestSplitProseAndActions covers the conservative splitter directly.
func TestSplitProseAndActions(t *testing.T) {
	// pure prose → no actions
	prose, acts, _ := splitProseAndActions("You can search the transcripts and click a quote to preview it.")
	if len(acts) != 0 || !strings.Contains(prose, "search the transcripts") {
		t.Fatalf("pure prose mis-split: prose=%q acts=%+v", prose, acts)
	}
	// prose that merely starts with a verb word but has no key=value is still prose
	prose2, acts2, _ := splitProseAndActions("Search works by reading the .srt files next to each video.")
	if len(acts2) != 0 {
		t.Fatalf("a sentence starting with 'Search' must not become an action: %+v", acts2)
	}
	if prose2 == "" {
		t.Fatal("the sentence should remain as prose")
	}
	// prose + a trailing DSL action line → both
	prose3, acts3, _ := splitProseAndActions("On it.\nadd_clip source=\"ring.mp4\" in=00:00:10,000 out=00:00:13,000 label=\"the cat\"")
	if !strings.Contains(prose3, "On it") {
		t.Fatalf("prose lost: %q", prose3)
	}
	if len(acts3) != 1 || acts3[0].Verb != VerbAddClip {
		t.Fatalf("trailing DSL action should parse: %+v", acts3)
	}
	// a pure JSON action payload → all actions, no prose
	prose4, acts4, _ := splitProseAndActions(`[{"verb":"export","args":{}}]`)
	if prose4 != "" || len(acts4) != 1 || acts4[0].Verb != VerbExport {
		t.Fatalf("pure JSON payload mis-split: prose=%q acts=%+v", prose4, acts4)
	}
}
