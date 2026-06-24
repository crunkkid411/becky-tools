// model.go — the real local-model backends for becky-scout's autonomous gate.
//
// Two independent models, both driven through llama.cpp's llama-server exactly
// the way becky-ask (cmd/ask/llama.go) and internal/avlm do — spawn a transient
// server on a free port, wait /health, POST /v1/chat/completions, read the reply:
//
//   - qwenProposer: the user's Qwen3.5-4B judges a video and, if it's genuinely
//     worth it, PROPOSES a concrete becky tool (strict-JSON intake fields).
//   - gemmaJudge:   Gemma-4 independently VOTES on that proposal. A different
//     model than the proposer, so agreement is real corroboration.
//
// Both degrade rather than crash: if a model GGUF or llama-server is missing, or
// a server won't start, PickProposeModels returns "no models" and scout simply
// skips the autonomous step (the deterministic report still prints). Determinism:
// temperature 0, fixed seed.
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
	"strings"
	"time"

	"becky-go/internal/config"
	"becky-go/internal/scout"
)

// serverStartTimeout caps how long we wait for a freshly-spawned llama-server to
// load a GGUF and answer /health (generous for a cold disk; ~11s warm on a 3070).
const serverStartTimeout = 150 * time.Second

// chatModel is a transient llama-server bound to one GGUF, reused across all
// videos in a run (started once, closed at the end).
type chatModel struct {
	model       string
	server      string
	thinkingOff bool // send chat_template_kwargs.enable_thinking=false (Qwen line)
	baseURL     string
	cleanup     func()
}

// ready reports whether the model GGUF and llama-server binary both exist.
func (m *chatModel) ready() error {
	if m.model == "" {
		return fmt.Errorf("model path not configured")
	}
	if _, err := os.Stat(m.model); err != nil {
		return fmt.Errorf("model GGUF not found at %s", m.model)
	}
	if m.server == "" {
		return fmt.Errorf("llama-server path not configured")
	}
	if _, err := os.Stat(m.server); err != nil {
		if _, lerr := exec.LookPath(m.server); lerr != nil {
			return fmt.Errorf("llama-server not found at %s", m.server)
		}
	}
	return nil
}

