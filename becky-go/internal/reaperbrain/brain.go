// Package reaperbrain resolves and launches the local llama.cpp llama-server that
// REAPER's "REAPER Chat" extension talks to for natural-language DAW control.
//
// THE LIVE BLOCKER (reaper1.jpg, 2026-06-20): Jordan typed a plain-English request
// into REAPER Chat ("change tempo to 128, add a four-on-the-floor kick…") and it
// failed with `Failed to connect to http://localhost:11435/v1/chat/completions`.
// Nothing was listening on 11435. REAPER Chat POSTs to that HARD-CODED port,
// expecting an OpenAI-compatible chat endpoint.
//
// becky's standard backend is llama.cpp's `llama-server` (NOT Ollama — Jordan's
// explicit, repeated requirement). This package finds a chat GGUF and the
// llama-server binary on disk, binds them to port 11435, and serves the endpoint
// REAPER Chat expects — so its natural-language DAW control just works.
//
// Everything here is deterministic and degrade-never-crash: Resolve never panics;
// a missing model or binary becomes a descriptive error the CLI prints in plain
// language. The actual server launch is the only side-effecting step and it too
// degrades cleanly when nothing is installed (e.g. on a cloud/CI box).
package reaperbrain

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"becky-go/internal/pathx"
)

const (
	// DefaultPort is the port REAPER Chat is hard-wired to. Do not change it
	// without also changing the extension's config (Jordan can't).
	DefaultPort = 11435
	// DefaultHost binds loopback only — this is a local brain, never exposed.
	DefaultHost = "127.0.0.1"

	// DefaultServer is becky's canonical llama-server build path (matches
	// internal/config.LlamaServer).
	DefaultServer = `C:\llama.cpp\build\bin\llama-server.exe`
	// DefaultModel is the becky-owned chat GGUF used across the suite (matches
	// internal/canvas.DefaultTransformModel) — a solid default for DAW chat.
	DefaultModel = `X:\AI-2\becky-tools\models\Qwen3-4B-Instruct-2507-Q4_K_M.gguf`
	// ModelDir is the becky-owned model root scanned when no explicit model is set.
	ModelDir = `X:\AI-2\becky-tools\models`

	// Env overrides (so Jordan never edits code to point at a different model).
	EnvServer = "BECKY_LLAMA_SERVER"
	EnvModel  = "BECKY_REAPER_MODEL"
	EnvPort   = "BECKY_REAPER_BRAIN_PORT"

	defaultNGL    = 99   // offload all layers; a 4B Q4 fits an 8 GB RTX 3070
	defaultCtxLen = 4096 // ample for DAW-control turns
)

// Config is a fully-resolved launch plan for the REAPER brain. Build it with a
// Resolver; render the command with Args/CommandLine; serve it with Start.
type Config struct {
	Server string // llama-server binary (or a PATH-resolvable name)
	Model  string // chat GGUF path
	Host   string
	Port   int
	NGL    int
	CtxLen int
}

// Resolver locates the model + binary. The function fields are injectable so the
// whole resolution is unit-testable with no real filesystem. Use NewResolver for
// the real os-backed implementation.
type Resolver struct {
	Getenv   func(string) string            // os.Getenv
	Exists   func(string) bool              // true if the path exists on disk
	LookPath func(string) (string, error)   // exec.LookPath
	Glob     func(string) ([]string, error) // filepath.Glob
}

// Args returns the exact llama-server argv (WITHOUT the binary) that serves the
// OpenAI-compatible endpoint REAPER Chat expects on the configured host:port.
func (c Config) Args() []string {
	return []string{
		"-m", c.Model,
		"-ngl", strconv.Itoa(c.NGL),
		"-c", strconv.Itoa(c.CtxLen),
		"--host", c.Host,
		"--port", strconv.Itoa(c.Port),
	}
}

// CommandLine renders a copy-pasteable command string (binary + args, each
// whitespace-containing token quoted). Deterministic for a given Config.
func (c Config) CommandLine() string {
	parts := append([]string{c.Server}, c.Args()...)
	for i, p := range parts {
		if strings.ContainsAny(p, " \t") {
			parts[i] = `"` + p + `"`
		}
	}
	return strings.Join(parts, " ")
}

// BaseURL is the server root REAPER Chat resolves against.
func (c Config) BaseURL() string {
	return fmt.Sprintf("http://%s:%d", c.Host, c.Port)
}

// ChatCompletionsURL is the exact endpoint REAPER Chat POSTs to.
func (c Config) ChatCompletionsURL() string { return c.BaseURL() + "/v1/chat/completions" }

