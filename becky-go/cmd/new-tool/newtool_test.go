// newtool_test.go — unit tests for the factory's pure deterministic logic: slug
// derivation, input-kind guessing, gate-approval parsing, the gate auto-pass policy,
// the OpenRouter free-filter, the cheap-model JSON extractor, and the idempotent
// build-all-tools.bat TOOLS-line edit. No network, no model.
//
// Fact-Forcing-Gate self-certification:
//  1. Callers: `go test ./cmd/new-tool/` runs these.
//  2. No-dup: first _test.go in cmd/new-tool/; not a duplicate.
//  3. Data shape: tests pure functions with synthetic inputs; writes a temp .bat for
//     the TOOLS-line test (synthetic content, not production data).
//  4. Verbatim instruction: "VERIFY (real, honest): the deterministic stages +
//     state.json resume work".
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeriveSlug(t *testing.T) {
	cases := map[string]string{
		"I need a tool that counts shot changes in a clip": "becky-counts-shot-changes",
		"blur faces in a video":                            "becky-blur-faces-in",
		"":                                                 "becky-tool",
	}
	for in, want := range cases {
		if got := deriveSlug(in); got != want {
			t.Errorf("deriveSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeSlug(t *testing.T) {
	cases := map[string]string{
		"Becky Redact":     "becky-redact",
		"redact":           "becky-redact",
		"becky-framematch": "becky-framematch",
		"  ":               "",
	}
	for in, want := range cases {
		if got := sanitizeSlug(in); got != want {
			t.Errorf("sanitizeSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGuessInputKind(t *testing.T) {
	cases := map[string]string{
		"blur faces in a video clip":  "video",
		"transcribe this audio":       "audio",
		"compare two photos":          "image",
		"scrape a url":                "url",
		"summarize a transcript json": "json",
		"do something vague":          "text",
	}
	for in, want := range cases {
		if got := guessInputKind(in); got != want {
			t.Errorf("guessInputKind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseApprovals(t *testing.T) {
	m := parseApprovals("gateA,gateC")
	if !m["A"] || m["B"] || !m["C"] {
		t.Errorf("parseApprovals(gateA,gateC) = %v, want A+C only", m)
	}
}

func TestGateDecision(t *testing.T) {
	o := &orchestrator{approve: map[string]bool{}}

	// Gate A auto-passes only on new_tool & conf>=0.7 & research-ok.
	s := &State{
		Redundancy: &Redundancy{Verdict: "new_tool", Confidence: 0.8},
		Research:   &Research{QualityOK: true},
	}
	if ok, _ := o.gateDecision(s, "A"); !ok {
		t.Errorf("Gate A should auto-pass on new_tool/0.8/research-ok")
	}
	// Low confidence -> needs human.
	s.Redundancy.Confidence = 0.5
	if ok, _ := o.gateDecision(s, "A"); ok {
		t.Errorf("Gate A should NOT auto-pass at confidence 0.5")
	}
	// Gate B/C never auto-pass without --yes or explicit approval.
	if ok, _ := o.gateDecision(s, "B"); ok {
		t.Errorf("Gate B should require --yes/--approve")
	}
	o.yes = true
	if ok, _ := o.gateDecision(s, "B"); !ok {
		t.Errorf("Gate B should pass with --yes")
	}
	// Explicit approval overrides.
	o.yes = false
	o.approve["C"] = true
	if ok, _ := o.gateDecision(s, "C"); !ok {
		t.Errorf("Gate C should pass with explicit --approve")
	}
}

func TestFilterFreeNewest(t *testing.T) {
	models := []orModel{
		{ID: "a/paid", Created: 100, Pricing: struct {
			Prompt string `json:"prompt"`
		}{Prompt: "0.001"}},
		{ID: "b/free:free", Created: 200},
		{ID: "c/zero", Created: 300, Pricing: struct {
			Prompt string `json:"prompt"`
		}{Prompt: "0"}},
	}
	free := filterFreeNewest(models)
	if len(free) != 2 {
		t.Fatalf("expected 2 free models, got %d", len(free))
	}
	// Newest first: c (300) before b (200).
	if free[0].ID != "c/zero" || free[1].ID != "b/free:free" {
		t.Errorf("free not sorted newest-first: %v", free)
	}
}

func TestExtractJSON(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"```json\n{\"a\":1}\n```", `{"a":1}`},
		{"prose before {\"x\":2} prose after", `{"x":2}`},
		{"[1,2,3]", "[1,2,3]"},
		{"no json here", ""},
	}
	for _, c := range cases {
		got, ok := extractJSON(c.in)
		if c.want == "" {
			if ok {
				t.Errorf("extractJSON(%q) should fail, got %q", c.in, got)
			}
			continue
		}
		if !ok || got != c.want {
			t.Errorf("extractJSON(%q) = %q (ok=%v), want %q", c.in, got, ok, c.want)
		}
	}
}

func TestAppendToToolsLine(t *testing.T) {
	dir := t.TempDir()
	bat := filepath.Join(dir, "build-all-tools.bat")
	content := "@echo off\nset TOOLS=transcribe cut vad\nfor %%T in (%TOOLS%) do echo %%T\n"
	if err := os.WriteFile(bat, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// First append adds the token.
	added, err := appendToToolsLine(bat, "newtool")
	if err != nil || !added {
		t.Fatalf("first append: added=%v err=%v", added, err)
	}
	data, _ := os.ReadFile(bat)
	if !containsWord(string(data), "newtool") {
		t.Errorf("TOOLS line missing newtool after append: %s", data)
	}
	// Second append is idempotent (no change).
	added2, err := appendToToolsLine(bat, "newtool")
	if err != nil || added2 {
		t.Errorf("second append should be idempotent: added=%v err=%v", added2, err)
	}
}

func TestHasRealTunableSurface(t *testing.T) {
	// Placeholder-only spec -> nothing to tune.
	sp := &Spec{
		TunableSurface: []string{"(declare per tool: thresholds)"},
		AnswerKeyFacts: []string{"(declare per tool: facts)"},
	}
	if hasRealTunableSurface(sp) {
		t.Errorf("placeholder-only spec should have no real tunable surface")
	}
	// Real surface + real fact -> tunable.
	sp = &Spec{
		TunableSurface: []string{"--threshold 0.45"},
		AnswerKeyFacts: []string{"shot change at 0:12"},
	}
	if !hasRealTunableSurface(sp) {
		t.Errorf("real surface+fact should be tunable")
	}
}
