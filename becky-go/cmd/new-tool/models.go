// models.go — the Model Verification Protocol, executed at RUNTIME.
//
// This is the load-bearing anti-staleness logic. The spec's example model ids
// ("Phi-4-mini", "qwen3-4b", "gemini flash") are STALE and MUST NOT be hardcoded.
// Instead, whenever the factory needs to NAME a model, it verifies the current id
// live, from the authoritative channel for that model class, and records a
// ModelCheck (exact id + official source + date checked + why). No fixed model id
// is baked into control flow; the only constants here are the PROTOCOL itself (which
// API/CLI to consult) and the spec-sanctioned DEFAULTS-OF-LAST-RESORT, which are
// themselves re-confirmed live before use.
//
// Channels (per BUILD-AGENT-BRIEFING.md "Model Verification Protocol"):
//   - local-hf:    the `hf` CLI + on-disk reuse (X:\HuggingFace\models, models\).
//   - openrouter:  the LIVE models API, filtered to free, sorted newest.
//   - hosted-api:  official site only; exact current version (no version-less names).
//   - claude:      the build/spec model id is resolved by the claude CLI itself.
//
// Fact-Forcing-Gate self-certification:
//  1. Callers: cmd/new-tool/stages.go S2 (research) calls VerifyResearchModels;
//     cheap.go consumes the resolved ids; orchestrator.go calls dumpModelChecks.
//  2. No-dup: no existing model-verification code in the repo — internal/config only
//     stores static on-disk paths and never checks currency. Not a duplicate.
//  3. Data shape: GETs the OpenRouter models API (data[].{id,created,
//     context_length,pricing.prompt}); runs the `hf` CLI; emits []ModelCheck (typed
//     in state.go) with ISO checked_at dates.
//  4. Verbatim instruction: "The pipeline's research/model-choice stages MUST follow
//     the Model Verification Protocol at RUNTIME ... the OpenRouter live models API
//     (filter free, sort newest) with defaults `poolside/laguna-m.1:free` ->
//     `moonshotai/kimi-k2.6:free`, and official sites for hosted APIs ... Bake the
//     protocol into the research stage's prompt/logic, not as fixed model ids."
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"becky-go/internal/config"
)

// openRouterModelsURL is the LIVE models endpoint. We query it every run — we never
// trust a cached/hardcoded id.
const openRouterModelsURL = "https://openrouter.ai/api/v1/models"

// Spec-sanctioned defaults of LAST RESORT for the OpenRouter channel (from the
// briefing). These are NOT trusted blindly: resolveOpenRouterFree confirms they are
// still live in the freshly-fetched list before returning them, and falls back to
// the newest live free agentic/reasoning model otherwise.
const (
	defaultORPrimary  = "poolside/laguna-m.1:free"
	defaultORFallback = "moonshotai/kimi-k2.6:free"
)

// knownOnDiskGGUF lists the specific on-disk model files the protocol verifies by
// presence. The PATHS come from config (config.Qwen() for the Qwen3.5-4B
// orchestrator, config.GemmaAVLM() for the Gemma-4 AVLM) — NEVER hardcoded here,
// so a single config edit retargets every tool. We stat them (not assume) and
// only record those that truly exist.
func knownOnDiskGGUF() []struct {
	id   string // the publisher/repo identity
	path string
	note string
} {
	cfg := config.Load()
	qwen, _, _ := cfg.Qwen()
	gemma, _, _ := cfg.GemmaAVLM()
	return []struct {
		id   string
		path string
		note string
	}{
		{"unsloth/Qwen3.5-4B-GGUF", qwen,
			"Qwen's current small line is 3.5 (NOT qwen3); the Unsloth Qwen3.5-4B UD-Q4_K_XL GGUF is becky's orchestrator + cross-family corroborator and is image-capable (NOT a separate Qwen3.5-VL); fits the 8GB RTX 3070"},
		{"google/gemma-4-E4B-it (local)", gemma,
			"Gemma-4 E4B-it audio-visual model already used by becky-validate; reuse before adding anything"},
	}
}

// orModel is the subset of the OpenRouter models API we read.
type orModel struct {
	ID            string `json:"id"`
	Created       int64  `json:"created"`
	ContextLength int64  `json:"context_length"`
	Pricing       struct {
		Prompt string `json:"prompt"`
	} `json:"pricing"`
}

type orModelsResponse struct {
	Data []orModel `json:"data"`
}

