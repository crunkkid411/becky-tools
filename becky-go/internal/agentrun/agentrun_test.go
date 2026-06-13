// agentrun_test.go — unit tests for the envelope parser + result mapping.
//
// Fact-Forcing-Gate self-certification:
//  1. Callers: `go test ./internal/agentrun/` runs these; they exercise
//     parseEnvelope + resultFrom + buildArgs (same package).
//  2. No-dup: no prior _test.go existed in internal/agentrun/ (the package was just
//     created); this is the first test file, not a duplicate.
//  3. Data shape: feeds a REAL-SHAPED `claude -p --output-format json` envelope
//     string (captured 2026-06-08, values trimmed) and asserts the mapped
//     AgentResult fields, including the per-model `modelUsage[*].costUSD` breakdown.
//  4. Verbatim instruction: "VERIFY (real, honest): the deterministic stages +
//     state.json resume work; `internal/agentrun` actually invokes `claude -p` and
//     parses a real result (show one real round-trip)".
package agentrun

import "testing"

// realEnvelope is the shape of an actual `claude -p "hi" --output-format json`
// result object captured on this machine 2026-06-08 (claude v2.1.169). The text +
// ids are trimmed/redacted; the STRUCTURE (field names, nesting) is verbatim.
const realEnvelope = `{"type":"result","subtype":"success","is_error":false,` +
	`"duration_ms":118092,"num_turns":1,"result":"Hey! Good to see you.",` +
	`"session_id":"d65150da-05d8-4fd1-ac26-dcda757d9643",` +
	`"total_cost_usd":0.49622625,` +
	`"modelUsage":{"claude-opus-4-8":{"inputTokens":22829,"outputTokens":794,` +
	`"costUSD":0.49622625,"contextWindow":1000000}},` +
	`"permission_denials":[],"terminal_reason":"completed"}`

func TestParseEnvelope_RealShape(t *testing.T) {
	env, raw, ok := parseEnvelope(realEnvelope)
	if !ok {
		t.Fatalf("parseEnvelope failed on a real envelope")
	}
	if len(raw) == 0 {
		t.Errorf("raw span should be non-empty")
	}
	res := resultFrom(env, raw, nil)

	if res.Subtype != "success" {
		t.Errorf("subtype = %q, want success", res.Subtype)
	}
	if res.IsError {
		t.Errorf("is_error = true, want false")
	}
	if res.SessionID != "d65150da-05d8-4fd1-ac26-dcda757d9643" {
		t.Errorf("session_id = %q", res.SessionID)
	}
	if res.CostUSD != 0.49622625 {
		t.Errorf("total_cost_usd = %v, want 0.49622625", res.CostUSD)
	}
	if res.NumTurns != 1 {
		t.Errorf("num_turns = %d, want 1", res.NumTurns)
	}
	if res.Result != "Hey! Good to see you." {
		t.Errorf("result = %q", res.Result)
	}
	// Q6 resolution: the per-model cost breakdown lives under modelUsage[*].costUSD.
	got, ok := res.ModelCostUSD["claude-opus-4-8"]
	if !ok {
		t.Fatalf("ModelCostUSD missing claude-opus-4-8 (modelUsage not parsed)")
	}
	if got != 0.49622625 {
		t.Errorf("modelUsage costUSD = %v, want 0.49622625", got)
	}
}

func TestParseEnvelope_LeadingNoise(t *testing.T) {
	// The CLI may print a warning line before the JSON; parseEnvelope must recover
	// the outermost {...} span.
	noisy := "Some CLI warning about an update\n" + realEnvelope
	env, _, ok := parseEnvelope(noisy)
	if !ok {
		t.Fatalf("parseEnvelope should recover JSON after a leading noise line")
	}
	if env.Subtype != "success" {
		t.Errorf("subtype after noise = %q, want success", env.Subtype)
	}
}

func TestParseEnvelope_Garbage(t *testing.T) {
	if _, _, ok := parseEnvelope("not json at all"); ok {
		t.Errorf("parseEnvelope should fail on non-JSON")
	}
	if _, _, ok := parseEnvelope(""); ok {
		t.Errorf("parseEnvelope should fail on empty input")
	}
}

func TestBuildArgs_Shape(t *testing.T) {
	// A representative S5-build spec: verify the managed flags land in argv and the
	// prompt does NOT (it goes on stdin).
	spec := AgentSpec{
		PromptStdin:      "build me a tool",
		SystemPromptFile: "BUILD-AGENT-BRIEFING.md",
		Model:            "sonnet",
		FallbackModel:    "haiku",
		MaxTurns:         40,
		MaxBudgetUSD:     5,
		AllowedTools:     []string{"Read", "Edit", "Bash(go *)"},
		AddDirs:          []string{`X:\AI-2\becky-tools\becky-go`},
		PermissionMode:   "acceptEdits",
		SessionID:        "fixed-uuid",
		SettingSources:   "project",
	}
	args := spec.buildArgs("stream-json", true)

	wantContains := []string{
		"-p", "--output-format", "stream-json", "--verbose",
		"--append-system-prompt-file", "BUILD-AGENT-BRIEFING.md",
		"--model", "sonnet", "--fallback-model", "haiku",
		"--permission-mode", "acceptEdits",
		"--allowedTools", "Read,Edit,Bash(go *)",
		"--add-dir", `X:\AI-2\becky-tools\becky-go`,
		"--max-turns", "40", "--max-budget-usd", "5",
		"--setting-sources", "project",
		"--session-id", "fixed-uuid",
	}
	for _, w := range wantContains {
		if !containsArg(args, w) {
			t.Errorf("args missing %q; got %v", w, args)
		}
	}
	// The prompt body must never appear in argv.
	if containsArg(args, "build me a tool") {
		t.Errorf("prompt leaked into argv; must be stdin only")
	}
	// Resume should take precedence over a fixed session id when both are present.
	spec.Resume = "resume-uuid"
	if a := spec.buildArgs("json", false); containsArg(a, "--session-id") {
		t.Errorf("when Resume is set, --session-id should be omitted in favor of --resume")
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
