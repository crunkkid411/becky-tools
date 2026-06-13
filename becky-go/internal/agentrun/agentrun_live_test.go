// agentrun_live_test.go — a LIVE integration test that actually invokes `claude -p`
// through agentrun.Run and asserts the parsed envelope. It is gated behind the
// AGENTRUN_LIVE=1 env var so the normal `go test` run does NOT spend Agent-SDK
// credit; run it explicitly to prove the real round-trip.
//
// Fact-Forcing-Gate self-certification:
//  1. Callers: `AGENTRUN_LIVE=1 go test ./internal/agentrun/ -run TestLiveRoundTrip`.
//  2. No-dup: complements agentrun_test.go's offline unit tests with one live test;
//     it does not duplicate them (different concern: real CLI invocation).
//  3. Data shape: calls agentrun.Run with a trivial prompt; asserts the parsed
//     AgentResult (Subtype/SessionID/CostUSD/ModelCostUSD). No data files.
//  4. Verbatim instruction: "internal/agentrun actually invokes `claude -p` and
//     parses a real result (show one real round-trip)".
package agentrun

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLiveRoundTrip(t *testing.T) {
	if os.Getenv("AGENTRUN_LIVE") != "1" {
		t.Skip("set AGENTRUN_LIVE=1 to run the live claude -p round-trip (spends Agent-SDK credit)")
	}
	if ResolveBin() == "" {
		t.Skip("claude CLI not on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	res, err := Run(ctx, AgentSpec{
		PromptStdin: "Reply with exactly the word: pong",
		WorkDir:     os.TempDir(), // neutral dir: no project CLAUDE.md auto-discovery
	})
	if err != nil {
		t.Fatalf("live Run failed: %v", err)
	}
	if res.IsError || res.Subtype != "success" {
		t.Fatalf("agent reported error: subtype=%s is_error=%v", res.Subtype, res.IsError)
	}
	if res.SessionID == "" {
		t.Errorf("expected a session_id")
	}
	t.Logf("LIVE result=%q session=%s cost=$%.5f models=%v", res.Result, res.SessionID, res.CostUSD, res.ModelCostUSD)
}