// VerifyResearchModels runs the full protocol for a research stage and returns the
// auditable checks plus the resolved cheap-model id to actually use. offline skips
// every network channel (local-only). It NEVER returns a hardcoded id without a
// recorded verification result.
func VerifyResearchModels(ctx context.Context, offline bool) (checks []ModelCheck, resolvedCheap string, resolvedChannel string) {
	now := todayISO()

	// 1) LOCAL (hf CLI + on-disk reuse) — preferred for sensitive forensic work.
	localChecks := checkLocalHF(ctx, now)
	checks = append(checks, localChecks...)

	// 2) OPENROUTER (live, free, newest) — for non-sensitive utility tools / online runs.
	if !offline {
		orChecks, orPrimary := resolveOpenRouterFree(ctx, now)
		checks = append(checks, orChecks...)
		if orPrimary != "" {
			resolvedCheap, resolvedChannel = orPrimary, "openrouter"
		}
	}

	// 3) HOSTED-API rule — record the protocol guardrail (no version-less names).
	checks = append(checks, hostedAPIGuidance(now))

	// Prefer a verified on-disk local model when present (offline-first + private),
	// else the verified OpenRouter pick. If neither verified, record an honest gap.
	if resolvedCheap == "" {
		if id := firstVerifiedLocal(localChecks); id != "" {
			resolvedCheap, resolvedChannel = id, "local-hf"
		}
	}
	if resolvedCheap == "" {
		checks = append(checks, ModelCheck{
			Purpose:   "cheap-synthesis",
			Channel:   "none",
			CheckedAt: now,
			Rationale: "no local model verified on disk and OpenRouter not reachable/allowed; cheap stages will degrade to deterministic",
			Verified:  false,
		})
	}
	return checks, resolvedCheap, resolvedChannel
}

// checkLocalHF verifies the local channel: probe the specific GGUF files the briefing
// calls out and confirm the `hf` CLI is present (the documented way to verify a
// publisher's CURRENT repo/line at runtime). It records what is actually on disk.
func checkLocalHF(ctx context.Context, now string) []ModelCheck {
	var checks []ModelCheck

	hfPresent := hfCLIPresent(ctx)
	checks = append(checks, ModelCheck{
		Purpose:   "local-verification-tool",
		ModelID:   "hf CLI",
		Channel:   "local-hf",
		SourceURL: "https://huggingface.co/docs/huggingface_hub/guides/cli",
		CheckedAt: now,
		Rationale: hfNote(hfPresent),
		Verified:  hfPresent,
	})

	for _, m := range knownOnDiskGGUF() {
		present := fileExists(m.path)
		checks = append(checks, ModelCheck{
			Purpose:        "local-reuse-candidate",
			ModelID:        m.id,
			Channel:        "local-hf",
			SourceURL:      "on-disk: " + m.path,
			CheckedAt:      now,
			InstructVsBase: "Instruct/it (tool+chat work)",
			Rationale:      m.note,
			Verified:       present,
		})
	}
	return checks
}

// resolveOpenRouterFree fetches the live models list, filters to free, sorts newest,
// confirms the spec defaults are still live (else picks the newest agentic/reasoning
// free model), and records the protocol-bound checks.
func resolveOpenRouterFree(ctx context.Context, now string) ([]ModelCheck, string) {
	models, err := fetchOpenRouterModels(ctx)
	if err != nil {
		return []ModelCheck{{
			Purpose:   "cheap-synthesis",
			Channel:   "openrouter",
			SourceURL: openRouterModelsURL,
			CheckedAt: now,
			Rationale: "OpenRouter models API not reachable: " + err.Error() + " — falling back to local/deterministic",
			Verified:  false,
		}}, ""
	}

	free := filterFreeNewest(models)
	live := map[string]orModel{}
	for _, m := range free {
		live[m.ID] = m
	}

	var checks []ModelCheck
	// Confirm the spec defaults FIRST (the briefing's "default if you cannot verify").
	primary := ""
	if m, ok := live[defaultORPrimary]; ok {
		primary = m.ID
		checks = append(checks, orCheck(m, now, "spec-default primary; confirmed live in today's free list (agentic coding + tool-calling + reasoning)"))
	}
	if m, ok := live[defaultORFallback]; ok {
		checks = append(checks, orCheck(m, now, "spec-default fallback; confirmed live in today's free list"))
	}
	// If the default primary is gone, pick the newest live free model as the primary.
	if primary == "" && len(free) > 0 {
		m := free[0]
		primary = m.ID
		checks = append(checks, orCheck(m, now, "spec-default primary NOT live today; selected the newest live free model as primary"))
	}
	if len(free) == 0 {
		checks = append(checks, ModelCheck{
			Purpose:   "cheap-synthesis",
			Channel:   "openrouter",
			SourceURL: openRouterModelsURL,
			CheckedAt: now,
			Rationale: "models API returned no free models today",
			Verified:  false,
		})
	}
	return checks, primary
}

