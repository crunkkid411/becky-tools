package assistant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"becky-go/internal/footage"
)

// onlineCtx builds a Context with the frontier tier enabled + a generous budget.
func onlineCtx(idx *footage.FolderIndex) Context {
	return Context{
		FolderRoot: "C:/case",
		Index:      idx,
		Timeline:   TimelineState{},
		Online:     true,
		Budget:     &Budget{MaxUSD: 5},
	}
}

// tinyIndex returns an in-memory FolderIndex with one transcribed video whose
// transcript mentions the cat — enough for the funnel's grep half.
func tinyIndex(t *testing.T) *footage.FolderIndex {
	t.Helper()
	root := t.TempDir()
	mp4 := filepath.Join(root, "ring.mp4")
	srt := filepath.Join(root, "ring.srt")
	must(t, os.WriteFile(mp4, []byte("x"), 0o644))
	must(t, os.WriteFile(srt, []byte(
		"1\n00:00:10,000 --> 00:00:13,000\nI will pay you for the cat\n\n"+
			"2\n00:00:20,000 --> 00:00:22,000\nbring me the cat Penguin\n"), 0o644))
	idx, err := footage.Index(root)
	if err != nil {
		t.Fatal(err)
	}
	return &idx
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// TestHandleTier0: an explicit command lands at Tier 0, produces the right action
// + a search exec command for retrieval verbs, and never calls a backend.
func TestHandleTier0(t *testing.T) {
	local := &fakeBackend{name: "local", available: true, reply: "should-not-be-called"}
	r := NewRouter(Options{Local: local})

	p, err := r.Handle(context.Background(), "export the compilation", Context{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Tier != TierDeterministic {
		t.Fatalf("tier = %v, want deterministic", p.Tier)
	}
	if len(p.Actions) != 1 || p.Actions[0].Verb != VerbExport {
		t.Fatalf("actions = %+v, want export", p.Actions)
	}
	if local.callCount() != 0 {
		t.Fatal("Tier-0 command must not call the model")
	}

	// A literal search lands at Tier 0 and forms a becky-search exec command.
	p2, _ := r.Handle(context.Background(), `find the word "cat"`, Context{}, nil)
	if len(p2.ExecCommands) != 1 || p2.ExecCommands[0].Bin != "becky-search" {
		t.Fatalf("literal search should form a becky-search command: %+v", p2.ExecCommands)
	}
}

// TestHandleTier1Local: a fuzzy single-action utterance goes to the local model;
// its parsed action becomes the proposal.
func TestHandleTier1Local(t *testing.T) {
	local := &fakeBackend{
		name: "local", available: true,
		reply: `add_clip source="ring.mp4" in=00:00:20,000 out=00:00:22,000 label="the cat bit"`,
	}
	r := NewRouter(Options{Local: local})
	p, err := r.Handle(context.Background(), "chuck the cat bit onto the end", Context{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Tier != TierLocal {
		t.Fatalf("tier = %v, want local", p.Tier)
	}
	if len(p.Actions) != 1 || p.Actions[0].Verb != VerbAddClip {
		t.Fatalf("actions = %+v, want one add_clip", p.Actions)
	}
	if local.callCount() != 1 {
		t.Fatalf("local backend calls = %d, want 1", local.callCount())
	}
	if !p.Mutates {
		t.Fatal("an add_clip proposal must be flagged Mutates")
	}
}

// TestHandleTier1DegradesWhenLocalDown: when the local model is unavailable, a
// Tier-1 turn degrades to a Tier-0 literal search with an honest note (never
// crashes).
func TestHandleTier1DegradesWhenLocalDown(t *testing.T) {
	r := NewRouter(Options{Local: &fakeBackend{name: "local", available: false}})
	p, err := r.Handle(context.Background(), "tighten up that clip a bit", Context{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Tier != TierDeterministic {
		t.Fatalf("degrade tier = %v, want deterministic floor", p.Tier)
	}
	if p.Note == "" || !strings.Contains(p.Note, "locally") {
		t.Fatalf("expected an honest degrade note, got %q", p.Note)
	}
}

// TestHandleTier2Funnel: a semantic ask with the frontier ON runs the funnel; the
// map+plan calls happen and the model NEVER receives the whole index — its prompt
// only contains bounded candidate text.
func TestHandleTier2Funnel(t *testing.T) {
	idx := tinyIndex(t)
	// CLI fake: map step returns indices [1], plan step returns an add_clip list.
	cli := &scriptBackend{name: "claude-cli", available: true, replies: []string{
		"[1]", // MAP window 0 selection
		`add_clip source="ring.mp4" in=00:00:20,000 out=00:00:22,000 label="cat penguin"`, // PLAN
	}}
	r := NewRouter(Options{ClaudeCLI: cli, Log: func(string, ...any) {}})

	p, err := r.Handle(context.Background(), "find every time he offered money for the cat", onlineCtx(idx), nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Tier != TierFrontier {
		t.Fatalf("tier = %v, want frontier", p.Tier)
	}
	if len(p.Actions) == 0 || p.Actions[0].Verb != VerbAddClip {
		t.Fatalf("frontier plan actions = %+v, want add_clip(s)", p.Actions)
	}
	if cli.callCount() < 2 {
		t.Fatalf("funnel should make ≥2 calls (map+plan), got %d", cli.callCount())
	}
	// The 500GB invariant: no model prompt contains the whole index. A bounded
	// candidate block holds cue text, not a 200+-line dump.
	for _, c := range cli.calls {
		if strings.Count(c.User, "\n") > 200 {
			t.Fatal("model prompt looks unbounded — funnel must window candidates")
		}
	}
	if len(p.Sources) == 0 {
		t.Fatal("frontier proposal should carry source provenance")
	}
}

// TestHandleTier2GatedOffline: even a hard semantic ask stays LOCAL when online is
// OFF (the budget/online gate), with an honest note.
func TestHandleTier2GatedOffline(t *testing.T) {
	idx := tinyIndex(t)
	cli := &fakeBackend{name: "claude-cli", available: true, reply: "[1]"}
	local := &fakeBackend{name: "local", available: true, reply: `search query="cat" mode=keyword`}
	r := NewRouter(Options{Local: local, ClaudeCLI: cli})

	cx := onlineCtx(idx)
	cx.Online = false // toggle OFF
	p, _ := r.Handle(context.Background(), "find every time he offered money for the cat", cx, nil)

	if cli.callCount() != 0 {
		t.Fatal("frontier must NOT be called when online is off")
	}
	if p.Tier == TierFrontier {
		t.Fatalf("tier = frontier despite online off; want a local/deterministic answer")
	}
	if p.Note == "" {
		t.Fatal("an offline-gated turn should carry an honest note")
	}
}

// TestHandleTier2DegradesToLocal: online ON but no frontier backend available →
// degrade to local.
func TestHandleTier2DegradesToLocal(t *testing.T) {
	idx := tinyIndex(t)
	local := &fakeBackend{name: "local", available: true, reply: `search query="cat" mode=keyword`}
	r := NewRouter(Options{Local: local}) // no CLI, no API

	p, _ := r.Handle(context.Background(), "whenever he threatens the host family", onlineCtx(idx), nil)
	if p.Tier == TierFrontier {
		t.Fatalf("with no frontier backend, tier should not be frontier; got %v", p.Tier)
	}
	if local.callCount() == 0 {
		t.Fatal("should have downgraded to the local backend")
	}
}

// TestProposeThenApply: a proposal does NOT mutate or log until Apply; Apply
// returns the actions + writes a corrections-log line; Reject discards.
func TestProposeThenApply(t *testing.T) {
	logDir := t.TempDir()
	logPath := filepath.Join(logDir, "clip.corrections.jsonl")
	local := &fakeBackend{name: "local", available: true,
		reply: `add_clip source="ring.mp4" in=00:00:20,000 out=00:00:22,000 label="cat"`}
	r := NewRouter(Options{Local: local, CorrLogPath: logPath})

	p, err := r.Handle(context.Background(), "chuck the cat bit on the end", Context{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Pre-approval: NOTHING is logged (no side effects).
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatal("corrections log must not exist before approval (no mutation pre-✓)")
	}
	if p.ID == "" {
		t.Fatal("proposal should carry an ID for apply/reject")
	}

	// Approve: actions returned + a log line written.
	acts, _, err := r.Apply(p.ID)
	if err != nil {
		t.Fatalf("Apply error = %v", err)
	}
	if len(acts) != 1 || acts[0].Verb != VerbAddClip {
		t.Fatalf("Apply actions = %+v", acts)
	}
	data, err := os.ReadFile(logPath)
	if err != nil || !strings.Contains(string(data), `"tool":"clip"`) {
		t.Fatalf("approval should append a clip corrections line; log=%q err=%v", string(data), err)
	}

	// A second Apply of the same id fails (it was consumed).
	if _, _, err := r.Apply(p.ID); err == nil {
		t.Fatal("re-applying a consumed proposal should error")
	}

	// Reject path: a fresh proposal can be discarded without error.
	p2, _ := r.Handle(context.Background(), "chuck the cat bit on the end", Context{}, nil)
	r.Reject(p2.ID)
	if _, _, err := r.Apply(p2.ID); err == nil {
		t.Fatal("applying a rejected proposal should error")
	}
}

// TestOfflineSemanticAskYieldsKeywordSearch is the offline-GUI path proof: with NO
// backends available (no local model, frontier off — exactly the default offline
// becky-clip session), a semantic "find every time he…" ask must still produce a
// usable Tier-0 keyword search (a becky-search exec command the GUI can run to
// populate results) — and the query must be the MEANINGFUL keywords, not the whole
// sentence (which would grep nothing). This is the FIX-PLAN §4 requirement.
func TestOfflineSemanticAskYieldsKeywordSearch(t *testing.T) {
	idx := tinyIndex(t)
	// No backends at all: every Available() fails → the router falls to the Tier-0
	// retrieval floor.
	r := NewRouter(Options{Log: func(string, ...any) {}})

	cx := onlineCtx(idx)
	cx.Online = false // offline, as the default becky-clip session is
	p, err := r.Handle(context.Background(), "find every time he offered money for the cat", cx, nil)
	if err != nil {
		t.Fatal(err)
	}

	// A usable search action + its becky-search exec command must be present so the
	// frontend can populate results.
	if len(p.Actions) == 0 || p.Actions[0].Verb != VerbSearch {
		t.Fatalf("offline semantic ask should propose a search action, got %+v", p.Actions)
	}
	if len(p.ExecCommands) == 0 || p.ExecCommands[0].Bin != "becky-search" {
		t.Fatalf("offline ask should attach a becky-search exec command, got %+v", p.ExecCommands)
	}

	// The query must be the keywords (money/cat), NOT the whole sentence — the
	// framing/stop words ("find", "every", "time", "he", "offered") are stripped.
	q := strings.ToLower(argString(p.Actions[0], "query"))
	for _, want := range []string{"money", "cat"} {
		if !strings.Contains(q, want) {
			t.Fatalf("keyword search query %q should contain %q", q, want)
		}
	}
	// Pure framing/stop words must be gone (proving it's not the whole sentence).
	// (Content words like "offered" legitimately survive — retrievalTerms only
	// strips framing/stop words, and the query still greps on money/cat.)
	for _, banned := range []string{"every", "time", " he "} {
		if strings.Contains(q, banned) {
			t.Fatalf("keyword search query %q should NOT contain framing word %q", q, banned)
		}
	}
	if p.Note == "" {
		t.Fatal("an offline-degraded turn should carry an honest note")
	}
}

// TestBudgetExhaustedGate: a frontier ask with an exhausted budget degrades, even
// with online ON and a frontier backend available.
func TestBudgetExhaustedGate(t *testing.T) {
	idx := tinyIndex(t)
	cli := &fakeBackend{name: "claude-cli", available: true, reply: "[1]"}
	local := &fakeBackend{name: "local", available: true, reply: `search query="cat"`}
	r := NewRouter(Options{Local: local, ClaudeCLI: cli})

	cx := onlineCtx(idx)
	cx.Budget = &Budget{MaxUSD: 1, SpentUSD: 1} // exhausted
	r.Handle(context.Background(), "find every time he offered money for the cat", cx, nil)
	if cli.callCount() != 0 {
		t.Fatal("an exhausted budget must block the frontier call")
	}
}

// scriptBackend returns a sequence of replies (one per Complete call) so the
// map→plan funnel can script distinct outputs.
type scriptBackend struct {
	name      string
	available bool
	replies   []string
	calls     []Request
	i         int
}

func (b *scriptBackend) Name() string { return b.name }
func (b *scriptBackend) Available() error {
	if b.available {
		return nil
	}
	return errUnavailable
}
func (b *scriptBackend) Complete(ctx context.Context, req Request) (string, error) {
	b.calls = append(b.calls, req)
	if b.i < len(b.replies) {
		out := b.replies[b.i]
		b.i++
		return out, nil
	}
	return "", nil
}
func (b *scriptBackend) callCount() int { return len(b.calls) }