// HealthURL is llama-server's readiness probe.
func (c Config) HealthURL() string { return c.BaseURL() + "/health" }

// Resolve produces a launch Config from env + disk. It ALWAYS returns a usable
// Config (host/port/ngl/ctx filled, and whatever model/server could be located);
// the error is non-nil only when the model or the binary could not be found, so
// callers can still print the intended command for the user to run by hand.
func (r Resolver) Resolve() (Config, error) {
	c := Config{Host: DefaultHost, Port: DefaultPort, NGL: defaultNGL, CtxLen: defaultCtxLen}

	if p := strings.TrimSpace(r.Getenv(EnvPort)); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 && n <= 65535 {
			c.Port = n
		}
	}

	server, serverErr := r.resolveServer()
	c.Server = server
	model, modelErr := r.resolveModel()
	c.Model = model

	switch {
	case serverErr != nil && modelErr != nil:
		return c, fmt.Errorf("%v; and %v", serverErr, modelErr)
	case serverErr != nil:
		return c, serverErr
	case modelErr != nil:
		return c, modelErr
	}
	return c, nil
}

// resolveServer: env override → becky default build → PATH (llama-server[.exe]).
// On not-found it returns the default path (so the printed command is still
// sensible) plus a descriptive error.
func (r Resolver) resolveServer() (string, error) {
	if e := strings.TrimSpace(r.Getenv(EnvServer)); e != "" {
		if r.Exists(e) {
			return e, nil
		}
		return e, fmt.Errorf("llama-server not found at %s (%s)", e, EnvServer)
	}
	if r.Exists(DefaultServer) {
		return DefaultServer, nil
	}
	for _, name := range []string{"llama-server", "llama-server.exe"} {
		if p, err := r.LookPath(name); err == nil && p != "" {
			return p, nil
		}
	}
	return DefaultServer, fmt.Errorf("llama-server not found (looked at %s, %s, and PATH; set %s)", EnvServer, DefaultServer, EnvServer)
}

// resolveModel: env override → becky default GGUF → best chat GGUF found under
// ModelDir. On not-found it returns the default path plus a descriptive error.
func (r Resolver) resolveModel() (string, error) {
	if e := strings.TrimSpace(r.Getenv(EnvModel)); e != "" {
		if r.Exists(e) {
			return e, nil
		}
		return e, fmt.Errorf("model GGUF not found at %s (%s)", e, EnvModel)
	}
	if r.Exists(DefaultModel) {
		return DefaultModel, nil
	}
	if best := r.scanModelDir(); best != "" {
		return best, nil
	}
	return DefaultModel, fmt.Errorf("no chat GGUF found (looked at %s, %s, and %s; set %s)", EnvModel, DefaultModel, ModelDir, EnvModel)
}

// scanModelDir globs ModelDir (one level deep) for *.gguf and returns the
// highest-scoring chat model, ties broken lexically for determinism.
func (r Resolver) scanModelDir() string {
	if r.Glob == nil {
		return ""
	}
	var cands []string
	for _, pat := range []string{ModelDir + `\*.gguf`, ModelDir + `\*\*.gguf`} {
		if m, err := r.Glob(pat); err == nil {
			cands = append(cands, m...)
		}
	}
	best, bestScore := "", -1
	for _, p := range cands {
		s := scoreModel(p)
		if s < 0 {
			continue // disqualified (embedding/mmproj/vad/…)
		}
		if s > bestScore || (s == bestScore && (best == "" || p < best)) {
			best, bestScore = p, s
		}
	}
	return best
}

// scoreModel ranks a GGUF filename for use as a DAW-chat model. Non-chat models
// (embeddings, mmproj projectors, VAD, vision/rerank) score -1 (disqualified);
// otherwise the score rewards instruct/chat indicators. Separator-agnostic so a
// Windows path resolves correctly even when this runs on Linux/CI.
func scoreModel(path string) int {
	name := strings.ToLower(pathx.Base(path))
	for _, bad := range []string{"embedding", "embed", "mmproj", "-vad", "vad.", "rerank", "vision", "clip", "mtmd"} {
		if strings.Contains(name, bad) {
			return -1
		}
	}
	score := 0
	for _, good := range []string{"instruct", "chat", "-it-", "-it.", "qwen", "gemma", "llama", "mistral", "phi", "smol"} {
		if strings.Contains(name, good) {
			score++
		}
	}
	return score
}

// CheckHealth reports whether a server is already answering on baseURL/health
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
