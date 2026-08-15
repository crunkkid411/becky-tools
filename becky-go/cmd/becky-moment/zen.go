// zen.go — the CONTENT judge backend: OpenCode Zen's OpenAI-compatible endpoint.
//
// Jordan explicitly authorised using his OpenCode Zen key for the moment-selection
// judgement (2026-08-15). This file honours that while keeping spending
// DELIBERATE rather than accidental, because two properties of this provider make
// an unguarded client genuinely dangerous:
//
//  1. Zen resells `claude-opus-5` / `claude-sonnet-5` / `claude-haiku-4-5` per
//     token. Jordan pays for Claude Max, so those are ALREADY BOUGHT — routing
//     them through a metered gateway is paying twice for the same model. That is
//     the exact mistake that burned his balance on 2026-07-19 (CLAUDE.md's
//     spending invariant). They are hard-blocked here, separately from the
//     free/paid gate, and --allow-paid does NOT unblock them.
//  2. Zen auto-reloads $20 when the balance drops below $5. On OpenRouter a
//     runaway loop stopped itself when the balance hit zero and every call 402'd.
//     Here it would just keep charging. So a paid model requires an explicit
//     per-run --allow-paid, and the batch count is bounded.
//
// The guard pattern (refuse BEFORE the request is sent) is copied from
// cmd/subtitle/openrouter.go's isFreeModel, as CLAUDE.md requires for any new
// tool that talks to a metered endpoint.
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

	"becky-go/internal/moment"
)

const (
	zenChatURL = "https://opencode.ai/zen/v1/chat/completions"
	zenTimeout = 120 * time.Second
)

// defaultZenModel is a FREE-tier Zen model. Zen does not currently expose an
// OpenAI-style /v1/models discovery endpoint, so this cannot be verified from
// code — the exact free-tier ids are listed at https://opencode.ai/docs/zen/ and
// this default is the one to confirm on first real run (HANDOFF-SHORTS-PIPELINE
// step 1 verification). If it is wrong, the tool degrades with the endpoint's own
// error rather than silently falling back to something metered.
const defaultZenModel = "deepseek-v4-flash-free"

// anthropicResold are model ids Jordan already owns through Claude Max. Paying
// per token for these is the documented theft case, so they are refused outright.
var anthropicResold = []string{"claude-", "anthropic/"}

// freeSuffixes mark Zen's free tier. Anything else is metered.
var freeSuffixes = []string{"-free", ":free"}

// isFreeZenModel reports whether id is on Zen's free tier. Conservative by
// construction: an id it does not recognise is treated as PAID, so a typo costs
// a refusal rather than money.
func isFreeZenModel(id string) bool {
	l := strings.ToLower(strings.TrimSpace(id))
	if l == "big-pickle" { // named free model with no -free suffix
		return true
	}
	for _, s := range freeSuffixes {
		if strings.HasSuffix(l, s) {
			return true
		}
	}
	return false
}

// isResoldAnthropic reports whether id is a Claude model being resold per token.
func isResoldAnthropic(id string) bool {
	l := strings.ToLower(strings.TrimSpace(id))
	for _, p := range anthropicResold {
		if strings.HasPrefix(l, p) || strings.Contains(l, "/"+p) {
			return true
		}
	}
	return false
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
func zenJudge(model string, allowPaid bool, batchSize int) (moment.JudgeFunc, error) {
	if model == "" {
		model = defaultZenModel
	}
	if isResoldAnthropic(model) {
		return nil, fmt.Errorf(
			"refusing model %q: Claude is already paid for through Jordan's Max subscription — "+
				"reach it via the OAuth session (claude / internal/agentrun), not a metered gateway", model)
	}
	if !isFreeZenModel(model) && !allowPaid {
		return nil, fmt.Errorf(
			"refusing model %q: it is not on Zen's free tier and --allow-paid was not given "+
				"(Zen auto-reloads $20 below $5, so a metered run must be deliberate)", model)
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
			end := start + batchSize
			if end > len(cands) {
				end = len(cands)
			}
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
	// Belt and braces: the guard also runs immediately before the wire, so no
	// future refactor can route around the constructor's check.
	if isResoldAnthropic(model) {
		return "", fmt.Errorf("refusing to send a metered request for %q (already covered by Claude Max)", model)
	}

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
