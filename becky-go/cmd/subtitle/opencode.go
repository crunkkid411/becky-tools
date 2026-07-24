package main

// opencode.go routes the caption-review pass through OpenCode Zen (Jordan's account) instead
// of OpenRouter's now-dead free models. Jordan, 2026-07-24: "use my opencode zen api key for
// hy3, not the openrouter key ... the fallback if the llm fails should be to just use the
// transcription as if --review=false instead of fucking up the whole thing." So this makes ONE
// request for the whole edit (main.go sends every segment in one batch), retries once on a
// transient error, then gives up so PlanChunks falls back to the deterministic captions.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"becky-go/internal/proc"
	"becky-go/internal/subs"
)

const zenEndpoint = "https://opencode.ai/zen/v1/chat/completions"

// zenModelID maps the friendly --review-model name to an OpenCode Zen model id. Only the FREE
// Zen models are used (the "-free" ids) - Jordan pays for Claude Max, never a per-token API.
// NOTE (2026-07-24): hy3 was REMOVED from the Zen catalog; deepseek-v4-flash-free is the free
// model that answers the regrouping task reliably (nemotron/mimo were failing), so "hy3" and
// the unknown/default case both resolve to it. Jordan: "that's fine, deepseek is fine."
func zenModelID(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "north", "north-mini-code", "north-mini-code-free":
		return "north-mini-code-free"
	case "ling", "ling-3.0-flash", "ling-3.0-flash-free":
		return "ling-3.0-flash-free"
	default: // "", "hy3", "deepseek" and anything unknown -> the working free default
		return "deepseek-v4-flash-free"
	}
}

// zenKey returns Jordan's OpenCode Zen key. Preference: OPENCODE_ZEN_API_KEY, else the
// DPAPI-encrypted %USERPROFILE%\.claude\opencode_key.bin, decrypted with the SAME PowerShell
// one-liner the fleet uses (only his Windows account can read it) so there is one key store.
func zenKey() string {
	if k := strings.TrimSpace(os.Getenv("OPENCODE_ZEN_API_KEY")); k != "" {
		return k
	}
	if os.Getenv("USERPROFILE") == "" {
		return "" // no DPAPI store off Windows
	}
	const script = `$p=Join-Path $env:USERPROFILE '.claude\opencode_key.bin'; ` +
		`if(Test-Path $p){[Runtime.InteropServices.Marshal]::PtrToStringAuto(` +
		`[Runtime.InteropServices.Marshal]::SecureStringToBSTR((Get-Content $p|ConvertTo-SecureString)))}`
	// The DPAPI store is user-scoped, so PowerShell 7 (pwsh) and Windows PowerShell 5.1
	// (powershell) both decrypt to the SAME key - try each so a machine missing one, or one
	// whose Security module won't load, still yields the key.
	for _, sh := range []string{"pwsh", "powershell"} {
		cmd := exec.Command(sh, "-NoProfile", "-Command", script)
		proc.NoWindow(cmd)
		if out, err := cmd.Output(); err == nil {
			if k := strings.TrimSpace(string(out)); k != "" {
				return k
			}
		}
	}
	return ""
}

// haveZenKey reports whether the OpenCode Zen reviewer can run.
func haveZenKey() bool { return zenKey() != "" }

// opencodeZenModel returns a subs.ModelFunc backed by OpenCode Zen's OpenAI-compatible
// endpoint. One request, one retry on a transient failure, then it errors so the deterministic
// chunking takes over rather than the tool hanging.
func opencodeZenModel(name string, verbose bool) subs.ModelFunc {
	model := zenModelID(name)
	key := zenKey()
	client := &http.Client{Timeout: reviewHTTPTimeout}
	return func(ctx context.Context, prompt string) (string, error) {
		var lastErr error
		for attempt := 0; attempt < 2; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(800 * time.Millisecond):
				}
			}
			fmt.Fprintf(os.Stderr, "  reviewing caption grouping via OpenCode Zen (%s)...\n", model)
			start := time.Now()
			text, err := zenOnce(ctx, client, key, model, prompt)
			if err == nil {
				fmt.Fprintf(os.Stderr, "  review ok in %.1fs (%s)\n", time.Since(start).Seconds(), model)
				return text, nil
			}
			lastErr = err
			tail := " - falling back to the deterministic captions"
			if attempt == 0 {
				tail = " - retrying once"
			}
			fmt.Fprintf(os.Stderr, "  Zen %s failed (%v)%s\n", model, firstLine(err.Error()), tail)
		}
		return "", lastErr
	}
}

func zenOnce(ctx context.Context, client *http.Client, key, model, prompt string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"model":       model,
		"temperature": 0,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, zenEndpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Zen %s returned %d: %s", model, resp.StatusCode, firstLine(buf.String()))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		return "", fmt.Errorf("Zen %s: could not read the reply as JSON: %w", model, err)
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("Zen %s: empty reply", model)
	}
	return parsed.Choices[0].Message.Content, nil
}
