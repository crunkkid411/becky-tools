// model.go — the ONLY model-dependent path: a self-contained OpenAI-style
// llama-server client that powers LocalSelector (semantic selection, SPEC §4.1)
// and LLMExpander (yes/no neighbor judgments, SPEC §5).
//
// Per the becky-clip build brief this client is written here from scratch
// (stdlib net/http only) and does NOT import internal/llmlocal (a sibling agent
// owns that package). It mirrors the proven transport in cmd/ask/llama.go: spawn
// a transient llama-server on a free port -> wait /health -> POST
// /v1/chat/completions -> read message.content. Everything degrades to a clear
// error (never a crash, never a fabricated selection): a missing model/binary or
// an unreachable server returns an error that the CLI surfaces with a suggestion
// to use --exact or --select-from-json.
//
// An ALREADY-RUNNING server can be targeted via BaseURL (e.g. the resident
// embedding/chat server), skipping the spawn entirely — handy for the GUI which
// may keep a llama-server warm.
package quotes

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
	"strings"
	"time"
)

// serverStartTimeout caps how long we wait for a freshly-spawned llama-server to
// load the GGUF and answer /health (matches cmd/ask/llama.go's headroom).
const serverStartTimeout = 120 * time.Second

// LocalClient is the minimal local-LLM transport. Construct from config paths
// (no hardcoding). If BaseURL is set, it is used as-is (assume an external server
// is already up); otherwise Server+Model are spawned transiently per call.
type LocalClient struct {
	Model       string  // instruct GGUF path (cfg-provided)
	Server      string  // llama-server.exe (cfg.LlamaServer)
	BaseURL     string  // optional already-running endpoint; skips spawn when set
	Temperature float64 // 0 for reproducibility (SPEC §5/§13.7)
	NGL         int     // GPU layers (99 = full)
	CtxLen      int     // context window
	Logf        func(format string, a ...any)
}

// NewLocalClient builds a client with sane defaults. logf may be nil.
func NewLocalClient(model, server string, temperature float64, logf func(string, ...any)) *LocalClient {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &LocalClient{
		Model: model, Server: server, Temperature: temperature,
		NGL: 99, CtxLen: 8192, Logf: logf,
	}
}

