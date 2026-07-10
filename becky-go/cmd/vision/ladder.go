// ladder.go — the compiled-in escalation core from becky-AI-Agent-review-1.md.
//
// Tonight (2026-07-09) becky-vision ran ONLY the 450M LFM2.5-VL model, always,
// and returned a confidently-WRONG answer on a screen full of readable text
// ("nothing is stuck" on a terminal frozen at a permission prompt) while the
// 1.6B / Gemma-4 E4B / Gemma-4 12B rungs sat on disk, unused. The review's
// acceptance criteria (§4, #1/#2/#4/#9) demand a single no-flags call that
// decides FOR ITSELF whether to climb the ladder — never the caller picking a
// model via a flag.
//
// This file is that policy. It is deliberately NOT the full fix: OCR
// corroboration (a mandatory rung whenever on-screen text is detected, plus a
// real cross-source "agrees" signal) is slice B. What this slice adds are the
// three signals that ARE available without OCR:
//  1. promptImpliesTextOrUI  — the prompt itself smells like a text/UI read
//     (the review's exact failure mode: a tiny model confidently misreads a
//     screen because nobody told the ladder text-reading was even at stake).
//  2. looksUncertain         — the model's own hedge language (does NOT catch
//     a model being confidently wrong — see the review's incident — which is
//     exactly why signal 1 exists as an independent trigger).
//  3. disagree               — two rungs landing on OPPOSITE sides of the
//     stuck-vs-ready axis is real evidence of ambiguity even when neither
//     answer hedges.
//
// Most calls ("describe this photo") never leave rung 0 — escalation only
// costs time on the prompts/answers that actually look shaky.
package main

import (
	"context"
	"strings"
	"time"

	"becky-go/internal/avlm"
	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/vision"
)

// MaxEscalations bounds the ladder at 4 total rungs (450M, 1.6B, Gemma-4 E4B,
// Gemma-4 12B) — the exact ladder named in becky-AI-Agent-review-1.md. Extra
// rungs added later stay capped at this many escalations beyond rung 0 unless
// this constant changes with them.
const MaxEscalations = 3

// gemmaRungTimeoutSec bounds one Gemma-4 tier's llama-server spin-up + single
// inference. Matches becky-vision's existing --timeout default for --gemma/--qwen.
const gemmaRungTimeoutSec = 240

// rungResult is what one escalation rung produced: free text plus whether it
// is usable. ok=false covers every degrade path (missing model/mmproj/binary,
// empty model output, llama-server failing to start) — never a panic.
type rungResult struct {
	text string
	ok   bool
}

// rungCall is one tier of the ladder: a short label (becomes Result.Model /
// Source.Model — a NAME, never a raw GGUF path, so the envelope doesn't leak
// the machinery per the review's F3), the model family (Result.Engine), and
// the closure that actually invokes it. Closures are only called when the
// policy decides to climb that far, so cheap "describe this photo" calls never
// pay for a Gemma-4 llama-server spin-up.
type rungCall struct {
	label  string
	engine string
	run    func() rungResult
}

// runLadder is becky-vision's DEFAULT (no model flags) path. --gemma/--qwen
// remain as manual single-model overrides for debugging; every unflagged call
// goes through this policy instead of the old hardcoded 450M-only Describe.
func runLadder(image, prompt string, verbose bool) vision.Result {
	if strings.TrimSpace(prompt) == "" {
		prompt = vision.DefaultPrompt
	}
	cfg := config.Load()

	rungs := []rungCall{
		{
			label: "lfm2.5-vl-450m", engine: "LFM2.5-VL",
			run: func() rungResult {
				r := vision.Describe(vision.Options{Image: image, Prompt: prompt})
				return rungResult{text: r.Description, ok: !r.Degraded}
			},
		},
		{
			label: "lfm2.5-vl-1.6b", engine: "LFM2.5-VL",
			run: func() rungResult {
				r := vision.Describe(vision.Options{Image: image, Prompt: prompt, ModelDir: vision.Dir16B})
				return rungResult{text: r.Description, ok: !r.Degraded}
			},
		},
		{
			label: "gemma-4-E4B", engine: "Gemma-4",
			run: func() rungResult {
				return runGemmaRung(cfg, cfg.GemmaModel, cfg.GemmaMMProj, image, prompt, verbose)
			},
		},
		{
			label: "gemma-4-12B", engine: "Gemma-4",
			run: func() rungResult {
				return runGemmaRung(cfg, cfg.GemmaModel12B, cfg.GemmaMMProj12B, image, prompt, verbose)
			},
		},
	}

	return runEscalationPolicy(image, prompt, rungs, verbose)
}

