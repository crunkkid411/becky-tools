package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// backend_api.go is Tier-2b: the Anthropic Messages API frontier backend, used
// when the CLI path is unavailable or a key is preferred (e.g. unattended batch).
// cgo-free net/http. Endpoint + headers are the stable public shape (R-AI §4.5):
//
//	POST https://api.anthropic.com/v1/messages
//	  x-api-key: $ANTHROPIC_API_KEY
//	  anthropic-version: 2023-06-01
//	  body: {model, max_tokens, system, messages:[{role:"user",content}]}
//
// The exact model snapshot id should be re-verified via the in-repo claude-api
// skill before relying on a hard id (SPEC-BECKY-ASK §0); the CLI backend sidesteps
// this by using durable aliases. The default model here is left as a class label
// the GUI overrides with a confirmed id.

const (
	anthropicEndpoint = "https://api.anthropic.com/v1/messages"
	anthropicVersion  = "2023-06-01" // stable; re-verify via claude-api before shipping
)

// apiBackend POSTs to /v1/messages. key comes from ANTHROPIC_API_KEY (read at
// construction; "" means unavailable). endpoint is overridable for tests.
type apiBackend struct {
	key      string
	model    string
	endpoint string
	http     *http.Client
}

// newAPIBackend builds the Tier-2b backend. model is a snapshot id/class label
// (e.g. "claude-opus-4-8"); the key is read from the environment.
func newAPIBackend(model string) *apiBackend {
	return &apiBackend{
		key:      os.Getenv("ANTHROPIC_API_KEY"),
		model:    model,
		endpoint: anthropicEndpoint,
		http:     &http.Client{Timeout: 60 * time.Second},
	}
}

func (b *apiBackend) Name() string { return "anthropic-api" }

// Available reports whether an API key is configured.
func (b *apiBackend) Available() error {
	if b.key == "" {
		return fmt.Errorf("ANTHROPIC_API_KEY not set")
	}
	return nil
}

// Complete POSTs one message and returns the model's text (content[0].text).
func (b *apiBackend) Complete(ctx context.Context, req Request) (string, error) {
	if b.key == "" {
		return "", fmt.Errorf("ANTHROPIC_API_KEY not set")
	}
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = 1024
	}
	body, err := json.Marshal(map[string]any{
		"model":      b.model,
		"max_tokens": maxTok,
		"system":     req.System,
		"messages":   []map[string]string{{"role": "user", "content": req.User}},
	})
	if err != nil {
		return "", fmt.Errorf("marshal messages request: %w", err)
	}

	endpoint := b.endpoint
	if endpoint == "" {
		endpoint = anthropicEndpoint
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("x-api-key", b.key)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	httpReq.Header.Set("content-type", "application/json")

	client := b.http
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("anthropic API HTTP %d", resp.StatusCode)
	}
	var r struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", fmt.Errorf("decode messages response: %w", err)
	}
	if len(r.Content) == 0 {
		return "", fmt.Errorf("empty content from anthropic API")
	}
	return r.Content[0].Text, nil
}
