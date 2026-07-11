// resident.go — reuse an already-running multimodal llama-server instead of
// spawning a duplicate.
//
// The single biggest VRAM win in the resource forensics (docs/research/
// resource-forensics.md #1): when WHORETANA is up, its resident Gemma-4 brain
// already holds a multimodal llama-server on 127.0.0.1:8033. A becky-vision
// --gemma call would otherwise spawn a SECOND copy of that same multi-GB model
// (+4–7 GB) onto the shared 8 GB GPU → oversubscription → the 100% GPU event.
// ResidentServerURL lets becky detect that resident server and route through it
// via the existing Runner.ServerURL reuse path instead — zero extra VRAM, and
// faster (no per-call multi-GB model reload from disk).
package avlm

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// residentProbeTimeout caps the resident-server check so a missing/slow server
// costs at most this long before we fall back to spawning our own.
const residentProbeTimeout = 3 * time.Second

// residentProps is the subset of llama-server's /props we read: which model it
// loaded (model_path) and whether that model has a vision projector.
type residentProps struct {
	ModelPath  string `json:"model_path"`
	Modalities struct {
		Vision bool `json:"vision"`
	} `json:"modalities"`
}

// ResidentServerURL reports whether the multimodal llama-server at baseURL is
// already serving the SAME model becky needs — matched by GGUF basename — with a
// vision projector loaded (i.e. WHORETANA's resident brain on 127.0.0.1:8033).
// When it is, it returns (baseURL, true) so the caller reuses that server; when
// it is not, it returns ("", false) so the caller spawns a fresh one.
//
// It NEVER errors. Any failure — nothing listening, server still loading, model
// mismatch (e.g. resident is E2B but becky needs E4B, or becky needs the 12B),
// no vision modality, unparseable response — all mean "no reuse", so becky
// safely falls back to spawning. That fallback is exactly the mismatch case
// AUTOPILOT Law 20 covers.
//
// ponytail: match is by basename, not a content hash — two builds of a
// same-named GGUF are treated as interchangeable. If that ever bites, compare
// meta.size from /v1/models here instead.
func ResidentServerURL(ctx context.Context, baseURL, wantModel string, logf func(string, ...any)) (string, bool) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" || strings.TrimSpace(wantModel) == "" {
		return "", false
	}

	pctx, cancel := context.WithTimeout(ctx, residentProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(pctx, http.MethodGet, baseURL+"/props", nil)
	if err != nil {
		return "", false
	}
	resp, err := (&http.Client{Timeout: residentProbeTimeout}).Do(req)
	if err != nil {
		return "", false // nothing reachable (or still loading) → spawn
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}

	var p residentProps
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return "", false
	}
	if !p.Modalities.Vision || p.ModelPath == "" {
		return "", false // not a vision server we can reuse for a still
	}
	if !strings.EqualFold(filepath.Base(p.ModelPath), filepath.Base(wantModel)) {
		logf("avlm: resident server at %s serves %s, need %s — spawning fresh",
			baseURL, filepath.Base(p.ModelPath), filepath.Base(wantModel))
		return "", false
	}
	logf("avlm: reusing resident llama-server at %s (%s) — no duplicate model load",
		baseURL, filepath.Base(p.ModelPath))
	return baseURL, true
}