// runGemmaRung is the Gemma-4 tier's rungCall body (E4B and 12B share it —
// only the model/mmproj paths differ). A missing model/mmproj (e.g. the 12B
// QAT file not yet downloaded) degrades this ONE rung; the ladder keeps
// whatever the previous rung answered rather than failing the whole call.
func runGemmaRung(cfg config.Config, model, mmproj, image, prompt string, verbose bool) rungResult {
	if strings.TrimSpace(model) == "" || strings.TrimSpace(mmproj) == "" {
		return rungResult{ok: false}
	}
	logf := func(format string, a ...any) { beckyio.Logf(verbose, format, a...) }
	runner := avlm.New(model, mmproj, cfg.LlamaServer, "", cfg.FFmpeg, cfg.FFprobe, logf)

	ctx, cancel := context.WithTimeout(context.Background(), gemmaRungTimeoutSec*time.Second)
	defer cancel()

	out, err := runner.AnalyzeImage(ctx, image, avlm.ImageOptions{Prompt: prompt, Verbose: verbose})
	if err != nil {
		return rungResult{ok: false}
	}
	return rungResult{text: out.Text, ok: true}
}

// runEscalationPolicy drives the ladder over an abstract list of rungs so the
// DECISION logic is testable without spawning a single model (see
// ladder_test.go's fake rungs). Rung 0 always runs; each later rung runs only
// while budget remains AND the current best answer still looks shaky per
// needsMore(). Degraded is true ONLY when every rung failed to produce text —
// otherwise the policy completed, even if the final Confidence is still low.
func runEscalationPolicy(image, prompt string, rungs []rungCall, verbose bool) vision.Result {
	res := vision.Result{Tool: vision.ToolName, Image: image, Prompt: prompt}
	textLikely := promptImpliesTextOrUI(prompt)

	var sources []vision.Source
	var bestText, bestModel, bestEngine string
	finalRung := -1
	needMore := true // rung 0 always runs regardless

	for i, rc := range rungs {
		if i > 0 {
			if !needMore {
				break
			}
			if i > MaxEscalations {
				break
			}
		}
		logRung(verbose, i, rc.label)
		r := rc.run()
		sources = append(sources, vision.Source{Kind: "vlm", Model: rc.label, OK: r.ok})

		if !r.ok {
			needMore = true // nothing usable from this rung; keep climbing if budget allows
			continue
		}

		disagreed := finalRung >= 0 && disagree(bestText, r.text)
		bestText, bestModel, bestEngine, finalRung = r.text, rc.label, rc.engine, i

		switch {
		case i <= 1:
			// Verified 2026-07-09 against the review's own regression image
			// (IMG_7725.JPEG): the 450M AND the 1.6B tiers both confidently
			// agreed on the SAME wrong "nothing stuck" read, so neither
			// looksUncertain() nor disagree() ever fires between them — yet
			// Gemma-4 E4B (rung 2) read the on-screen "Do you want to
			// proceed?" prompt correctly on the first try. Two small LFM
			// tiers agreeing with each other is not evidence they're RIGHT;
			// slice B's real OCR-disagreement check is the proper signal
			// here — until then, a text/UI-implying prompt is never trusted
			// to the small tiers alone, so it always reaches Gemma-4 E4B.
			needMore = textLikely || looksUncertain(r.text)
		default:
			// Gemma-4 E4B (rung 2) is the verified-trustworthy tier for this
			// domain; only its OWN hedging or disagreeing with rung 1 earns
			// the expensive climb to Gemma-4 12B.
			needMore = looksUncertain(r.text) || disagreed
		}
	}

	res.Sources = sources
	if n := len(sources) - 1; n > 0 {
		res.Escalations = n
	}

	if finalRung < 0 {
		res.Degraded = true
		res.Error = "every escalation rung failed to produce a description (see sources)"
		return res
	}

	res.Model = bestModel
	res.Engine = bestEngine
	res.Description = bestText
	res.Confidence = computeConfidence(finalRung, bestText, needMore)
	res.Degraded = false
	return res
}

func logRung(verbose bool, i int, label string) {
	if i == 0 {
		beckyio.Logf(verbose, "becky-vision ladder: rung 0/%d - %s", MaxEscalations, label)
		return
	}
	beckyio.Logf(verbose, "becky-vision ladder: escalating to rung %d/%d - %s", i, MaxEscalations, label)
}

