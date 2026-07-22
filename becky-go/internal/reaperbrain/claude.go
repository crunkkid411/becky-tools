package reaperbrain

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	// EnvClaudeBin overrides where the claude CLI lives (else PATH).
	EnvClaudeBin = "BECKY_CLAUDE_BIN"
	// EnvClaudeModel overrides the model alias passed to claude -p.
	EnvClaudeModel = "BECKY_CLAUDE_MODEL"
	// DefaultClaudeModel: Sonnet is fast enough for chat turns and covered by
	// the Max plan like every other Anthropic model.
	DefaultClaudeModel = "sonnet"
)

// ClaudeBackend answers chat turns by shelling out to the Claude Code CLI in
// print mode (`claude -p`), which rides Jordan's Max OAuth session — the
// already-paid path, no API key, no per-token bill. This is the launcher-CLI
// method of reaching an Anthropic model (never a paid API — CLAUDE.md).
type ClaudeBackend struct {
	Bin      string // resolved claude executable
	ModelID  string
	resolved error // nil when Bin is usable

	// Run executes the CLI and returns stdout. Injectable for tests; nil uses
	// the real implementation.
	Run func(ctx context.Context, bin string, args []string, stdin string) (string, error)
}

// NewClaudeBackend resolves the claude binary + model from env/PATH. It always
// returns a backend; a resolution problem surfaces as a descriptive error from
// Complete (degrade, never crash).
func NewClaudeBackend() *ClaudeBackend {
	b := &ClaudeBackend{ModelID: DefaultClaudeModel}
	if m := strings.TrimSpace(os.Getenv(EnvClaudeModel)); m != "" {
		b.ModelID = m
	}
	if e := strings.TrimSpace(os.Getenv(EnvClaudeBin)); e != "" {
		if _, err := os.Stat(e); err == nil {
			b.Bin = e
			return b
		}
		b.resolved = fmt.Errorf("claude CLI not found at %s (%s)", e, EnvClaudeBin)
		return b
	}
	for _, name := range []string{"claude", "claude.exe", "claude.cmd"} {
		if p, err := exec.LookPath(name); err == nil && p != "" {
			b.Bin = p
			return b
		}
	}
	b.resolved = fmt.Errorf("claude CLI not found on PATH (install Claude Code, or set %s)", EnvClaudeBin)
	return b
}

func (b *ClaudeBackend) Name() string  { return "claude" }
func (b *ClaudeBackend) Model() string { return "claude/" + b.ModelID }

// Ready reports whether the backend can serve at all (used by the CLI plan).
func (b *ClaudeBackend) Ready() error { return b.resolved }

// Complete flattens the conversation to one prompt and runs
// `claude -p --model <m> --output-format text` with the prompt on stdin.
func (b *ClaudeBackend) Complete(ctx context.Context, messages []Message) (string, error) {
	if b.resolved != nil {
		return "", b.resolved
	}
	run := b.Run
	if run == nil {
		run = runClaude
	}
	args := []string{"-p", "--model", b.ModelID, "--output-format", "text"}
	out, err := run(ctx, b.Bin, args, FlattenMessages(messages))
	if err != nil {
		return "", fmt.Errorf("claude CLI: %w", err)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", fmt.Errorf("claude CLI returned no text")
	}
	return out, nil
}

// runClaude is the real CLI invocation. It runs in the OS temp dir ON PURPOSE:
// print-mode claude loads project context from the working directory, and the
// brain must answer as a plain chat model, not wake up inside whatever repo the
// brain happened to be started from.
func runClaude(ctx context.Context, bin string, args []string, stdin string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = os.TempDir()
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		detail := firstLine(strings.TrimSpace(errb.String()))
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("%s", detail)
	}
	return out.String(), nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
