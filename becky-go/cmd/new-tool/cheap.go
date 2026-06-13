// cheap.go — the cheap/local model transport used by the (cheap) stages: S1 intake
// normalization, S2 research synthesis, S3 redundancy judgment, and S7 second-AI
// review. These stages NEVER use the metered Claude credit; they use a cheap/free
// model resolved by the Model Verification Protocol (models.go).
//
// Two backends, picked per privacy + reachability:
//   - openrouter: OpenAI-compatible /chat/completions over HTTPS (online runs,
//     non-sensitive tools). Requires OPENROUTER_API_KEY; the model id is the
//     protocol-verified live id, NEVER a hardcoded stale one.
//   - local:      a resident llama.cpp llama-server /v1/chat/completions endpoint
//     (offline, private — preferred for sensitive forensic work). The served model
//     is the on-disk one the protocol verified (e.g. Qwen3.5-4B GGUF).
//
// EVERY call degrades gracefully: if no backend is reachable, the caller falls back
// to a deterministic path and records a note — it never crashes the run.
//
// Fact-Forcing-Gate self-certification:
//  1. Callers: cmd/new-tool/stages.go (S1/S2/S3/S7) calls cheapComplete; the
//     resolved id comes from models.go's VerifyResearchModels.
//  2. No-dup: cmd/review/backend.go has an openRouterBackend, but it lives in
//     cmd/review's package main and is not importable; this is a small fresh client
//     for the factory, not a duplicate of an importable helper.
//  3. Data shape: POSTs {model, messages:[{role,content}], temperature} to an
//     OpenAI-compatible endpoint; reads choices[0].message.content (text). No files.
//  4. Verbatim instruction: spec §3 "(cheap) = a cheap/free model: local small LLM
//     (llama.cpp/Ollama) OR a cheap/free API" + the briefing's OpenRouter defaults
//     `poolside/laguna-m.1:free` -> `moonshotai/kimi-k2.6:free`.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// openRouterChatURL is the OpenAI-compatible chat-completions endpoint.
const openRouterChatURL = "https://openrouter.ai/api/v1/chat/completions"

// CheapBackend selects which transport a cheap stage uses.
type CheapBackend struct {
	Kind      string // "openrouter" | "local" | "none"
	Model     string // the PROTOCOL-VERIFIED model id (never a hardcoded stale one)
	ServerURL string // for local: the llama-server base URL
}

// chatMessage / chatRequest / chatResponse are the minimal OpenAI-compatible shapes
// both OpenRouter and llama-server speak.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
}
type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// cheapComplete sends one system+user prompt to the resolved cheap backend and
// returns the model's text. ok=false means no backend produced a usable answer; the
// caller MUST degrade deterministically (it never returns a hard error to the run).
func cheapComplete(ctx context.Context, be CheapBackend, system, user string) (text string, ok bool, note string) {
	switch be.Kind {
	case "openrouter":
		return openRouterComplete(ctx, be.Model, system, user)
	case "local":
		return localComplete(ctx, be.ServerURL, be.Model, system, user)
	default:
		return "", false, "no cheap backend available; using deterministic fallback"
	}
}

// openRouterComplete POSTs to OpenRouter when OPENROUTER_API_KEY is set, else returns
// a clean skip. Any HTTP/parse failure degrades to ok=false + note.
func openRouterComplete(ctx context.Context, model, system, user string) (string, bool, string) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		return "", false, "openrouter cheap backend skipped: OPENROUTER_API_KEY not set"
	}
	if model == "" {
		return "", false, "openrouter cheap backend skipped: no protocol-verified model id resolved"
	}
	rc, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	body, _ := json.Marshal(chatRequest{
		Model:       model,
		Temperature: 0.1,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	})
	req, err := http.NewRequestWithContext(rc, http.MethodPost, openRouterChatURL, bytes.NewReader(body))
	if err != nil {
		return "", false, "openrouter cheap backend degraded: " + err.Error()
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", false, "openrouter cheap backend degraded: " + err.Error()
	}
	defer resp.Body.Close()

	var cr chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return "", false, fmt.Sprintf("openrouter cheap backend degraded: decode failed (HTTP %d)", resp.StatusCode)
	}
	if cr.Error != nil {
		return "", false, "openrouter cheap backend degraded: API error: " + cr.Error.Message
	}
	if len(cr.Choices) == 0 || strings.TrimSpace(cr.Choices[0].Message.Content) == "" {
		return "", false, fmt.Sprintf("openrouter cheap backend degraded: no content (HTTP %d)", resp.StatusCode)
	}
	return cr.Choices[0].Message.Content, true, ""
}

// localComplete POSTs to a resident llama-server's /v1/chat/completions. With no
// reachable server it returns a clean skip (the offline-but-no-server case).
func localComplete(ctx context.Context, serverURL, model, system, user string) (string, bool, string) {
	if serverURL == "" {
		return "", false, "local cheap backend skipped: no llama-server URL configured"
	}
	rc, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	url := strings.TrimRight(serverURL, "/") + "/v1/chat/completions"
	body, _ := json.Marshal(chatRequest{
		Model:       firstNonEmptyStr(model, "local"),
		Temperature: 0.1,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	})
	req, err := http.NewRequestWithContext(rc, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", false, "local cheap backend degraded: " + err.Error()
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", false, "local cheap backend degraded (is a llama-server chat endpoint running?): " + err.Error()
	}
	defer resp.Body.Close()

	var cr chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return "", false, fmt.Sprintf("local cheap backend degraded: decode failed (HTTP %d)", resp.StatusCode)
	}
	if len(cr.Choices) == 0 || strings.TrimSpace(cr.Choices[0].Message.Content) == "" {
		return "", false, fmt.Sprintf("local cheap backend degraded: no content (HTTP %d)", resp.StatusCode)
	}
	return cr.Choices[0].Message.Content, true, ""
}

// extractJSON pulls the first balanced {...} or [...] span out of a model reply that
// may wrap JSON in prose or ```fences (cheap models rarely return clean JSON). It is
// the defensive parse the (cheap) stages rely on before json.Unmarshal.
func extractJSON(s string) (string, bool) {
	s = strings.TrimSpace(s)
	// Strip a leading ```json / ``` fence if present.
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[nl+1:]
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			rest = rest[:end]
		}
		s = strings.TrimSpace(rest)
	}
	for _, pair := range [][2]byte{{'{', '}'}, {'[', ']'}} {
		start := strings.IndexByte(s, pair[0])
		end := strings.LastIndexByte(s, pair[1])
		if start >= 0 && end > start {
			return s[start : end+1], true
		}
	}
	return "", false
}

func firstNonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