// start spawns the server and waits for health. cleanup is set on success.
func (m *chatModel) start(ctx context.Context) error {
	if err := m.ready(); err != nil {
		return err
	}
	port, err := freePort()
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	cmd := exec.Command(m.server,
		"-m", m.model, "-ngl", "99", "-c", "8192", "--no-warmup",
		"--host", "127.0.0.1", "--port", strconv.Itoa(port))
	logFile, _ := os.CreateTemp("", "becky_scout_llama_*.log")
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	if err := cmd.Start(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return fmt.Errorf("start llama-server: %w", err)
	}
	m.cleanup = func() {
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
	if err := waitHealth(ctx, url, serverStartTimeout); err != nil {
		m.cleanup()
		m.cleanup = nil
		return fmt.Errorf("llama-server did not become healthy: %w", err)
	}
	m.baseURL = url
	return nil
}

// chat sends one system+user turn and returns the assistant text.
func (m *chatModel) chat(ctx context.Context, system, user string) (string, error) {
	body := map[string]any{
		"model":       "becky",
		"temperature": 0.0,
		"seed":        42,
		"max_tokens":  512,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	}
	if m.thinkingOff {
		body["chat_template_kwargs"] = map[string]bool{"enable_thinking": false}
	}
	payload, _ := json.Marshal(body)
	raw, err := postJSON(ctx, m.baseURL+"/v1/chat/completions", payload)
	if err != nil {
		return "", err
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
	if err := json.Unmarshal(raw, &cr); err != nil {
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

// ---- scout.Proposer / scout.Judge implementations -----------------------------

type qwenProposer struct {
	m   *chatModel
	ctx context.Context
}

func (q qwenProposer) Propose(it scout.Item) (scout.Proposal, error) {
	user := videoBrief(it)
	out, err := q.m.chat(q.ctx, proposerSystem, user)
	if err != nil {
		return scout.Proposal{}, err
	}
	var p scout.Proposal
	if err := json.Unmarshal([]byte(extractJSON(out)), &p); err != nil {
		return scout.Proposal{}, fmt.Errorf("proposer JSON: %w", err)
	}
	if p.Slug != "" && !strings.HasPrefix(p.Slug, "becky-") {
		p.Slug = "becky-" + p.Slug
	}
	return p, nil
}

type gemmaJudge struct {
	m    *chatModel
	ctx  context.Context
	name string
}

func (g gemmaJudge) Name() string { return g.name }

func (g gemmaJudge) Vote(p scout.Proposal, it scout.Item) (scout.JudgeVote, error) {
	user := fmt.Sprintf("PROPOSED becky tool:\n  name: %s\n  does: %s\n  takes: %s -> gives: %s\n  why: %s\n\nSOURCE VIDEO:\n%s",
		p.Slug, p.Capability, p.InputKind, p.OutputKind, p.Why, videoBrief(it))
	out, err := g.m.chat(g.ctx, judgeSystem, user)
	if err != nil {
		return scout.JudgeVote{}, err
	}
	var v scout.JudgeVote
	if err := json.Unmarshal([]byte(extractJSON(out)), &v); err != nil {
		return scout.JudgeVote{}, fmt.Errorf("judge JSON: %w", err)
	}
	v.Judge = g.name
	return v, nil
}

// videoBrief renders the offline-readable facts about a video for a prompt.
func videoBrief(it scout.Item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "title: %s\n", it.Title)
	if it.Channel != "" {
		fmt.Fprintf(&b, "channel: %s\n", it.Channel)
	}
	if len(it.BeckyTools) > 0 {
		fmt.Fprintf(&b, "scout sees becky areas: %s\n", strings.Join(it.BeckyTools, ", "))
	}
	if len(it.Interests) > 0 {
		fmt.Fprintf(&b, "jordan interest areas: %s\n", strings.Join(it.Interests, ", "))
	}
	desc := it.Description
	if len(desc) > 1500 {
		desc = desc[:1500]
	}
	if desc != "" {
		fmt.Fprintf(&b, "description: %s\n", desc)
	}
	if len(it.Tags) > 0 {
		fmt.Fprintf(&b, "tags: %s\n", strings.Join(it.Tags, ", "))
	}
	return b.String()
}

const proposerSystem = `You are becky-scout's proposer. becky-tools is a suite of OFFLINE, deterministic, single-purpose command-line tools: each takes a file or JSON in and writes JSON out. Given a YouTube video that may describe a model, technique, or tool, decide whether becky should build ONE new small tool inspired by it.

Only say worth_building=true when there is a CONCRETE, buildable, becky-appropriate tool (offline, single-purpose, file/JSON in -> JSON out). Most videos are NOT worth a new tool; say false for hype, news, vague tips, or anything needing a cloud service.

Reply with STRICT JSON and nothing else:
{"worth_building": true|false, "slug": "becky-kebab-name", "capability": "one sentence, what it does", "input_kind": "video|audio|image|url|json|text", "output_kind": "json|csv|text", "kind": "improve|extend", "why": "why this video justifies it"}`

const judgeSystem = `You are an INDEPENDENT reviewer of a proposed becky tool. becky tools are offline, deterministic, single-purpose (file/JSON in -> JSON out). Approve ONLY if the proposal is concrete, genuinely useful, buildable offline, single-purpose, and does not just duplicate an obvious existing tool. Be skeptical — it is fine to reject.

Reply with STRICT JSON and nothing else:
{"agree": true|false, "why": "short reason", "confidence": 0.0-1.0}`

// extractJSON returns the first {...} object in s (models sometimes wrap JSON in
// prose or code fences). Falls back to the whole trimmed string.
func extractJSON(s string) string {
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return strings.TrimSpace(s)
}

// PickProposeModels resolves and STARTS the proposer (Qwen) and judge (Gemma)
// servers. It returns the proposer, the judges, a cleanup func, and ok=false (with
// a reason) when the models/binary aren't available — in which case scout skips
// the autonomous step and just prints its report. Overrides:
//
//	BECKY_SCOUT_PROPOSE_MODEL  Qwen GGUF (default: the becky-ask intent model)
//	BECKY_SCOUT_JUDGE_MODEL    Gemma GGUF (default: cfg.GemmaAVLM())
func PickProposeModels(ctx context.Context) (scout.Proposer, []scout.Judge, func(), bool, string) {
	cfg := config.Load()
	server := cfg.LlamaServer

	qwenPath := strings.TrimSpace(os.Getenv("BECKY_SCOUT_PROPOSE_MODEL"))
	if qwenPath == "" {
		qwenPath = defaultProposeModel
	}
	judgePath := strings.TrimSpace(os.Getenv("BECKY_SCOUT_JUDGE_MODEL"))
	if judgePath == "" {
		judgePath, _, _ = cfg.GemmaAVLM()
	}

	proposerModel := &chatModel{model: qwenPath, server: server, thinkingOff: true}
	if err := proposerModel.start(ctx); err != nil {
		return nil, nil, func() {}, false, "proposer (Qwen) unavailable: " + err.Error()
	}
	judgeModel := &chatModel{model: judgePath, server: server}
	if err := judgeModel.start(ctx); err != nil {
		proposerModel.cleanup()
		return nil, nil, func() {}, false, "judge (Gemma) unavailable: " + err.Error()
	}

	cleanup := func() {
		judgeModel.cleanup()
		proposerModel.cleanup()
	}
	proposer := qwenProposer{m: proposerModel, ctx: ctx}
	judges := []scout.Judge{gemmaJudge{m: judgeModel, ctx: ctx, name: "gemma-4"}}
	return proposer, judges, cleanup, true, ""
}

// defaultProposeModel is the same Qwen GGUF becky-ask uses for intent. Override
// with BECKY_SCOUT_PROPOSE_MODEL.
const defaultProposeModel = `X:\HuggingFace\models\unsloth\Qwen3.5-4B-GGUF\Qwen3.5-4B-Q4_K_M.gguf`

// ---- tiny HTTP/port helpers (same approach as cmd/ask/llama.go) ----------------

func waitHealth(ctx context.Context, baseURL string, timeout time.Duration) error {
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

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
