// Package llmlocal is a reusable local-LLM transport for becky tools that need a
// small offline text model. It is lifted (not imported) from cmd/ask/llama.go:
// the SAME pattern internal/avlm uses — spawn a transient llama-server.exe on a
// free localhost port, wait for /health, POST an OpenAI-style
// /v1/chat/completions, read message.content. cgo-free; HTTP only.
//
// Two callers share it now: cmd/ask (the intent classifier) and
// internal/assistant (the becky-clip "Underlord" Tier-1 backend). The original
// cmd/ask/llama.go is deliberately left untouched; this package is the shared
// home so the assistant does not duplicate the spawn/health/chat logic.
//
// Degrade-never-crash is load-bearing: a missing GGUF, a missing binary, a
// server that won't come up, or an unparseable reply all return a plain error
// the caller turns into an offline fallback. Available() reports up-front
// whether the client can run at all, so callers can route around an absent model
// without ever attempting a spawn.
//
// Warm-server option: NewClient spawns per Chat call (cheap to reason about,
// frees GPU VRAM between turns). NewWarmClient keeps one server resident for the
// session (faster, holds VRAM) — Close() shuts it down. The becky-clip assistant
// uses the warm client so repeated turns reuse the loaded weights + KV cache.
package llmlocal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// serverStartTimeout caps how long we wait for a freshly-spawned llama-server to
// load the GGUF and answer /health. Verified ~11 s for a 4B Q4 on an RTX 3070;
// 120 s leaves headroom for a cold disk. (Carried over from cmd/ask.)
const serverStartTimeout = 120 * time.Second

// Options tune one Chat request. Zero values fall back to deterministic defaults
// (temperature 0, seed 42, 256 max tokens, thinking disabled) — the settings
// that make a local classification reproducible.
type Options struct {
	Temperature *float64 // nil → 0.0 (deterministic)
	Seed        *int     // nil → 42
	MaxTokens   int      // 0 → 256
	// EnableThinking, when false (the default), sends
	// chat_template_kwargs.enable_thinking=false so the answer lands in
	// message.content instead of a stripped reasoning channel. Load-bearing for
	// Qwen "unified" GGUFs (verified at runtime, 2026-06-08).
	EnableThinking bool
}

// Client drives a local llama-server text model over HTTP. Construct it with
// NewClient (spawn-per-call) or NewWarmClient (one resident server). It is safe
// for concurrent Chat calls only with the warm client after the first call has
// started the server; the spawn-per-call client serialises naturally because
// each call owns its own server.
type Client struct {
	model  string // text GGUF path
	server string // llama-server.exe path (or a name resolvable on PATH)
	ngl    int    // GPU layers to offload (99 = full; a 4B Q4 fits 8 GB)
	ctxLen int    // context window
	logf   func(format string, a ...any)

	warm bool // keep one resident server for the session

	mu       sync.Mutex
	baseURL  string // warm: the resident server URL once started
	cleanup  func() // warm: terminate the resident server
	started  bool   // warm: has the resident server been spawned?
	startErr error  // warm: a sticky spawn error so we don't retry a dead box
}

// NewClient builds a spawn-per-call client. logf may be nil. Defaults: -ngl 99,
// 4096-token context — ample for a short classification or action parse.
func NewClient(model, server string, logf func(string, ...any)) *Client {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Client{model: model, server: server, ngl: 99, ctxLen: 4096, logf: logf}
}

// NewWarmClient builds a client that keeps one llama-server resident for the
// session. The first Chat (or an explicit Start) spawns it; Close shuts it down.
// Use this for an interactive loop (e.g. becky-clip) so weights load once.
func NewWarmClient(model, server string, logf func(string, ...any)) *Client {
	c := NewClient(model, server, logf)
	c.warm = true
	return c
}

// NewClientCtx is NewClient with a larger context window. A short classifier is
// fine at the 4096 default, but page-recovery (becky-regrab feeds a whole page's
// text to the model) needs room for input + output, so it spawns the server with
// a bigger -c. ctxLen <= 0 keeps the 4096 default.
func NewClientCtx(model, server string, ctxLen int, logf func(string, ...any)) *Client {
	c := NewClient(model, server, logf)
	if ctxLen > 0 {
		c.ctxLen = ctxLen
	}
	return c
}

// Available reports whether the model GGUF and the llama-server binary both
// exist (a descriptive error otherwise — never panics). Callers use it to
// degrade to a deterministic path without attempting a spawn. (Mirrors
// cmd/ask's Ready, renamed to fit the assistant's Backend.Available() seam.)
func (c *Client) Available() error {
	if c.model == "" {
		return fmt.Errorf("local model path not configured")
	}
	if _, err := os.Stat(c.model); err != nil {
		return fmt.Errorf("local model GGUF not found at %s", c.model)
	}
	if c.server == "" {
		return fmt.Errorf("llama-server path not configured")
	}
	if _, err := os.Stat(c.server); err != nil {
		if _, lerr := exec.LookPath(c.server); lerr != nil {
			return fmt.Errorf("llama-server not found at %s", c.server)
		}
	}
	return nil
}

