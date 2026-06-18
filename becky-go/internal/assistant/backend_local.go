package assistant

import (
	"context"

	"becky-go/internal/llmlocal"
)

// backend_local.go is the Tier-1 backend: a thin adapter over the shared
// internal/llmlocal transport (the lifted cmd/ask llama-server client). It uses a
// WARM client so an interactive becky-clip session reuses one resident server +
// its KV cache across turns (R-AI §4.3). Everything degrades: Available() reports
// the missing GGUF/binary up front so the router falls to Tier 0 without a spawn.

// localBackend wraps an llmlocal.Client for the assistant's Backend seam.
type localBackend struct {
	c *llmlocal.Client
}

// newLocalBackend builds the Tier-1 backend from the resolved model + server
// paths (the GUI passes config.Load().LlamaServer and the chosen text GGUF). A
// warm client keeps the server resident for the session.
func newLocalBackend(model, server string, logf func(string, ...any)) *localBackend {
	return &localBackend{c: llmlocal.NewWarmClient(model, server, logf)}
}

func (b *localBackend) Name() string { return "local" }

// Available reports whether the local model + llama-server can run (no spawn).
func (b *localBackend) Available() error { return b.c.Available() }

// Complete sends the request to the resident llama-server with deterministic
// settings (temp 0, seed 42, thinking disabled) so the action parse is
// reproducible. The reply text is returned raw for the router to Parse.
func (b *localBackend) Complete(ctx context.Context, req Request) (string, error) {
	opts := llmlocal.Options{MaxTokens: req.MaxTokens} // temp/seed default to 0/42; thinking off
	return b.c.Chat(ctx, req.System, req.User, opts)
}

// Close shuts down the resident server (called when the becky-clip session ends).
func (b *localBackend) Close() { b.c.Close() }
