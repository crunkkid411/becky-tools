// Package agentrun is the ONE shared helper for invoking a headless Claude Code
// agent ("claude -p") from Go. Before this package, the only in-repo proof of the
// invocation lived inline in cmd/review/backend.go (claudeCodeBackend). The
// becky-new-tool factory (cmd/new-tool) drives a headless agent in its S4-spec and
// S5-build stages, and becky-review can be refactored onto this same helper, so the
// invocation shape is extracted here once instead of copied per caller.
//
// Fact-Forcing-Gate self-certification for this file:
//  1. Callers: cmd/new-tool's S4 (spec authoring, optional) and S5 (build via Ralph
//     loop) call agentrun.Run / agentrun.Stream; cmd/review/backend.go MAY be
//     refactored onto it. Invoked from the becky-new-tool orchestrator process.
//  2. No-dup: checked cmd/review/backend.go (the only existing claude -p caller) and
//     internal/* (no existing agent-invocation package); this consolidates that one
//     inline implementation rather than adding a parallel one. SPEC-BECKY-NEW-TOOL.md
//     §5.4 mandates this exact package.
//  3. Data shape: reads an AgentSpec (prompt on stdin, flags) + the live `claude`
//     CLI's stdout; emits a typed AgentResult{Result, StructuredOutput, SessionID,
//     CostUSD, ModelCostUSD, IsError, Subtype, NumTurns, Events}.
//  4. Verbatim instruction: "Extract a shared `internal/agentrun/` helper for the
//     headless Claude Code invocation per the spec's verified shape (prompt via STDIN
//     not argv, `--output-format json`, fixed `--session-id` for `--resume`,
//     `--permission-mode acceptEdits`, tight `--allowedTools`, `--add-dir becky-go`,
//     budget flags)."
//
// VERIFIED 2026-06-08 against `claude` v2.1.169 on this machine by running
// `claude -p "hi" --output-format json`. The envelope is a single JSON object with
// top-level: type, subtype, is_error, result, session_id, total_cost_usd, num_turns,
// duration_ms, and — the field the spec flagged as unverified — a per-model cost
// breakdown under `modelUsage` (e.g. modelUsage["claude-opus-4-8"].costUSD). With
// --json-schema the validated object arrives in `structured_output`. All flags used
// below (--add-dir, --session-id, --permission-mode, --append-system-prompt[-file],
// --max-turns, --max-budget-usd, --fallback-model, --output-format, --allowedTools,
// --resume, --setting-sources) were confirmed present in `claude --help`.
package agentrun

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// AgentSpec is the full, typed description of one headless agent invocation. Only
// the fields a caller sets are passed through; empty fields are omitted from argv
// so the CLI defaults apply.
type AgentSpec struct {
	// PromptStdin is the user prompt. It is delivered on STDIN, NOT as a -p argument:
	// on Windows the CLI is a claude.cmd/claude.ps1 shim and a large multi-line -p arg
	// gets mangled through cmd.exe (the becky-review lesson, still valid on v2.1.169).
	PromptStdin string

	// SystemPromptFile, if set, is appended as the system layer via
	// --append-system-prompt-file (e.g. BUILD-AGENT-BRIEFING.md for the build stage).
	SystemPromptFile string
	// SystemPrompt is an inline alternative appended via --append-system-prompt. If
	// both are set, the file wins (it is the documented build-system layer).
	SystemPrompt string

	// Model selects the build/spec model explicitly (a cost lever). Empty = CLI default.
	Model string
	// FallbackModel enables --fallback-model so an overloaded/retired primary auto-falls back.
	FallbackModel string

	// MaxTurns is a hard stop on agentic turns (--max-turns). 0 = no cap.
	MaxTurns int
	// MaxBudgetUSD is a hard per-call spend stop (--max-budget-usd). 0 = no cap.
	MaxBudgetUSD float64

	// AllowedTools is the tight allowlist (--allowedTools). Empty = CLI default (asks).
	AllowedTools []string
	// AddDirs grants file access to extra roots (--add-dir), e.g. the becky-go build root.
	AddDirs []string

	// PermissionMode (--permission-mode), e.g. "acceptEdits" so the agent can write the
	// new package + run go/ffmpeg/the new binary without a prompt per action.
	PermissionMode string

	// SessionID fixes the session (--session-id) so a later retry resumes the SAME
	// conversation via Resume below. Pass a fixed UUID per factory run.
	SessionID string
	// Resume, if set, resumes an existing session by id (--resume). Used by the
	// S5<->S6 build/test loop to feed failing assertions back to the same agent.
	Resume string

	// SettingSources controls which setting layers load (--setting-sources), e.g.
	// "project" to pick up a project hook config (the Fact-Forcing-Gate) without
	// --bare (which would skip hooks AND OAuth). Empty = CLI default.
	SettingSources string

	// JSONSchema, if set, requests typed structured output (--json-schema). The
	// validated object then arrives in AgentResult.StructuredOutput instead of Result.
	JSONSchema string

	// WorkDir is the process working directory. The becky-review pattern runs from a
	// neutral dir (os.TempDir) so the CLI does not auto-discover a project CLAUDE.md
	// and go conversational; the build stage instead sets this to the build root and
	// relies on SettingSources to control loading. Empty = inherit the parent's cwd.
	WorkDir string

	// Env, if non-nil, replaces the child environment (else the parent's is inherited).
	Env []string

	// ExtraArgs are passed through verbatim after the managed flags (escape hatch for
	// flags this struct does not model yet, e.g. --include-partial-messages).
	ExtraArgs []string
}

