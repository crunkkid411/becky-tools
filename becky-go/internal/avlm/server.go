// server.go — the llama-server transport for Gemma-4 multimodal inference.
//
// invokeServer either reuses an already-running multimodal llama-server
// (Runner.ServerURL) or spawns a transient one bound to a free localhost port,
// waits for /health, POSTs an OpenAI-style /v1/chat/completions request with the
// frames as image_url parts + the audio as an input_audio part, and returns the
// assistant message content.
//
// Two non-obvious settings are load-bearing (see avlm.go header):
//   - we drive llama-server, NOT llama-mtmd-cli (the CLI hard-crashes on Gemma-4).
//   - chat_template_kwargs.enable_thinking=false, so the answer lands in
//     message.content instead of a stripped reasoning channel.
//   - flash-attention is OFF (-fa off): the Gemma-4 mmproj misbehaves with it.
package avlm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// contentPart models the subset of the OpenAI chat content schema we send.
type contentPart struct {
	Type       string      `json:"type"`
	Text       string      `json:"text,omitempty"`
	ImageURL   *imageURL   `json:"image_url,omitempty"`
	InputAudio *inputAudio `json:"input_audio,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type inputAudio struct {
	Data   string `json:"data"`
	Format string `json:"format"`
}

type chatMessage struct {
	Role    string        `json:"role"`
	Content []contentPart `json:"content"`
}

// chatRequest is the /v1/chat/completions body. enable_thinking=false is passed
// through chat_template_kwargs (the documented Gemma-4 unified toggle).
type chatRequest struct {
	Model             string          `json:"model"`
	Temperature       float64         `json:"temperature"`
	Seed              int             `json:"seed"`
	MaxTokens         int             `json:"max_tokens"`
	Messages          []chatMessage   `json:"messages"`
	ChatTemplateKwarg map[string]bool `json:"chat_template_kwargs,omitempty"`
}

// chatResponse is the subset of the reply we read.
type chatResponse struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// invokeServer runs one multimodal request against llama-server and returns the
// assistant text. Every recoverable failure is a *DegradeError. It resolves a
// base URL (reusing Runner.ServerURL or spawning a transient server) and then
// delegates to chat. This is the legacy single-shot "all frames in one request"
// path; the two-stage caption+synthesize flow uses ensureServer + chat directly.
func (r *Runner) invokeServer(ctx context.Context, prompt string, opts Options, frames []string, audioPath string) (string, error) {
	baseURL, cleanup, err := r.ensureServer(ctx)
	if err != nil {
		return "", err // already a *DegradeError
	}
	defer cleanup()

	// Build the multimodal user message: text first, then images, then audio.
	parts := []contentPart{{Type: "text", Text: prompt}}
	for _, f := range frames {
		b64, err := readBase64(f)
		if err != nil {
			r.Logf("avlm: skipping frame %s (%v)", filepath.Base(f), err)
			continue
		}
		parts = append(parts, contentPart{
			Type:     "image_url",
			ImageURL: &imageURL{URL: "data:image/jpeg;base64," + b64},
		})
	}
	if audioPath != "" {
		b64, err := readBase64(audioPath)
		if err != nil {
			r.Logf("avlm: skipping audio (%v)", err)
		} else {
			parts = append(parts, contentPart{
				Type:       "input_audio",
				InputAudio: &inputAudio{Data: b64, Format: "wav"},
			})
		}
	}

	r.Logf("avlm: single-shot request (%d image(s), audio=%v, %d tok)...",
		len(frames), audioPath != "", opts.MaxTokens)
	return r.chat(ctx, baseURL, opts.SystemPrompt, parts, opts.Temperature, opts.Seed, opts.MaxTokens)
}

// ensureServer returns a usable base URL and a cleanup func. When Runner.ServerURL
// is set it reuses that endpoint (cleanup is a no-op); otherwise it spawns a
// transient server the caller must clean up. Resolving once lets a multi-request
// flow (per-frame captioning + synthesis) reuse a single warm server.
func (r *Runner) ensureServer(ctx context.Context) (string, func(), error) {
	if r.ServerURL != "" {
		r.Logf("avlm: reusing llama-server at %s", r.ServerURL)
		return r.ServerURL, func() {}, nil
	}
	return r.spawnServer(ctx)
}

// chat POSTs one /v1/chat/completions request (an optional system message + the
// given user content parts) and returns the assistant text. It always disables
// the Gemma-4 thinking channel. Every recoverable failure is a *DegradeError.
func (r *Runner) chat(ctx context.Context, baseURL, systemPrompt string, parts []contentPart, temperature float64, seed, maxTokens int) (string, error) {
	var messages []chatMessage
	if systemPrompt != "" {
		messages = append(messages, chatMessage{Role: "system", Content: []contentPart{{Type: "text", Text: systemPrompt}}})
	}
	messages = append(messages, chatMessage{Role: "user", Content: parts})

	reqBody := chatRequest{
		Model:             "gemma4",
		Temperature:       temperature,
		Seed:              seed,
		MaxTokens:         maxTokens,
		Messages:          messages,
		ChatTemplateKwarg: map[string]bool{"enable_thinking": false},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", degrade("cannot marshal chat request", err)
	}

	start := time.Now()
	resp, err := postJSON(ctx, baseURL+"/v1/chat/completions", payload)
	if err != nil {
		return "", degrade("llama-server request failed", err)
	}
	dur := time.Since(start)

	var cr chatResponse
	if err := json.Unmarshal(resp, &cr); err != nil {
		return "", degrade("cannot parse llama-server response", fmt.Errorf("%w: %s", err, tail(string(resp))))
	}
	if cr.Error != nil {
		return "", degrade("llama-server error", fmt.Errorf("%s", cr.Error.Message))
	}
	if len(cr.Choices) == 0 {
		return "", degrade("llama-server returned no choices", nil)
	}
	out := cr.Choices[0].Message.Content
	r.Logf("avlm: inference done in %s (%d chars, finish=%s)",
		dur.Round(time.Millisecond), len(out), cr.Choices[0].FinishReason)
	return out, nil
}

// spawnServer launches a transient multimodal llama-server on a free localhost
// port and waits for it to answer /health. The returned cleanup terminates it.
func (r *Runner) spawnServer(ctx context.Context) (string, func(), error) {
	port, err := freePort()
	if err != nil {
		return "", nil, degrade("cannot allocate a server port", err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d", port)

	args := []string{
		"-m", r.Model,
		"--mmproj", r.MMProj,
		"-ngl", itoa(r.NGL),
		"-c", "16384", // must fit frames (~256 tok each) + audio + prompt; 8192 overflowed the default ~30-frame window (HTTP 400)
		"-fa", "off",
		"--no-warmup",
		"--host", "127.0.0.1",
		"--port", itoa(port),
	}
	r.Logf("avlm: spawning llama-server on %s (-ngl %d)...", url, r.NGL)

	cmd := exec.Command(r.Server, args...)
	// Send server logs to a temp file so they never pollute the tool's stdout.
	logFile, _ := os.CreateTemp("", "becky_llama_server_*.log")
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	if err := cmd.Start(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return "", nil, degrade("cannot start llama-server", err)
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
		return "", nil, degrade("llama-server did not become healthy", err)
	}
	r.Logf("avlm: llama-server ready at %s", url)
	return url, cleanup, nil
}

// waitForHealth polls GET /health until it returns 200 or the deadline passes.
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
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, tail(string(data)))
	}
	return data, nil
}
