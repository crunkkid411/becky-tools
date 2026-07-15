package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"becky-go/internal/workflowdef"
)

// runSelfTest proves the runnable engine offline (no becky-*.exe, no claude): a recipe
// with tool + conditional + OPT-IN agent + merge steps runs in order, skips a step whose
// `when` is false, hands the agent the prior tool output, and bundles the tools in merge.
// Exits 0 on PASS. This is the one-command proof for the handoff.
func runSelfTest() int {
	origTool, origAgent := runToolFn, runAgentFn
	defer func() { runToolFn, runAgentFn = origTool, origAgent }()

	var toolCalls, agentCalls int
	runToolFn = func(_ context.Context, tool, target string) (string, error) {
		toolCalls++
		return fmt.Sprintf(`{"tool":%q,"target":%q,"ok":true}`, tool, target), nil
	}
	runAgentFn = func(_ context.Context, _, prompt, _ string, _ float64) (string, error) {
		agentCalls++
		if !strings.Contains(prompt, "becky-transcribe") {
			return "", fmt.Errorf("agent prompt did not include the prior tool output")
		}
		return "AGENT REASONED OVER THE TOOL OUTPUT", nil
	}

	recipe, err := workflowdef.Parse([]byte(`{
		"name": "selftest",
		"steps": [
			{ "tool": "becky-transcribe" },
			{ "tool": "becky-diarize", "when": "speakers > 1" },
			{ "agent": "claude-code", "prompt": "reason over this" },
			{ "merge": "result" }
		]
	}`))
	if err != nil {
		return fail("parse recipe: %v", err)
	}

	// One speaker -> diarize SKIPS; the agent still runs and sees the transcribe output.
	rn := &runner{target: "clip.mp4"}
	res1 := recipe.Run(workflowdef.Facts{"speakers": 1}, func(s workflowdef.Step, f workflowdef.Facts) (string, error) {
		return rn.run(context.Background(), s, f)
	})
	names := workflowdef.ExecutedNames(res1)
	if contains(names, "becky-diarize") {
		return fail("diarize should SKIP on one speaker; ran: %v", names)
	}
	if !contains(names, "claude-code") {
		return fail("agent step did not run; ran: %v", names)
	}
	if agentCalls != 1 {
		return fail("agent calls = %d, want exactly 1 (opt-in, once)", agentCalls)
	}
	// The merge step (last) must bundle the transcribe tool output.
	last := res1[len(res1)-1]
	if last.Err != nil || !strings.Contains(last.Output, "becky-transcribe") {
		return fail("merge did not bundle the tool output: %q (err %v)", last.Output, last.Err)
	}

	// Three speakers -> diarize RUNS.
	rn2 := &runner{target: "clip.mp4"}
	res2 := recipe.Run(workflowdef.Facts{"speakers": 3}, func(s workflowdef.Step, f workflowdef.Facts) (string, error) {
		return rn2.run(context.Background(), s, f)
	})
	if !contains(workflowdef.ExecutedNames(res2), "becky-diarize") {
		return fail("diarize should RUN on three speakers; ran: %v", workflowdef.ExecutedNames(res2))
	}

	// A recipe with ZERO agent steps must make ZERO agent calls (the anti-Archon guarantee).
	agentCalls = 0
	deterministic, _ := workflowdef.Parse([]byte(`{"name":"det","steps":[{"tool":"becky-transcribe"},{"merge":"r"}]}`))
	deterministic.Run(workflowdef.Facts{}, func(s workflowdef.Step, f workflowdef.Facts) (string, error) {
		return (&runner{target: "x"}).run(context.Background(), s, f)
	})
	if agentCalls != 0 {
		return fail("a recipe with no agent step made %d agent calls, want 0", agentCalls)
	}

	fmt.Println("PASS: becky-workflow (tool steps, conditional skip, opt-in agent sees tool output, merge bundles, zero-agent recipe spends nothing)")
	return 0
}

func fail(format string, a ...any) int {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", a...)
	return 1
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
