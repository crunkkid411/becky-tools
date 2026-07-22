package reaperbrain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// ZenBaseURL is OpenCode Zen's OpenAI-compatible root (verified live
	// 2026-07-22: /v1/models and /v1/chat/completions).
	ZenBaseURL = "https://opencode.ai/zen/v1"

	// EnvZenKey / EnvZenKeyAlt hold Jordan's Zen API key.
	EnvZenKey    = "OPENCODE_API_KEY"
	EnvZenKeyAlt = "OPENCODE_ZEN_API_KEY"
	// EnvZenModel overrides the model id (must still pass IsZenFree).
	EnvZenModel = "BECKY_ZEN_MODEL"
	// EnvZenFreeExtra is a comma-separated allowlist extension for future free
	// ids that don't carry the -free suffix (the big-pickle case).
	EnvZenFreeExtra = "BECKY_ZEN_FREE_EXTRA"

	// DefaultZenModel: "big-pickle" is Zen's free stealth frontier model —
	// the strongest free chat brain on the endpoint (docs list it $0/$0,
	// verified 2026-07-22).
	DefaultZenModel = "big-pickle"

	zenHTTPTimeout = 120 * time.Second
)

// zenFreeRotation is the fallback chain, best-first — every entry FREE on Zen
// (verified against /v1/models + the pricing docs, 2026-07-22). Free endpoints
// rate-limit and expire without notice, so one failing model must never cost
// Jordan the feature: the backend walks the list. Ids the live catalogue no
// longer offers are dropped (same lesson as cmd/subtitle's liveFreeModels: a
// hardcoded name must not survive going stale).
var zenFreeRotation = []string{
	DefaultZenModel,
	"deepseek-v4-flash-free",
	"nemotron-3-ultra-free",
	"laguna-s-2.1-free",
	"mimo-v2.5-free",
	"north-mini-code-free",
}

// IsZenFree is the SPEND GUARD, the reaperbrain twin of cmd/subtitle's
// isFreeModel. Jordan's money is never spent: Zen's free ids end in "-free",
// plus the explicitly-verified stealth freebie big-pickle, plus anything he
// lists in BECKY_ZEN_FREE_EXTRA. Everything else is refused BEFORE a request
// leaves the machine — Zen also serves paid Claude/GPT ids, and Anthropic
// models are already covered by the Max OAuth session.
func IsZenFree(id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return false
	}
	if strings.HasSuffix(id, "-free") || id == DefaultZenModel {
		return true
	}
	for _, extra := range strings.Split(os.Getenv(EnvZenFreeExtra), ",") {
		if id == strings.ToLower(strings.TrimSpace(extra)) && extra != "" {
			return true
		}
	}
	return false
}

// ZenBackend answers chat turns over OpenCode Zen's /v1/chat/completions with
// free models only. Direct HTTPS to an OpenAI-compatible endpoint — the
// sanctioned pattern (cmd/new-tool/cheap.go / cmd/subtitle/openrouter.go).
type ZenBackend struct {
	BaseURL string
	Key     string
	ModelID string

	// Client is injectable for tests; nil uses a timeout-bounded default.
	Client *http.Client

	liveOnce sync.Once
	liveFree map[string]bool
}

// NewZenBackend resolves the key + model from env. A missing key degrades to a
// descriptive Complete error, not a crash.
func NewZenBackend() *ZenBackend {
	b := &ZenBackend{BaseURL: ZenBaseURL, ModelID: DefaultZenModel}
	if k := strings.TrimSpace(os.Getenv(EnvZenKey)); k != "" {
		b.Key = k
	} else if k := strings.TrimSpace(os.Getenv(EnvZenKeyAlt)); k != "" {
		b.Key = k
	}
	if m := strings.TrimSpace(os.Getenv(EnvZenModel)); m != "" {
		b.ModelID = m
	}
	return b
}

func (b *ZenBackend) Name() string  { return "zen" }
func (b *ZenBackend) Model() string { return "zen/" + b.ModelID }

// Ready reports whether the backend can serve at all (used by the CLI plan).
func (b *ZenBackend) Ready() error {
	if b.Key == "" {
		return fmt.Errorf("no OpenCode Zen key (set %s; get one at opencode.ai/zen)", EnvZenKey)
	}
	if !IsZenFree(b.ModelID) {
		return fmt.Errorf("refusing model %q: OpenCode Zen is FREE MODELS ONLY here (ids ending in -free, or %s). "+
			"Anthropic models are covered by the Claude Max OAuth session - use the claude backend, never a paid API", b.ModelID, DefaultZenModel)
	}
	return nil
}

// Complete walks the free rotation (requested model first) until one answers.
func (b *ZenBackend) Complete(ctx context.Context, messages []Message) (string, error) {
	if err := b.Ready(); err != nil {
		return "", err
	}
	var lastErr error
	for _, m := range b.rotation() {
		text, err := b.once(ctx, m, messages)
		if err == nil {
			return text, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return "", lastErr
}

// rotation puts the configured model first, then every other free id the live
// catalogue still offers. If the catalogue can't be fetched, the static order
// stands (offline-tolerant).
func (b *ZenBackend) rotation() []string {
	live := b.liveFreeModels()
	keep := func(id string) bool { return len(live) == 0 || live[id] }
	out := []string{b.ModelID}
	seen := map[string]bool{b.ModelID: true}
	for _, m := range zenFreeRotation {
		if !seen[m] && keep(m) {
			out = append(out, m)
			seen[m] = true
		}
	}
	for id := range live {
		if !seen[id] && IsZenFree(id) {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out
}

// liveFreeModels asks GET /models which free ids ACTUALLY exist right now.
// Cached for the process; empty on any failure.
func (b *ZenBackend) liveFreeModels() map[string]bool {
	b.liveOnce.Do(func() {
		b.liveFree = map[string]bool{}
		req, err := http.NewRequest(http.MethodGet, b.BaseURL+"/models", nil)
		if err != nil {
			return
		}
		if b.Key != "" {
			req.Header.Set("Authorization", "Bearer "+b.Key)
		}
		resp, err := b.client().Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		var out struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if json.NewDecoder(resp.Body).Decode(&out) != nil {
			return
		}
		for _, m := range out.Data {
			if IsZenFree(m.ID) {
				b.liveFree[m.ID] = true
			}
		}
	})
	return b.liveFree
}

type zenRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
}

type zenResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (b *ZenBackend) once(ctx context.Context, model string, messages []Message) (string, error) {
	if !IsZenFree(model) { // belt AND braces: the rotation is free-only already
		return "", fmt.Errorf("refusing to call %q: not a free OpenCode Zen model", model)
	}
	body, err := json.Marshal(zenRequest{Model: model, Messages: messages, Temperature: 0.2})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+b.Key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s returned %s: %s", model, resp.Status, firstLine(strings.TrimSpace(string(raw))))
	}
	var out zenResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("could not read %s's reply: %w", model, err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("%s: %s", model, out.Error.Message)
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("%s returned no content", model)
	}
	return out.Choices[0].Message.Content, nil
}

func (b *ZenBackend) client() *http.Client {
	if b.Client != nil {
		return b.Client
	}
	return &http.Client{Timeout: zenHTTPTimeout}
}