// AgentResult is the parsed outcome of one invocation. It mirrors the verified
// envelope fields so callers never re-parse raw JSON.
type AgentResult struct {
	Result           string             // the model's free-text result (empty when JSONSchema is used)
	StructuredOutput json.RawMessage    // the validated object when JSONSchema is set
	SessionID        string             // the session id (for --resume on retry)
	Subtype          string             // "success" on success
	IsError          bool               // the CLI's is_error flag
	CostUSD          float64            // total_cost_usd (scalar, whole call)
	ModelCostUSD     map[string]float64 // per-model cost from `modelUsage[*].costUSD` (Q6: VERIFIED present)
	NumTurns         int                // num_turns
	DurationMS       int64              // duration_ms
	Raw              json.RawMessage    // the full envelope, for auditing
	Events           []json.RawMessage  // stream-json events (Stream only)
}

// envelope is the subset of the `claude -p --output-format json` result object we
// decode. Field names verified 2026-06-08 against claude v2.1.169.
type envelope struct {
	Type             string          `json:"type"`
	Subtype          string          `json:"subtype"`
	IsError          bool            `json:"is_error"`
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	SessionID        string          `json:"session_id"`
	TotalCostUSD     float64         `json:"total_cost_usd"`
	NumTurns         int             `json:"num_turns"`
	DurationMS       int64           `json:"duration_ms"`
	// ModelUsage is the per-model breakdown: { "<model-id>": { "costUSD": <float>, ... } }.
	ModelUsage map[string]struct {
		CostUSD float64 `json:"costUSD"`
	} `json:"modelUsage"`
}

