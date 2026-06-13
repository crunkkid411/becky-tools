// llama.go — the local-only intent model transport for becky-ask.
//
// It drives the user's Qwen3.5-4B GGUF (X:\HuggingFace\models\unsloth\
// Qwen3.5-4B-GGUF\Qwen3.5-4B-Q4_K_M.gguf) through llama.cpp's llama-server, the
// SAME transport pattern internal/avlm uses for Gemma-4 (spawn a transient
// server on a free port -> wait /health -> POST an OpenAI-style
// /v1/chat/completions -> read message.content). This is a TEXT-only classifier,
// so no mmproj / frames / audio — just a system prompt + the user's request.
//
// Two settings carried over from internal/avlm because they are load-bearing for
// this Qwen "unified" line too:
//   - chat_template_kwargs.enable_thinking=false, so the answer lands in
//     message.content instead of a stripped reasoning channel (verified at
//     runtime against this exact GGUF on 2026-06-08).
//   - flash-attention left at the llama-server default (no mmproj here).
//
// Everything degrades rather than crashes: a missing model or binary, a server
// that won't come up, or an unparseable reply returns an error the caller turns
// into the offline keyword-catalog fallback. becky-ask NEVER hard-fails on the
// model being absent — that is the brief's "degrade to the offline keyword
// catalog if llama.cpp/model is unavailable."
package main

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
	"time"
)

// intentServerStartTimeout caps how long we wait for a freshly-spawned
// llama-server to load the ~2.7 GB GGUF and answer /health. Verified ~11 s on the
// RTX 3070; 120 s leaves generous headroom for a cold disk.
const intentServerStartTimeout = 120 * time.Second

// llamaClient holds the resolved paths for the local intent model. Construct it
// from config (no hardcoding); Ready() reports whether it can run at all.
type llamaClient struct {
	Model  string // Qwen3.5-4B GGUF (cfg.AskIntentModel)
	Server string // llama-server.exe (cfg.LlamaServer)
	NGL    int    // GPU layers to offload (99 = full; the 4B Q4 fits 8 GB)
	CtxLen int    // context window (4096 is ample for a short classification)
	Logf   func(format string, a ...any)
}

// newLlamaClient builds a client with sane defaults. logf may be nil.
func newLlamaClient(model, server string, logf func(string, ...any)) *llamaClient {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &llamaClient{Model: model, Server: server, NGL: 99, CtxLen: 4096, Logf: logf}
}

// Ready reports whether the model GGUF and the llama-server binary both exist.
// It returns a descriptive error (never panics) so the caller can degrade to the
// keyword catalog with an honest note naming what was missing.
func (c *llamaClient) Ready() error {
	if c.Model == "" {
		return fmt.Errorf("intent model path not configured")
	}
	if _, err := os.Stat(c.Model); err != nil {
		return fmt.Errorf("intent model GGUF not found at %s", c.Model)
	}
	if c.Server == "" {
		return fmt.Errorf("llama-server path not configured")
	}
	if _, err := os.Stat(c.Server); err != nil {
		if _, lerr := exec.LookPath(c.Server); lerr != nil {
			return fmt.Errorf("llama-server not found at %s", c.Server)
		}
	}
	return nil
}

// classify spawns the intent server (if needed), sends the system + user prompt,
// and returns the assistant's raw text (expected to be a JSON object the caller
// parses). Any failure is returned as a plain error -> caller falls back.
func (c *llamaClient) classify(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	if err := c.Ready(); err != nil {
		return "", err
	}
	baseURL, cleanup, err := c.spawnServer(ctx)
	if err != nil {
		return "", err
	}
	defer cleanup()
	return c.chat(ctx, baseURL, systemPrompt, userPrompt)
}

// chat POSTs one /v1/chat/completions request (system + user) and returns the
// assistant text. Thinking is disabled so the JSON lands in message.content.
func (c *llamaClient) chat(ctx context.Context, baseURL, systemPrompt, userPrompt string) (string, error) {
	reqBody := map[string]any{
		"model":       "qwen",
		"temperature": 0.0, // deterministic classification
		"seed":        42,
		"max_tokens":  256,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"chat_template_kwargs": map[string]bool{"enable_thinking": false},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal intent request: %w", err)
	}

	resp, err := postJSONIntent(ctx, baseURL+"/v1/chat/completions", payload)
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
// to a temp file so they never pollute becky-ask's UI.
func (c *llamaClient) spawnServer(ctx context.Context) (string, func(), error) {
	port, err := freeIntentPort()
	if err != nil {
		return "", nil, fmt.Errorf("allocate server port: %w", err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d", port)

	args := []string{
		"-m", c.Model,
		"-ngl", strconv.Itoa(c.NGL),
		"-c", strconv.Itoa(c.CtxLen),
		"--no-warmup",
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
	}
	c.Logf("ask: spawning llama-server on %s (-ngl %d) for intent…", url, c.NGL)

	cmd := exec.Command(c.Server, args...)
	logFile, _ := os.CreateTemp("", "becky_ask_llama_*.log")
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

	if err := waitForIntentHealth(ctx, url, intentServerStartTimeout); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("llama-server did not become healthy: %w", err)
	}
	c.Logf("ask: intent llama-server ready at %s", url)
	return url, cleanup, nil
}

// waitForIntentHealth polls GET /health until 200 or the deadline passes.
func waitForIntentHealth(ctx context.Context, baseURL string, timeout time.Duration) error {
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

// postJSONIntent POSTs a JSON body and returns the raw response bytes. The HTTP
// client has no timeout of its own; the caller's context governs the deadline.
func postJSONIntent(ctx context.Context, url string, body []byte) ([]byte, error) {
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

// freeIntentPort asks the OS for an unused TCP port on localhost.
func freeIntentPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
