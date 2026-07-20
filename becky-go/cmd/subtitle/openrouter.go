package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"becky-go/internal/subs"
)

// The caption reviewer talks to the model over plain HTTPS (OpenRouter's
// OpenAI-compatible endpoint), NOT through a CLI.
//
// Every CLI route was tried and every one hung: `claude -p` stops on a
// permission prompt nobody can grant, and wrapping it in fleet-run.ps1 still
// left the run sitting for >10 minutes with nothing to show. A direct request
// has none of those failure modes - no shim process, no inherited pipes, no
// interactive prompt, no stdout truncation - and measured 1.8s round trip.
// If this ever regresses, the fix is a better HTTP call, not another wrapper.
const (
	openRouterURL = "https://openrouter.ai/api/v1/chat/completions"
	// Hy3 is FREE on this key ("the Fable 5 stand-in" per the profile launcher),
	// so the caption pass costs nothing to run. Sonnet 5 is reachable by name but
	// is NOT the default: the OpenRouter balance was $0.67 and a single run of
	// this pass exhausted it, after which every batch 402'd and silently fell
	// back to pacing-only chunking. Free by default, paid only when asked for.
	defaultReviewer   = "tencent/hy3:free"
	reviewHTTPTimeout = 90 * time.Second
)

type orMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type orRequest struct {
	Model       string      `json:"model"`
	Messages    []orMessage `json:"messages"`
	MaxTokens   int         `json:"max_tokens"`
	Temperature float64     `json:"temperature"`
}

type orResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// reviewerModelID maps the friendly names Jordan uses onto OpenRouter ids.
func reviewerModelID(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "hy3", "free":
		return defaultReviewer
	case "nemotron":
		return "nvidia/nemotron-3-ultra-550b-a55b:free"
	case "gemma":
		return "google/gemma-4-31b-it:free"
	case "laguna":
		return "poolside/laguna-xs-2.1:free"
	}
	return name // already a full OpenRouter id
}

// freeRotation is the fallback chain, best-first. Every entry is FREE. None of
// these endpoints is dependable on its own — they rate-limit, go DEGRADED, and
// expire — so the reviewer walks the list rather than betting the feature on
// any single one.
var freeRotation = []string{
	"tencent/hy3:free",                       // Jordan's pick, the Fable 5 stand-in
	"minimax/minimax-m3:free",                // m-3
	"z-ai/glm-5.2:free",                      // GLM 5.2
	"nvidia/nemotron-3-ultra-550b-a55b:free", // big, usually up
	"google/gemma-4-31b-it:free",             // last resort
}

// rotationFor puts the requested model first, then every OTHER free model as a
// fallback, so an explicit choice is honoured but never a single point of
// failure. Duplicates are dropped.
func rotationFor(first string) []string {
	out := []string{first}
	seen := map[string]bool{first: true}
	for _, m := range freeRotation {
		if !seen[m] {
			out = append(out, m)
			seen[m] = true
		}
	}
	return out
}

// haveReviewer reports whether the reviewer can run at all.
func haveReviewer() bool { return os.Getenv("OPENROUTER_API_KEY") != "" }

// openRouterModel returns a subs.ModelFunc backed by one HTTPS request per
// batch. Retries once on a transient failure, then gives up so the
// deterministic chunking takes over rather than the tool hanging.
func openRouterModel(name string, verbose bool) subs.ModelFunc {
	model := reviewerModelID(name)
	key := os.Getenv("OPENROUTER_API_KEY")
	client := &http.Client{Timeout: reviewHTTPTimeout}
	var batch int

	return func(ctx context.Context, prompt string) (string, error) {
		batch++
		start := time.Now()
		fmt.Fprintf(os.Stderr, "  reviewing caption grouping, batch %d (%s)...\n", batch, model)

		// ROTATE. Jordan: "none of them are reliable, you also have not proven
		// yourself reliable - FUCKING ROTATE". Free endpoints rate-limit, go
		// degraded, and expire without notice, so one failing model must never
		// cost him the feature. Try each in turn; the first that answers wins.
		var lastErr error
		for _, m := range rotationFor(model) {
			text, err := openRouterOnce(ctx, client, key, m, prompt)
			if err == nil {
				fmt.Fprintf(os.Stderr, "  batch %d ok in %.1fs (%s)\n", batch, time.Since(start).Seconds(), m)
				return text, nil
			}
			lastErr = err
			fmt.Fprintf(os.Stderr, "  batch %d: %s failed (%v) - rotating\n", batch, m, firstLine(err.Error()))
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
		return "", lastErr
	}
}

// isFreeModel is the SPENDING GUARD. Jordan pays for Claude Max; his OAuth
// covers Sonnet 5 and every other Anthropic model at no extra cost. Any paid
// API call is spending his money for something he ALREADY owns.
//
// This is not advice, it is enforced: a run of the caption pass on
// anthropic/claude-sonnet-5 burned his entire $0.67 OpenRouter balance, after
// which every batch 402'd. That must be impossible, not discouraged.
//
// OpenRouter is for FREE models only. Anything else returns an error before a
// single request leaves the machine.
func isFreeModel(id string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(id)), ":free")
}

func openRouterOnce(ctx context.Context, client *http.Client, key, model, prompt string) (string, error) {
	if !isFreeModel(model) {
		return "", fmt.Errorf("refusing to call %q: OpenRouter is FREE MODELS ONLY (ids ending in :free). "+
			"Anthropic models are covered by the Claude Max subscription - reach them through the OAuth "+
			"session, never a paid API", model)
	}
	body, err := json.Marshal(orRequest{
		Model:     model,
		Messages:  []orMessage{{Role: "user", Content: prompt}},
		MaxTokens: 4000,
		// Deterministic-as-possible: the same transcript should regroup the same
		// way twice, because becky's contract is same input -> same output.
		Temperature: 0,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openRouterURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Title", "becky-subtitle")

	resp, err := client.Do(req)
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

	var out orResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("could not read the reply: %w", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("%s: %s", model, out.Error.Message)
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("%s returned no content", model)
	}
	return out.Choices[0].Message.Content, nil
}
