package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// backend_claude.go is Tier-2a: the `claude` CLI frontier backend running on
// Jordan's Max-plan OAuth (no API key needed). The exact non-interactive
// invocation was verified against `claude --help` on his box (2026-06-18, CLI
// 2.x): print mode + JSON output + an appended system prompt, with the (possibly
// large) user/candidate block piped on STDIN to dodge argv length limits.
//
//	claude -p --output-format json --model <alias> --append-system-prompt <rules> --max-turns 1
//
// --output-format json wraps the reply as {"type":"result","result":"<text>",…};
// we read .result. Aliases (opus/haiku) are used (durable across snapshot bumps,
// no model-id re-verification needed). Auth wall → return the plain reason; the
// router degrades to the API or local tier. Never authenticate from in here.

// claudeBin is the CLI name resolved on PATH (the npm shim → claude.exe).
const claudeBin = "claude"

// claudeCLIBackend drives `claude -p`. model is a CLI alias ("opus" deep /
// "haiku" mid). bin is overridable for tests (default "claude").
type claudeCLIBackend struct {
	bin   string
	model string
}

// newClaudeCLIBackend builds the Tier-2a backend with the given model alias.
func newClaudeCLIBackend(model string) *claudeCLIBackend {
	if model == "" {
		model = "opus"
	}
	return &claudeCLIBackend{bin: claudeBin, model: model}
}

func (b *claudeCLIBackend) Name() string { return "claude-cli" }

// Available reports whether the claude binary is on PATH.
func (b *claudeCLIBackend) Available() error {
	bin := b.bin
	if bin == "" {
		bin = claudeBin
	}
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("claude CLI not on PATH: %w", err)
	}
	return nil
}

// Complete runs one print-mode call and returns the model's text. The system
// prompt is APPENDED (so the CLI's own tool-use scaffolding stays intact); the
// user payload is piped on STDIN. A JSON schema, when provided, asks the CLI for
// schema-validated structured output. Honors ctx (CommandContext).
func (b *claudeCLIBackend) Complete(ctx context.Context, req Request) (string, error) {
	bin := b.bin
	if bin == "" {
		bin = claudeBin
	}
	model := b.model
	if model == "" {
		model = "opus"
	}

	args := []string{
		"-p",                      // print mode, non-interactive, exit
		"--output-format", "json", // single JSON result envelope
		"--model", model, // opus (deep) / haiku (mid) alias
		"--append-system-prompt", req.System, // becky forensic + action rules
		"--max-turns", "1", // one shot, no agentic loop
	}
	if req.JSONSchema != "" {
		args = append(args, "--json-schema", req.JSONSchema)
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = strings.NewReader(req.User) // candidate block + ask via stdin
	cmd.Env = nonInteractiveEnv()           // strip inherited Claude Code session vars
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("claude -p: %w", err)
	}
	return parseCLIEnvelope(out), nil
}

// parseCLIEnvelope reads the {type,result,…} envelope --output-format json emits,
// returning .result. If the shape shifts, it falls back to the raw output so the
// router can still try to Parse it (degrade, never crash).
func parseCLIEnvelope(out []byte) string {
	var env struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(out, &env); err == nil && env.Result != "" {
		return env.Result
	}
	return string(out)
}

// nonInteractiveEnv returns the current environment with any inherited Claude
// Code session markers removed, so a `claude -p` child launched from inside
// becky-clip behaves as a fresh print-mode call on the user's own OAuth rather
// than inheriting an enclosing agent session. (R-AI §4.4 env note.)
func nonInteractiveEnv() []string {
	drop := map[string]bool{
		"CLAUDECODE":             true,
		"CLAUDE_CODE_ENTRYPOINT": true,
		"CLAUDE_CODE_SSE_PORT":   true,
	}
	src := os.Environ()
	out := make([]string, 0, len(src))
	for _, kv := range src {
		eq := strings.IndexByte(kv, '=')
		if eq > 0 && drop[kv[:eq]] {
			continue
		}
		out = append(out, kv)
	}
	return out
}