// Available reports whether this client can run at all: either a BaseURL is set,
// or both the model GGUF and the llama-server binary exist. Returns a descriptive
// error (never panics) so the caller can degrade with an honest, specific note.
func (c *LocalClient) Available() error {
	if strings.TrimSpace(c.BaseURL) != "" {
		return nil
	}
	if c.Model == "" {
		return fmt.Errorf("local model path not configured (set --model or config)")
	}
	if _, err := os.Stat(c.Model); err != nil {
		return fmt.Errorf("local model GGUF not found at %s", c.Model)
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

// chat POSTs one system+user turn and returns the assistant text. It targets
// BaseURL if set, else spawns a transient server for the call. Thinking is
// disabled so the answer lands in message.content (carried over from avlm/ask
// for this Qwen "unified" line).
func (c *LocalClient) chat(ctx context.Context, systemPrompt, userPrompt string, maxTokens int) (string, error) {
	if err := c.Available(); err != nil {
		return "", err
	}
	baseURL := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if baseURL == "" {
		url, cleanup, err := c.spawnServer(ctx)
		if err != nil {
			return "", err
		}
		defer cleanup()
		baseURL = url
	}

	reqBody := map[string]any{
		"model":                "becky-quotes",
		"temperature":          c.Temperature,
		"seed":                 42,
		"max_tokens":           maxTokens,
		"chat_template_kwargs": map[string]bool{"enable_thinking": false},
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
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
// port and waits for /health. The returned cleanup terminates it; logs go to a
// temp file so they never pollute stdout/stderr.
func (c *LocalClient) spawnServer(ctx context.Context) (string, func(), error) {
	port, err := freePort()
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
	c.Logf("quotes: spawning llama-server on %s (-ngl %d)…", url, c.NGL)

	cmd := exec.Command(c.Server, args...)
	logFile, _ := os.CreateTemp("", "becky_quotes_llama_*.log")
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
	c.Logf("quotes: llama-server ready at %s", url)
	return url, cleanup, nil
}

// waitForHealth polls GET /health until 200 or the deadline.
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

// postJSON POSTs a JSON body and returns the raw response bytes. No client-side
// timeout; the caller's context governs the deadline.
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

// freePort asks the OS for an unused localhost TCP port.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// ---- LocalSelector / LLMExpander -----------------------------------------

// LocalSelector is the model-backed Selector (SPEC §4.1). It asks the local model
// to return the verbatim quotes that satisfy the criteria, as a strict JSON
// array. It NEVER fabricates: an unavailable model returns an error the CLI turns
// into a clear note suggesting --exact / --select-from-json.
type LocalSelector struct {
	Client *LocalClient
}

// selectSystemPrompt instructs the model to return ONLY a JSON array of verbatim
// quote objects — no prose, no paraphrase. The downstream snapper requires the
// quotes to appear verbatim in the transcript, so paraphrase would simply fail to
// resolve (and is filtered as unmatched).
const selectSystemPrompt = `You are a forensic transcript analyst. You are given a transcript and a SELECTION CRITERIA. Return ONLY the passages that satisfy the criteria.

Rules:
- Output a STRICT JSON array, nothing else. No prose, no markdown fences.
- Each element: {"quote": "<verbatim words copied EXACTLY from the transcript>", "because": "<one short reason>"}.
- Copy quotes VERBATIM from the transcript text. Do NOT paraphrase, summarize, or correct wording.
- Select only what genuinely matches the criteria. If nothing matches, output [].`

// Select asks the model for matching quotes and returns them as anchors. The
// transcript is sent whole here; the engine handles chunking for long inputs
// (SPEC §4.5) by calling Select per window.
func (s *LocalSelector) Select(ctx context.Context, transcript, criteria string) ([]Anchor, error) {
	if s.Client == nil {
		return nil, fmt.Errorf("local selector has no model client")
	}
	if err := s.Client.Available(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(criteria) == "" {
		criteria = "the most quotable / decision-relevant statements; skip filler and small talk"
	}
	user := "SELECTION CRITERIA:\n" + criteria + "\n\nTRANSCRIPT:\n" + transcript
	raw, err := s.Client.chat(ctx, selectSystemPrompt, user, 1024)
	if err != nil {
		return nil, err
	}
	return parseSelectionJSON(raw), nil
}

// parseSelectionJSON extracts {quote,because} objects from the model's reply,
// tolerating stray prose/markdown by slicing to the outermost JSON array. A
// reply we cannot parse yields no anchors (never a crash, never a fabrication).
func parseSelectionJSON(raw string) []Anchor {
	body := sliceJSONArray(raw)
	if body == "" {
		return nil
	}
	var items []struct {
		Quote   string `json:"quote"`
		Because string `json:"because"`
	}
	if err := json.Unmarshal([]byte(body), &items); err != nil {
		return nil
	}
	var anchors []Anchor
	for _, it := range items {
		q := strings.TrimSpace(it.Quote)
		if q == "" {
			continue
		}
		anchors = append(anchors, Anchor{Quote: q, Cue: -1, Because: strings.TrimSpace(it.Because)})
	}
	return anchors
}

// sliceJSONArray returns the substring from the first '[' to the last ']'
// inclusive, or "" if absent — a forgiving extractor for models that wrap JSON
// in prose or code fences.
func sliceJSONArray(s string) string {
	start := strings.IndexByte(s, '[')
	end := strings.LastIndexByte(s, ']')
	if start < 0 || end < 0 || end < start {
		return ""
	}
	return s[start : end+1]
}

// LLMExpander implements Expander via the local model (SPEC §5). It asks a
// strict yes/no question and degrades to (false, error) when the model is down,
// so expansion simply stops rather than crashing.
type LLMExpander struct {
	Client *LocalClient
}

const expandSystemPrompt = `You decide whether including a NEIGHBOR sentence is necessary to understand a QUOTE BLOCK from a transcript. Answer with ONLY one word: "yes" or "no". Say "yes" only if the neighbor provides context without which the block is unclear, ambiguous, or misleading.`

// NeedsContext returns true iff the model answers "yes". Any error (model
// unavailable, unparseable reply) returns (false, err) so the loop stops cleanly.
func (e *LLMExpander) NeedsContext(ctx context.Context, block, neighbor string) (bool, error) {
	if e.Client == nil {
		return false, fmt.Errorf("expander has no model client")
	}
	if err := e.Client.Available(); err != nil {
		return false, err
	}
	user := "QUOTE BLOCK:\n" + block + "\n\nNEIGHBOR SENTENCE:\n" + neighbor + "\n\nIs the neighbor necessary context? Answer yes or no."
	raw, err := e.Client.chat(ctx, expandSystemPrompt, user, 8)
	if err != nil {
		return false, err
	}
	ans := strings.ToLower(strings.TrimSpace(raw))
	return strings.HasPrefix(ans, "yes"), nil
}