// ResolveBin finds a runnable claude entrypoint. On Windows the CLI is
// claude.ps1/claude.cmd on PATH; LookPath resolves either via PATHEXT. Returns ""
// when none is found so callers can degrade gracefully instead of crashing.
func ResolveBin() string {
	for _, name := range []string{"claude", "claude.cmd", "claude.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// buildArgs assembles the managed flag list for the given output format. The prompt
// itself is NOT in argv — it goes on stdin (see AgentSpec.PromptStdin).
func (s AgentSpec) buildArgs(outputFormat string, stream bool) []string {
	args := []string{"-p", "--output-format", outputFormat}
	if stream {
		args = append(args, "--verbose") // stream-json requires --verbose
	}
	if s.SystemPromptFile != "" {
		args = append(args, "--append-system-prompt-file", s.SystemPromptFile)
	} else if s.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", s.SystemPrompt)
	}
	if s.Model != "" {
		args = append(args, "--model", s.Model)
	}
	if s.FallbackModel != "" {
		args = append(args, "--fallback-model", s.FallbackModel)
	}
	if s.PermissionMode != "" {
		args = append(args, "--permission-mode", s.PermissionMode)
	}
	if len(s.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(s.AllowedTools, ","))
	}
	for _, d := range s.AddDirs {
		args = append(args, "--add-dir", d)
	}
	if s.MaxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprintf("%d", s.MaxTurns))
	}
	if s.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%g", s.MaxBudgetUSD))
	}
	if s.SettingSources != "" {
		args = append(args, "--setting-sources", s.SettingSources)
	}
	if s.JSONSchema != "" {
		args = append(args, "--json-schema", s.JSONSchema)
	}
	// Resume an existing session, or fix a new one. --resume implies the id is known;
	// otherwise --session-id pins a fresh session so a later retry can resume it.
	if s.Resume != "" {
		args = append(args, "--resume", s.Resume)
	} else if s.SessionID != "" {
		args = append(args, "--session-id", s.SessionID)
	}
	args = append(args, s.ExtraArgs...)
	return args
}

// newCmd builds the *exec.Cmd with stdin wired to the prompt and the working dir /
// env applied. bin must be a resolved claude entrypoint.
func (s AgentSpec) newCmd(ctx context.Context, bin string, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = strings.NewReader(s.PromptStdin)
	if s.WorkDir != "" {
		cmd.Dir = s.WorkDir
	}
	if s.Env != nil {
		cmd.Env = s.Env
	}
	return cmd
}

// Run invokes the agent once in one-shot mode (--output-format json) and returns the
// parsed envelope. A nil error means the CLI ran and produced a parseable envelope;
// it does NOT mean the agent succeeded — check AgentResult.IsError / Subtype. ctx
// should carry a per-stage timeout.
func Run(ctx context.Context, spec AgentSpec) (AgentResult, error) {
	bin := ResolveBin()
	if bin == "" {
		return AgentResult{}, fmt.Errorf("claude CLI not found on PATH (tried claude, claude.cmd, claude.exe)")
	}
	args := spec.buildArgs("json", false)
	cmd := spec.newCmd(ctx, bin, args)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return AgentResult{}, fmt.Errorf("claude -p timed out: %w", ctx.Err())
		}
		return AgentResult{}, fmt.Errorf("claude -p failed: %w: %s", err, tail(stderr.String(), 600))
	}
	env, raw, ok := parseEnvelope(stdout.String())
	if !ok {
		return AgentResult{}, fmt.Errorf("could not parse claude envelope: %s", tail(stdout.String(), 800))
	}
	return resultFrom(env, raw, nil), nil
}

