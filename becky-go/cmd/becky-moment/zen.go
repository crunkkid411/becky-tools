// zen.go — the CONTENT judge backend: OpenCode Zen's OpenAI-compatible endpoint.
//
// Jordan authorised his OpenCode Zen key for the moment-selection judgement, with
// one standing rule: FREE MODELS ONLY. So this file does exactly one thing to
// enforce that — it checks the model id against the free list below and refuses
// anything else. There is no override flag, because "free only" is an absolute
// rule and a flag that breaks it should not exist.
//
// An allowlist (rather than a "looks free" heuristic) is the whole guard:
//   - `claude-*` is not on the list, so Jordan never pays per token for a model
//     his Claude Max subscription already covers.
//   - A typo is not on the list, so it costs a refusal instead of money.
//   - Zen's paid tier is one character away from its free tier
//     (`deepseek-v4-flash` is metered, `deepseek-v4-flash-free` is not), which is
//     precisely the mistake a suffix heuristic makes and a list cannot.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"becky-go/internal/moment"
)

const (
	zenChatURL = "https://opencode.ai/zen/v1/chat/completions"
	zenTimeout = 120 * time.Second
	zenDocsURL = "https://opencode.ai/docs/zen/"
)

// zenFreeModels is Zen's complete free tier, read from the pricing table at
// zenDocsURL and cross-checked against the live https://opencode.ai/zen/v1/models
// list on 2026-08-18. Zen rotates this roster, and /v1/models reports no pricing,
// so it cannot be derived at runtime — when a model here starts 404ing, check the
// docs page and update this list. Nothing else in this file needs to change.
var zenFreeModels = []string{
	"big-pickle",
	"deepseek-v4-flash-free",
	"mimo-v2.5-free",
	"hy3-free",
	"laguna-s-2.1-free",
	"nemotron-3-ultra-free",
	"nemotron-3.5-lightning-free",
}

// defaultZenModel is the free model becky judges with unless told otherwise.
const defaultZenModel = "deepseek-v4-flash-free"

// isFreeZenModel reports whether id is on Zen's free tier.
func isFreeZenModel(id string) bool {
	return slices.Contains(zenFreeModels, strings.ToLower(strings.TrimSpace(id)))
}

// zenKey returns the API key from the environment, or "" when unset.
func zenKey() string {
	for _, env := range []string{"BECKY_ZEN_API_KEY", "OPENCODE_API_KEY", "OPENCODE_ZEN_API_KEY"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return v
		}
	}
	return ""
}

// zenJudge returns a moment.JudgeFunc backed by OpenCode Zen, or an error
// explaining exactly why judging is unavailable. The caller treats that error as
// a DEGRADE (structure-only ranking), never a crash.
func zenJudge(model string, batchSize int) (moment.JudgeFunc, error) {
	if model == "" {
		model = defaultZenModel
	}
	if !isFreeZenModel(model) {
		return nil, fmt.Errorf(
			"refusing model %q: becky only uses OpenCode Zen's free models.\nfree models: %s\nif that roster has changed, see %s",
			model, strings.Join(zenFreeModels, ", "), zenDocsURL)
	}
	key := zenKey()
	if key == "" {
		return nil, fmt.Errorf("no API key: set BECKY_ZEN_API_KEY (or OPENCODE_API_KEY)")
	}
	if batchSize <= 0 {
		batchSize = 12
	}

	client := &http.Client{Timeout: zenTimeout}
	return func(ctx context.Context, cands []moment.Candidate) ([]moment.Judgement, error) {
		var all []moment.Judgement
		for start := 0; start < len(cands); start += batchSize {
			end := min(start+batchSize, len(cands))
			batch := cands[start:end]
			raw, err := zenOnce(ctx, client, key, model, moment.Prompt(batch))
			if err != nil {
				// Keep whatever earlier batches produced: a partial verdict set
				// degrades per-candidate in Rank, which is honest. Losing every
				// good verdict because batch 3 timed out would not be.
				if len(all) == 0 {
					return nil, err
				}
				return all, nil
			}
			for _, j := range moment.ParseJudgements(raw) {
				// Re-base the batch-local index onto the full candidate slice.
				j.Index += start
				all = append(all, j)
			}
		}
		return all, nil
	}, nil
}

// zenOnce performs one chat-completion request and returns the assistant text.
func zenOnce(ctx context.Context, client *http.Client, key, model, prompt string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature": 0, // deterministic: same transcript -> same verdicts
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, zenChatURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("zen %s: decode: %w", resp.Status, err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(out.Error.Message)
		if msg == "" {
			msg = resp.Status
		}
		return "", fmt.Errorf("zen %s: %s", resp.Status, msg)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("zen returned no choices")
	}
	return out.Choices[0].Message.Content, nil
}