// Chat sends one system+user exchange and returns the assistant's text (expected
// to be a JSON object or short string the caller parses). Any failure is a plain
// error → the caller falls back. With the warm client the resident server is
// reused; with the spawn-per-call client a transient server is started and torn
// down around this single call.
func (c *Client) Chat(ctx context.Context, system, user string, opts Options) (string, error) {
	if err := c.Available(); err != nil {
		return "", err
	}
	if c.warm {
		baseURL, err := c.ensureWarm(ctx)
		if err != nil {
			return "", err
		}
		return c.post(ctx, baseURL, system, user, opts)
	}
	baseURL, cleanup, err := c.spawnServer(ctx)
	if err != nil {
		return "", err
	}
	defer cleanup()
	return c.post(ctx, baseURL, system, user, opts)
}

// Close shuts down the resident server (warm client only). Safe to call on a
// spawn-per-call client (no-op) and safe to call more than once.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cleanup != nil {
		c.cleanup()
		c.cleanup = nil
	}
	c.started = false
	c.baseURL = ""
}

// ensureWarm lazily spawns the resident server on first use and returns its URL.
// A sticky startErr prevents hammering a box that already failed to come up.
func (c *Client) ensureWarm(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started && c.baseURL != "" {
		return c.baseURL, nil
	}
	if c.startErr != nil {
		return "", c.startErr
	}
	baseURL, cleanup, err := c.spawnServer(ctx)
	if err != nil {
		c.startErr = err
		return "", err
	}
	c.baseURL = baseURL
	c.cleanup = cleanup
	c.started = true
	return baseURL, nil
}

// post POSTs one /v1/chat/completions request and returns the assistant text.
func (c *Client) post(ctx context.Context, baseURL, system, user string, opts Options) (string, error) {
	temp := 0.0
	if opts.Temperature != nil {
		temp = *opts.Temperature
	}
	seed := 42
	if opts.Seed != nil {
		seed = *opts.Seed
	}
	maxTok := opts.MaxTokens
	if maxTok <= 0 {
		maxTok = 256
	}

	reqBody := map[string]any{
		"model":       "local",
		"temperature": temp,
		"seed":        seed,
		"max_tokens":  maxTok,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"chat_template_kwargs": map[string]bool{"enable_thinking": opts.EnableThinking},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal chat request: %w", err)
	}

	resp, err := postJSON(ctx, baseURL+"/v1/chat/completions", payload)
	if err != nil {
		return "", fmt.Errorf("llama-server request: %w", err)
	}

	var cr struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &cr); err != nil {
		return "", fmt.Errorf("parse llama-server reply: %w", err)
	}
	if cr.Error != nil {
		return "", fmt.Errorf("llama-server error: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("llama-server returned no choices")
	}
	return cr.Choices[0].Message.Content, nil
}

// spawnServer launches a transient text-only llama-server on a free localhost
// port and waits for /health. The returned cleanup terminates it. Server logs go
// to a temp file so they never pollute the caller's UI.
func (c *Client) spawnServer(ctx context.Context) (string, func(), error) {
	port, err := freePort()
	if err != nil {
		return "", nil, fmt.Errorf("allocate server port: %w", err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d", port)

	args := []string{
		"-m", c.model,
		"-ngl", strconv.Itoa(c.ngl),
		"-c", strconv.Itoa(c.ctxLen),
		"--no-warmup",
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
	}
	c.logf("llmlocal: spawning llama-server on %s (-ngl %d)…", url, c.ngl)

	cmd := exec.Command(c.server, args...)
	logFile, _ := os.CreateTemp("", "becky_llmlocal_*.log")
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	if err := cmd.Start(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return "", nil, fmt.Errorf("start llama-server: %w", err)
	}

	cleanup := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
		if logFile != nil {
			name := logFile.Name()
			logFile.Close()
			os.Remove(name)
		}
	}

	if err := waitForHealth(ctx, url, serverStartTimeout); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("llama-server did not become healthy: %w", err)
	}
	c.logf("llmlocal: llama-server ready at %s", url)
	return url, cleanup, nil
}

// waitForHealth polls GET /health until 200 or the deadline passes.
func waitForHealth(ctx context.Context, baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 5 * time.Second}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
		resp, err := client.Do(req)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("health check timed out after %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
}

// postJSON POSTs a JSON body and returns the raw response bytes. The HTTP client
// has no timeout of its own; the caller's context governs the deadline.
func postJSON(ctx context.Context, url string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return data, nil
}

// freePort asks the OS for an unused TCP port on localhost.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