// --- pure decision heuristics (unit tested in ladder_test.go without models) ---

// textUIKeywords are prompt words implying the answer hinges on reading
// on-screen text or UI state — the review's exact incident (a small model
// confidently misread a terminal permission prompt). Their presence forces
// the ladder past rung 0 regardless of how confident the 450M sounds.
var textUIKeywords = []string{
	"text", "read", "reads", "says", "written", "wording", "word",
	"screen", "terminal", "console", "dialog", "prompt", "message",
	"button", "menu", "window", "error", "sign", "label", "title",
	"stuck", "waiting", "state", "status", "click", "type",
}

// promptImpliesTextOrUI reports whether prompt implies the answer depends on
// reading on-screen text or UI/application state.
func promptImpliesTextOrUI(prompt string) bool {
	low := strings.ToLower(prompt)
	for _, kw := range textUIKeywords {
		if strings.Contains(low, kw) {
			return true
		}
	}
	return false
}

// hedgePhrases are how a model signals it isn't sure — the only
// self-reported-certainty proxy available without logprobs/OCR. This
// deliberately does NOT catch a model being confidently wrong; that failure
// mode is what promptImpliesTextOrUI and disagree cover instead.
var hedgePhrases = []string{
	"i think", "i'm not sure", "not sure", "unclear", "hard to tell",
	"can't tell", "cannot tell", "appears to", "appear to", "possibly",
	"perhaps", "may be", "might be", "it is unclear", "ambiguous",
	"no visible", "does not appear", "doesn't appear", "seems to",
	"hard to say", "difficult to determine",
}

// looksUncertain reports whether text hedges rather than stating a plain fact.
func looksUncertain(text string) bool {
	low := strings.ToLower(text)
	for _, p := range hedgePhrases {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}

// stuckWords / readyWords back disagree(): a cheap, targeted polarity check
// for the "is this stuck or fine" class of question the review's incident is
// about (and that MissionControl's terminal watchdog actually asks in
// production). Two rungs landing on OPPOSITE sides of this axis is real
// evidence of ambiguity even when neither answer hedges.
//
// negatedReady is checked (and stripped out) BEFORE stuckWords, because a
// phrase like "nothing stuck" contains the bare substring "stuck" — without
// stripping it first, that one phrase would set BOTH stuck and ready true and
// silently cancel itself out of disagree()'s exclusivity check.
var negatedReady = []string{"not stuck", "nothing stuck", "not waiting", "no longer stuck"}
var stuckWords = []string{"stuck", "waiting", "frozen", "blocked", "hung", "unresponsive", "needs input", "needs a response", "permission"}
var readyWords = []string{"ready", "idle", "no issues", "normal", "finished", "complete", "done"}

func polarity(text string) (stuck, ready bool) {
	low := strings.ToLower(text)
	for _, n := range negatedReady {
		if strings.Contains(low, n) {
			ready = true
			low = strings.ReplaceAll(low, n, "")
		}
	}
	for _, w := range stuckWords {
		if strings.Contains(low, w) {
			stuck = true
			break
		}
	}
	for _, w := range readyWords {
		if strings.Contains(low, w) {
			ready = true
			break
		}
	}
	return stuck, ready
}

// disagree reports whether a and b land on opposite sides of the
// stuck-vs-ready axis (and neither is merely hedging both ways at once).
func disagree(a, b string) bool {
	aStuck, aReady := polarity(a)
	bStuck, bReady := polarity(b)
	return (aStuck && !aReady && bReady && !bStuck) || (bStuck && !bReady && aReady && !aStuck)
}

// rungBaseConfidence is the heuristic starting point per rung: a bigger,
// stronger model earns a higher base confidence for the SAME kind of answer.
// This is rule-based on the signals actually available this slice (no
// logprobs, no OCR agreement yet) — honest, not fabricated.
var rungBaseConfidence = []float64{0.50, 0.65, 0.80, 0.92}

// computeConfidence turns (which rung answered, does the final text hedge,
// did we run out of budget while still unsure) into a 0.05-0.99 score.
func computeConfidence(rung int, text string, exhaustedStillUncertain bool) float64 {
	conf := 0.5
	if rung >= 0 && rung < len(rungBaseConfidence) {
		conf = rungBaseConfidence[rung]
	}
	if looksUncertain(text) {
		conf -= 0.25
	}
	if exhaustedStillUncertain {
		conf -= 0.10
	}
	if conf < 0.05 {
		conf = 0.05
	}
	if conf > 0.99 {
		conf = 0.99
	}
	return conf
}
