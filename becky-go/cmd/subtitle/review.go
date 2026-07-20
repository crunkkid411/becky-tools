package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"becky-go/internal/proc"
	"becky-go/internal/subs"
)

// The pass-2 caption reviewer runs on Sonnet 5 through the `claude` CLI, which
// is how the rest of becky reaches Claude (internal/assistant/backend_claude.go)
// — it uses the existing OAuth login, so there is no API key to configure.
//
// The flags are the ones that comment documents as load-bearing: without them a
// heavy Claude Code install boots its whole MCP ecosystem per call, or the model
// burns its single turn trying to use tools and returns no text.
const claudeBin = "claude"

// claudeModel returns a subs.ModelFunc backed by `claude -p`.
func claudeModel(model string, verbose bool) subs.ModelFunc {
	if model == "" {
		model = "sonnet"
	}
	return func(ctx context.Context, prompt string) (string, error) {
		args := []string{
			"-p",
			"--output-format", "json",
			"--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
			"--system-prompt", "You regroup caption word indices and reply with JSON only. No prose, no markdown fence.",
			"--model", model,
			"--tools", "",
			"--max-turns", "1",
		}
		cmd := exec.CommandContext(ctx, claudeBin, args...)
		proc.NoWindow(cmd)
		cmd.Stdin = strings.NewReader(prompt)
		var out, errb bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errb
		if err := cmd.Run(); err != nil {
			msg := strings.TrimSpace(errb.String())
			if msg == "" {
				msg = err.Error()
			}
			return "", fmt.Errorf("%s: %s", model, firstLine(msg))
		}
		return extractResult(out.String()), nil
	}
}

// extractResult unwraps `claude -p --output-format json`'s envelope. If the
// output is not that envelope, it is returned as-is so a plain-text reply still
// works.
func extractResult(s string) string {
	var env struct {
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &env); err == nil && env.Result != "" {
		return env.Result
	}
	return s
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// haveClaude reports whether the `claude` CLI is reachable, so --review can
// degrade with a clear reason instead of failing 90 times.
func haveClaude() bool {
	_, err := exec.LookPath(claudeBin)
	return err == nil
}

// noteReviewSkipped writes the reason to stderr so a silent fallback is never
// mistaken for a successful review.
func noteReviewSkipped(reason string) {
	fmt.Fprintf(os.Stderr, "caption review skipped (%s) - captions will break on pacing only\n", reason)
}
