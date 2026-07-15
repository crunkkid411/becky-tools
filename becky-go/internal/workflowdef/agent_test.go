package workflowdef

import "testing"

// An agent step is a first-class step kind: it parses, validates, and reports its
// kind/name like tool/verb/merge — so a recipe can carry an OPT-IN AI step.
func TestAgentStep_ParsesAndClassifies(t *testing.T) {
	r, err := Parse([]byte(`{
		"name": "watch-and-reason",
		"phrases": ["watch and reason"],
		"steps": [
			{ "tool": "becky-transcribe" },
			{ "agent": "claude-code", "prompt": "Summarize who and where." }
		]
	}`))
	if err != nil {
		t.Fatalf("Parse valid agent recipe: %v", err)
	}
	if got := r.Steps[1].Kind(); got != "agent" {
		t.Errorf("agent step Kind() = %q, want %q", got, "agent")
	}
	if got := r.Steps[1].Name(); got != "claude-code" {
		t.Errorf("agent step Name() = %q, want %q", got, "claude-code")
	}
	if got := r.Steps[1].Prompt; got != "Summarize who and where." {
		t.Errorf("agent step Prompt = %q, want the instruction", got)
	}
}

// A step that sets BOTH a tool and an agent is invalid — exactly one kind per step.
func TestAgentStep_RejectsMixedKind(t *testing.T) {
	_, err := Parse([]byte(`{
		"name": "bad",
		"steps": [ { "tool": "becky-transcribe", "agent": "claude-code" } ]
	}`))
	if err == nil {
		t.Fatal("expected a tool+agent step to be rejected, got nil error")
	}
}

// The engine runs an agent step through the injected executor exactly like any other
// step, and its output is returned — proving the executor can branch on Kind()=="agent".
func TestAgentStep_RunsThroughExecutor(t *testing.T) {
	r, err := Parse([]byte(`{
		"name": "reason",
		"steps": [ { "agent": "claude-code", "prompt": "answer" } ]
	}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var sawAgent bool
	results := r.Run(Facts{}, func(step Step, _ Facts) (string, error) {
		if step.Kind() == "agent" {
			sawAgent = true
			return "AGENT-RAN", nil
		}
		return "", nil
	})
	if !sawAgent {
		t.Fatal("executor was never handed the agent step")
	}
	if len(results) != 1 || results[0].Output != "AGENT-RAN" {
		t.Fatalf("agent step output = %+v, want one result with Output=AGENT-RAN", results)
	}
}
