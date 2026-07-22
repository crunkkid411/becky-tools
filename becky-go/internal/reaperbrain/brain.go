// Package reaperbrain serves the local endpoint that REAPER's "REAPER Chat"
// extension talks to for natural-language DAW control.
//
// REAPER Chat POSTs OpenAI-style chat completions to a HARD-CODED
// http://localhost:11435/v1/chat/completions. The first brain (2026-06-20) put a
// llama.cpp llama-server there, which loaded a 4B GGUF onto the GPU every time —
// the "chatbox hogs system resources" complaint (2026-07-22) — and when the
// server wasn't up, REAPER Chat errored on every REAPER launch.
//
// This brain is a FEATHERWEIGHT PROXY instead: a plain Go HTTP server on :11435
// (a few MB of RAM, zero GPU) that forwards each chat turn to one of two
// backends Jordan already has:
//
//   - claude — the Claude Code CLI over his Max OAuth session (`claude -p`).
//     Already paid for; costs nothing extra. The default.
//   - zen    — OpenCode Zen's OpenAI-compatible API, FREE models only.
//     The free-only rule is ENFORCED IN CODE (IsZenFree), same as
//     cmd/subtitle's isFreeModel guard: a paid id is refused before any
//     request leaves the machine.
//
// Everything degrades, never crashes: a missing claude binary or Zen key
// becomes a descriptive error REAPER Chat displays, not a dead port.
package reaperbrain

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// DefaultPort is the port REAPER Chat is hard-wired to. Do not change it
	// without also changing the extension's config (Jordan can't).
	DefaultPort = 11435
	// DefaultHost binds loopback only — this is a local brain, never exposed.
	DefaultHost = "127.0.0.1"

	// EnvBackend picks the backend when no --backend flag is given:
	// "claude" (default) or "zen".
	EnvBackend = "BECKY_REAPER_BACKEND"
	// EnvPort overrides the listen port (for tests; REAPER Chat can't follow).
	EnvPort = "BECKY_REAPER_BRAIN_PORT"
)

// Message is one OpenAI-style chat message — the shape REAPER Chat sends and
// both backends accept.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Backend turns a conversation into the assistant's next reply.
type Backend interface {
	// Name is the short id shown in logs and the CLI plan ("claude", "zen").
	Name() string
	// Model is the model id replies are attributed to (and /v1/models lists).
	Model() string
	// Complete returns the assistant's next message for the conversation.
	Complete(ctx context.Context, messages []Message) (string, error)
}

// FlattenMessages renders a conversation as a single plain-text prompt for a
// backend that takes one prompt (the claude CLI). Deterministic.
func FlattenMessages(messages []Message) string {
	var system, turns []string
	for _, m := range messages {
		text := strings.TrimSpace(m.Content)
		if text == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(m.Role)) {
		case "system":
			system = append(system, text)
		case "assistant":
			turns = append(turns, "Assistant: "+text)
		default: // user + anything unknown reads as the human
			turns = append(turns, "User: "+text)
		}
	}
	var b strings.Builder
	if len(system) > 0 {
		b.WriteString(strings.Join(system, "\n\n"))
		b.WriteString("\n\n")
	}
	b.WriteString("Conversation so far:\n")
	b.WriteString(strings.Join(turns, "\n"))
	b.WriteString("\n\nReply with the assistant's next message only - no preamble, no commentary about these instructions.")
	return b.String()
}

// ReadyBackend is a Backend that can also report up front whether it is usable
// (both real backends implement it; the CLI plan prints the result).
type ReadyBackend interface {
	Backend
	Ready() error
}

// SelectBackend maps a --backend flag / BECKY_REAPER_BACKEND value onto a real
// backend. Empty picks claude — the zero-config, already-paid default.
func SelectBackend(name string) (ReadyBackend, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "claude":
		return NewClaudeBackend(), nil
	case "zen", "opencode", "opencode-zen":
		return NewZenBackend(), nil
	default:
		return nil, fmt.Errorf("unknown backend %q (want claude or zen)", name)
	}
}

// CheckHealth reports whether a brain is already answering on baseURL/health
// within the timeout (i.e. REAPER Chat would connect). nil error = alive.
func CheckHealth(ctx context.Context, baseURL string, timeout time.Duration) error {
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health returned HTTP %d", resp.StatusCode)
	}
	return nil
}
