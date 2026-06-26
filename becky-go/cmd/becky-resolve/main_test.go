package main

import (
	"testing"

	"becky-go/internal/orchestrate"
)

// TestGemmaLadderDegrades: off-machine (no becky-validate binary) the escalation ladder returns
// an error, so the candidate stays HELD rather than being falsely concluded — degrade, not crash.
// (The mapping + corroboration are tested canonically in internal/forensic and internal/orchestrate.)
func TestGemmaLadderDegrades(t *testing.T) {
	_, err := gemmaLadder{file: "/no/such/file.mp4"}.Validate(orchestrate.Claim{Key: "person=X"}, 1)
	if err == nil {
		t.Errorf("a missing becky-validate must error so the candidate stays held, got nil")
	}
}
