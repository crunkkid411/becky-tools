package main

import (
	"context"
	"fmt"
	"strings"

	"becky-go/internal/avlm"
	"becky-go/internal/config"
	"becky-go/internal/ctlagent"
	"becky-go/internal/edittools"
	"becky-go/internal/footage"
	"becky-go/internal/llamacpp"
	"becky-go/internal/llmlocal"
)

// localModel adapts internal/llmlocal.Client (the warm Gemma-4 text server) to the
// ctlagent.Model interface. This is "the built-in llama, the AI model that has
// shared context with the program" (Jordan): the SAME Gemma-4 QAT GGUF the AVLM
// uses, driven here in text mode for the NL->tool agent loop. It loads once and
// stays warm for the session so the iterative editing loop is fast (the spec's
// in-loop workhorse, SPEC-BECKY-NLE §3.5).
type localModel struct {
	c *llmlocal.Client
}

// newLocalModel builds the agent's model. It PREFERS the IN-PROCESS Gemma-4 QAT
// (llama.dll via cgo — SPEC-BECKY-NLE §3.5's "embedded llama is the inner-loop
// workhorse") when this binary was built with `-tags llamacgo`; otherwise it falls
// back to the warm llama-server (HTTP). Returns (model, closeFn, note); model is a
// true-nil interface when no model is available so the bridge degrades the agent verb
// to an honest "no model" note rather than failing.
func newLocalModel(cfg config.Config, logf func(string, ...any)) (ctlagent.Model, func(), string) {
	model, _, label := cfg.GemmaAVLM()

	// 1) In-process llama.dll (the real Gemma-4 QAT GGUF loaded into this process).
	if llamacpp.Available() {
		if err := llamacpp.Load(model, "", -1, 4096); err == nil {
			logf("becky-edit: in-process Gemma-4 loaded (%s)", label)
			return &inProcessModel{}, llamacpp.Close, label + " (in-process llama.dll)"
		} else {
			logf("becky-edit: in-process llama unavailable (%v); trying llama-server", err)
		}
	}

	// 2) Warm llama-server fallback.
	c := llmlocal.NewWarmClient(model, cfg.LlamaServer, logf)
	if err := c.Available(); err != nil {
		return nil, nil, "agent disabled: " + err.Error()
	}
	lm := &localModel{c: c}
	return lm, lm.Close, label
}

// Complete runs one system+user exchange against the warm Gemma server. Thinking
// stays off so the JSON tool call lands in message.content.
func (m *localModel) Complete(ctx context.Context, system, user string) (string, error) {
	return m.c.Chat(ctx, system, user, llmlocal.Options{MaxTokens: 512})
}

// inProcessModel adapts the in-process llama.dll binding (internal/llamacpp) to the
// ctlagent.Model interface. It formats the prompt with the Gemma chat template (Gemma
// has no system role, so the system text is folded into the user turn) and decodes
// greedily for a deterministic JSON tool call. The context is not cancellable mid-decode
// (the cgo call is synchronous); the loop is fast enough that this is acceptable.
type inProcessModel struct{}

func (inProcessModel) Complete(_ context.Context, system, user string) (string, error) {
	var b strings.Builder
	b.WriteString("<start_of_turn>user\n")
	if system != "" {
		b.WriteString(system)
		b.WriteString("\n\n")
	}
	b.WriteString(user)
	b.WriteString("<end_of_turn>\n<start_of_turn>model\n")
	return llamacpp.Complete(b.String(), 512, 0, 42)
}

// Close shuts the warm server down (frees VRAM).
func (m *localModel) Close() {
	if m != nil && m.c != nil {
		m.c.Close()
	}
}

// enrich is the ctlagent.Enricher: for the read/produce verbs it runs the REAL
// becky work (transcript search via internal/footage, vision via internal/avlm)
// and folds the output into the Result the model sees next turn. Deterministic
// verbs never reach here (the loop only calls enrich for non-mutating verbs).
// Degrades to the queued-command result on any failure — never crashes the loop.
func (b *Bridge) enrich(ctx context.Context, call edittools.ToolCall, res edittools.Result, _ []edittools.HostCommand) edittools.Result {
	switch call.Verb {
	case "search":
		return b.enrichSearch(call, res)
	case "vision":
		return b.enrichVision(ctx, call, res)
	default:
		return res
	}
}

// enrichSearch runs the deterministic transcript grep over the open folder index
// and summarises the top hits for the model.
func (b *Bridge) enrichSearch(call edittools.ToolCall, res edittools.Result) edittools.Result {
	if b.index == nil {
		res.Data = mergeData(res.Data, map[string]any{"answer": "no folder is open; cannot search"})
		return res
	}
	query, _ := call.Args["query"].(string)
	hits := footage.GrepTranscripts(*b.index, strings.Fields(query))
	if len(hits) > maxEnrichHits {
		hits = hits[:maxEnrichHits]
	}
	lines := make([]string, 0, len(hits))
	for _, h := range hits {
		lines = append(lines, fmt.Sprintf("%s @ %.1f-%.1fs: %s", baseName(h.Source), h.Timestamp, h.End, truncate(h.Text, 60)))
	}
	answer := fmt.Sprintf("%d hit(s)", len(hits))
	if len(lines) > 0 {
		answer += ":\n" + strings.Join(lines, "\n")
	}
	res.Data = mergeData(res.Data, map[string]any{"answer": answer, "hits": hits})
	return res
}

// enrichVision runs the Gemma-4 multimodal AVLM over the requested span and
// returns its answer. It reuses the SAME model family as the text loop, but with
// the mmproj for vision+audio. Degrades to a note when the model isn't available.
func (b *Bridge) enrichVision(ctx context.Context, call edittools.ToolCall, res edittools.Result) edittools.Result {
	model, mmproj, _ := b.cfg.GemmaAVLM()
	runner := avlm.New(model, mmproj, b.cfg.LlamaServer, "", b.cfg.FFmpeg, b.cfg.FFprobe, b.logf)
	if err := runner.Ready(); err != nil {
		res.Data = mergeData(res.Data, map[string]any{"answer": "vision unavailable: " + err.Error()})
		return res
	}
	src, _ := call.Args["source"].(string)
	question, _ := call.Args["question"].(string)
	in, _ := toF(call.Args["in"])
	out, _ := toF(call.Args["out"])
	win := out - in
	if win <= 0 {
		win = 10
	}
	ar, err := runner.Analyze(ctx, avlm.Options{
		Clip:        src,
		Prompt:      question,
		WindowStart: in,
		WindowSec:   win,
		FPS:         1.0,
		MaxTokens:   384,
		Temperature: 0.2,
		Seed:        42,
	})
	if err != nil {
		res.Data = mergeData(res.Data, map[string]any{"answer": "vision degraded: " + err.Error()})
		return res
	}
	res.Data = mergeData(res.Data, map[string]any{"answer": ar.Text})
	return res
}

const maxEnrichHits = 8

// ensure the enrich method satisfies ctlagent.Enricher at compile time.
var _ ctlagent.Enricher = (&Bridge{}).enrich

func mergeData(dst, add map[string]any) map[string]any {
	if dst == nil {
		dst = map[string]any{}
	}
	for k, v := range add {
		dst[k] = v
	}
	return dst
}

func toF(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}
