//go:build llm

// intent_llm_test.go — OPT-IN live verification of FEATURE 3: the intent step
// actually driving the user's on-disk Qwen3.5-4B GGUF through llama-server.
//
// This file is behind the `llm` build tag so the default `go test ./cmd/ask/...`
// stays fast and model-free (CI / headless). Run it deliberately when a GPU +
// the model are present:
//
//	go test -tags=llm -run TestLiveQwen ./cmd/ask/...
//
// It SKIPS (not fails) when the model GGUF or llama-server is absent, so it never
// turns a clean machine red — matching the "degrade to the catalog" contract.
package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestLiveQwen_ClassifiesActionVsQuestion(t *testing.T) {
	cli := newLlamaClient(resolveIntentModel(), resolveLlamaServer(), func(f string, a ...any) {
		t.Logf(f, a...)
	})
	if err := cli.Ready(); err != nil {
		t.Skipf("Qwen3.5 model/llama-server not available, skipping live test: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// 1) Clear action with a (synthetic) target -> the model should say action.
	tgt := resolveTarget([]string{makeFile(t, t.TempDir(), "clip.mp4")})
	mi, err := classifyWithModel(ctx, cli, "transcribe this", tgt)
	if err != nil {
		t.Fatalf("live classify (action) failed: %v", err)
	}
	t.Logf("LIVE action-case model intent: %+v", mi)
	if strings.ToLower(mi.Kind) != "action" {
		t.Errorf("live model: 'transcribe this' should be action, got %q", mi.Kind)
	}

	// 2) Capability question -> the model should say question (no tool call).
	mi2, err := classifyWithModel(ctx, cli, "can becky figure out where this was filmed?", tgt)
	if err != nil {
		t.Fatalf("live classify (question) failed: %v", err)
	}
	t.Logf("LIVE question-case model intent: %+v", mi2)
	if strings.ToLower(mi2.Kind) == "action" {
		t.Errorf("live model: a capability question must not be an action, got %q", mi2.Kind)
	}
}
