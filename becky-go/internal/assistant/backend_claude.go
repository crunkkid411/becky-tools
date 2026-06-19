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
//	claude -p --output-format json --strict-mcp-config --mcp-config {"mcpServers":{}} \
//	  --tools "" --system-prompt <rules> --model <alias> --max-turns 1
//
// (The MCP/tools/system-prompt flags make it a fast answer engine on OAuth rather
// than a slow agent; see the Complete() args comment for the measured why.)
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
		// Make `claude -p` behave as a fast ANSWER engine, not an agent. Three flags,
		// each load-bearing (measured + verified live 2026-06-19 — without them the
		// in-app chat hung at "thinking..." or returned no answer):
		//   --strict-mcp-config + empty --mcp-config: skip the user's MCP servers.
		//     A heavy Claude Code install boots its whole MCP ecosystem on every turn
		//     (~100s+ cold start → past the 90s turn timeout). becky-clip uses none.
		//   --tools "": give the model NO built-in tools. Otherwise opus tries to USE
		//     tools (Bash/grep/...) to "investigate", spends the single turn, and
		//     returns error_max_turns with no text. With no tools it just answers.
		//   --system-prompt (REPLACE, not append): drop Claude Code's coding-agent
		//     framing so becky answers as itself in plain language (append left it
		//     narrating fake tool calls). Keeps OAuth (NOT --bare, which forces a key).
		// Net: a clean answer in ~15-25s on OAuth, no API key required.
		"--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
		"--tools", "",
		"--system-prompt", req.System, // becky's role + forensic + action rules (replaces default)
		"--model", model, // opus (deep) / haiku (mid) alias
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
