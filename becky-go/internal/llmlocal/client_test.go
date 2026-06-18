package llmlocal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAvailable covers the up-front degrade checks: every missing-dependency
// case must return a descriptive error (never panic), and a fully-resolvable
// pair must report nil. These are the gates the assistant routes around.
func TestAvailable(t *testing.T) {
	dir := t.TempDir()
	model := filepath.Join(dir, "model.gguf")
	server := filepath.Join(dir, "llama-server.exe")
	if err := os.WriteFile(model, []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(server, []byte("bin"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		model   string
		server  string
		wantErr string // substring; "" means expect nil
	}{
		{"both present", model, server, ""},
		{"no model path", "", server, "model path not configured"},
		{"no server path", model, "", "llama-server path not configured"},
		{"model missing", filepath.Join(dir, "nope.gguf"), server, "not found"},
		{"server missing", model, filepath.Join(dir, "nope.exe"), "not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewClient(tt.model, tt.server, nil)
			err := c.Available()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Available() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Available() = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

// TestChatDegradesWhenUnavailable confirms Chat returns the Available() error
// (no spawn attempt) when the model/binary are absent — the assistant's Tier-1
// degrade hinges on this.
func TestChatDegradesWhenUnavailable(t *testing.T) {
	c := NewClient(filepath.Join(t.TempDir(), "missing.gguf"), filepath.Join(t.TempDir(), "missing.exe"), nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := c.Chat(ctx, "sys", "user", Options{})
	if err == nil {
		t.Fatal("Chat() = nil error, want degrade error when model absent")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Chat() error = %v, want a 'not found' degrade", err)
	}
}

// TestCloseSafeOnSpawnPerCall verifies Close is a no-op (and doesn't panic) on a
// non-warm client, and is idempotent.
func TestCloseSafeOnSpawnPerCall(t *testing.T) {
	c := NewClient("m", "s", nil)
	c.Close()
	c.Close() // idempotent
}

// TestWarmClientFlag confirms the warm constructor sets the resident flag so the
// session reuses one server.
func TestWarmClientFlag(t *testing.T) {
	if NewClient("m", "s", nil).warm {
		t.Fatal("NewClient should be spawn-per-call (warm=false)")
	}
	if !NewWarmClient("m", "s", nil).warm {
		t.Fatal("NewWarmClient should be resident (warm=true)")
	}
}

// TestOptionDefaults documents the deterministic defaults the request builder
// relies on (nil Temperature → 0.0, zero MaxTokens → 256, EnableThinking false).
func TestOptionDefaults(t *testing.T) {
	o := Options{}
	if o.Temperature != nil {
		t.Fatal("zero Options.Temperature should be nil (→ 0.0 default)")
	}
	if o.Seed != nil {
		t.Fatal("zero Options.Seed should be nil (→ 42 default)")
	}
	if o.MaxTokens != 0 {
		t.Fatal("zero MaxTokens should stay 0 until post() defaults it to 256")
	}
	if o.EnableThinking {
		t.Fatal("EnableThinking must default false so content is not stripped")
	}
}