// Stream invokes the agent in streaming mode (--output-format stream-json --verbose)
// so live progress events can be observed (and a stuck build detected). Each
// newline-delimited JSON event is decoded; if onEvent is non-nil it is called for
// every event as it arrives (e.g. to mirror progress into a run log). The final
// "result" event supplies the same envelope fields Run returns.
func Stream(ctx context.Context, spec AgentSpec, onEvent func(json.RawMessage)) (AgentResult, error) {
	bin := ResolveBin()
	if bin == "" {
		return AgentResult{}, fmt.Errorf("claude CLI not found on PATH (tried claude, claude.cmd, claude.exe)")
	}
	args := spec.buildArgs("stream-json", true)
	cmd := spec.newCmd(ctx, bin, args)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return AgentResult{}, fmt.Errorf("stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return AgentResult{}, fmt.Errorf("start claude -p: %w", err)
	}

	var events []json.RawMessage
	var finalEnv envelope
	var finalRaw json.RawMessage
	gotFinal := false

	sc := bufio.NewScanner(stdoutPipe)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024) // tolerate large event lines
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		raw := json.RawMessage(append([]byte(nil), line...))
		events = append(events, raw)
		if onEvent != nil {
			onEvent(raw)
		}
		// The final result event carries the same envelope as one-shot mode.
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(line), &probe) == nil && probe.Type == "result" {
			if env, r, ok := parseEnvelope(line); ok {
				finalEnv, finalRaw, gotFinal = env, r, true
			}
		}
	}
	waitErr := cmd.Wait()
	if scanErr := sc.Err(); scanErr != nil && scanErr != io.EOF {
		return AgentResult{Events: events}, fmt.Errorf("reading claude stream: %w", scanErr)
	}
	if waitErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return AgentResult{Events: events}, fmt.Errorf("claude -p stream timed out: %w", ctx.Err())
		}
		// A non-zero exit with a parsed final result is still usable (e.g. budget hit);
		// surface the result but include the exit error context for the caller's log.
		if gotFinal {
			res := resultFrom(finalEnv, finalRaw, events)
			return res, fmt.Errorf("claude -p exited non-zero: %w: %s", waitErr, tail(stderr.String(), 400))
		}
		return AgentResult{Events: events}, fmt.Errorf("claude -p stream failed: %w: %s", waitErr, tail(stderr.String(), 600))
	}
	if !gotFinal {
		return AgentResult{Events: events}, fmt.Errorf("claude -p stream ended without a result event")
	}
	return resultFrom(finalEnv, finalRaw, events), nil
}

// resultFrom maps a decoded envelope into the public AgentResult.
func resultFrom(env envelope, raw json.RawMessage, events []json.RawMessage) AgentResult {
	res := AgentResult{
		Result:           env.Result,
		StructuredOutput: env.StructuredOutput,
		SessionID:        env.SessionID,
		Subtype:          env.Subtype,
		IsError:          env.IsError,
		CostUSD:          env.TotalCostUSD,
		NumTurns:         env.NumTurns,
		DurationMS:       env.DurationMS,
		Raw:              raw,
		Events:           events,
	}
	if len(env.ModelUsage) > 0 {
		res.ModelCostUSD = make(map[string]float64, len(env.ModelUsage))
		for id, mu := range env.ModelUsage {
			res.ModelCostUSD[id] = mu.CostUSD
		}
	}
	return res
}

// parseEnvelope decodes the CLI's JSON result object, tolerating any leading warning
// lines the CLI may print before the JSON (the becky-review defensive parse). It
// returns the decoded envelope, the raw object span, and whether parsing succeeded.
func parseEnvelope(s string) (envelope, json.RawMessage, bool) {
	s = strings.TrimSpace(s)
	var env envelope
	if json.Unmarshal([]byte(s), &env) == nil && env.Type != "" {
		return env, json.RawMessage(s), true
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		span := s[start : end+1]
		if json.Unmarshal([]byte(span), &env) == nil && env.Type != "" {
			return env, json.RawMessage(span), true
		}
	}
	return envelope{}, nil, false
}

// tail returns the last n bytes of s (trimmed), for compact error context.
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "..." + s[len(s)-n:]
	}
	return s
}

// WriteTempPrompt is a small convenience for callers that would rather pass a large
// prompt by file reference inside the prompt body (the documented workaround for the
// 10 MB stdin cap). It writes content to a temp file and returns its path; the caller
// owns cleanup. Kept here so the one place that knows the CLI's limits owns the hint.
func WriteTempPrompt(dir, pattern, content string) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", fmt.Errorf("create temp prompt: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		return "", fmt.Errorf("write temp prompt: %w", err)
	}
	return f.Name(), nil
}