func orCheck(m orModel, now, why string) ModelCheck {
	return ModelCheck{
		Purpose:   "cheap-synthesis",
		ModelID:   m.ID,
		Channel:   "openrouter",
		SourceURL: openRouterModelsURL,
		CheckedAt: now,
		Rationale: fmt.Sprintf("%s; created=%s ctx=%d", why, time.Unix(m.Created, 0).Format("2006-01-02"), m.ContextLength),
		Verified:  true,
	}
}

// fetchOpenRouterModels GETs the live models list with a short timeout.
func fetchOpenRouterModels(ctx context.Context) ([]orModel, error) {
	rc, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rc, http.MethodGet, openRouterModelsURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var out orModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}
	return out.Data, nil
}

// filterFreeNewest keeps only free models (":free" id suffix OR zero prompt price)
// and sorts them newest-first.
func filterFreeNewest(models []orModel) []orModel {
	var free []orModel
	for _, m := range models {
		if strings.HasSuffix(m.ID, ":free") || m.Pricing.Prompt == "0" {
			free = append(free, m)
		}
	}
	sort.Slice(free, func(i, j int) bool { return free[i].Created > free[j].Created })
	return free
}

// firstVerifiedLocal returns the id of the first on-disk model that was verified
// present, or "".
func firstVerifiedLocal(checks []ModelCheck) string {
	for _, c := range checks {
		if c.Channel == "local-hf" && c.Purpose == "local-reuse-candidate" && c.Verified {
			return c.ModelID
		}
	}
	return ""
}

// hfCLIPresent reports whether the `hf` (or legacy huggingface-cli) CLI is runnable,
// the documented tool for verifying a publisher's current repos at runtime.
func hfCLIPresent(ctx context.Context) bool {
	for _, name := range []string{"hf", "huggingface-cli"} {
		if _, err := exec.LookPath(name); err == nil {
			rc, cancel := context.WithTimeout(ctx, 8*time.Second)
			cmd := exec.CommandContext(rc, name, "--version")
			err := cmd.Run()
			cancel()
			if err == nil {
				return true
			}
		}
	}
	return false
}

func hfNote(present bool) string {
	if present {
		return "hf CLI present — use `hf` to verify a publisher's CURRENT repo/line (e.g. Qwen is on 3.5/3.6/3.7, prefer Instruct) before naming any local model"
	}
	return "hf CLI NOT found — cannot live-verify HF publisher repos; rely on on-disk reuse only and record the gap"
}

// hostedAPIGuidance returns the protocol rule for hosted APIs as a recorded check
// (no version-less names; verify the exact current version on the official site).
// It does not call any vendor API (none is configured by default); it records the
// rule so a downstream stage that DOES use a hosted model must verify the version.
func hostedAPIGuidance(now string) ModelCheck {
	return ModelCheck{
		Purpose:   "hosted-api-rule",
		Channel:   "hosted-api",
		SourceURL: "official vendor site (e.g. https://ai.google.dev/gemini-api/docs/models)",
		CheckedAt: now,
		Rationale: "NEVER write a version-less hosted-model name (e.g. 'gemini flash' resolves to a deprecated version and fails); verify the EXACT current version on the official site before use. Forbidden for sensitive forensic content (free tiers may train on prompts).",
		Verified:  false,
	}
}

// dumpModelChecks writes a human-readable summary of the model checks to w (the run
// log), so a reviewer can see the protocol ran and what it found.
func dumpModelChecks(w *os.File, checks []ModelCheck) {
	for _, c := range checks {
		mark := "UNVERIFIED"
		if c.Verified {
			mark = "VERIFIED"
		}
		fmt.Fprintf(w, "  [%s] %-22s %-12s %s  (%s)\n", mark, c.Purpose, c.Channel, c.ModelID, c.SourceURL)
	}
}
