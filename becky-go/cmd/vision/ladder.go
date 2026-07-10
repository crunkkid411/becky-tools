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
// This file is that policy. Slice A (2026-07-09) shipped three signals
// available WITHOUT OCR:
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
// Slice B (2026-07-10) adds MANDATORY becky-ocr corroboration: whenever
// promptImpliesTextOrUI is true (the same cheap, cost-free heuristic — a real
// "is there text in this frame" image detector would cost the model time a
// plain describe call can't afford; see gatherOCRSignal), becky-ocr's engine
// (internal/vision.RunOCR, the SAME engine becky-ocr.exe uses) runs ONCE
// up front. Its high-confidence lines feed three things: (a) the final
// Description (verbatim on-screen text, not just the VLM's paraphrase), (b)
// the per-rung needMore decision (an on-screen prompt while the model says
// "ready" forces another climb — ocrDisagreesWithReady), and (c) the
// Confidence/Validated/Sources envelope (a "kind":"ocr" Source with its own
// Agrees + KeyLines).
//
// Most calls ("describe this photo") never leave rung 0 and never run OCR —
// both escalation and OCR corroboration only cost time on the prompts/answers
// that actually look shaky or text-implying.
package main

import (
	"context"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"becky-go/internal/avlm"
	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/vision"
)

// EnvMaxEscalations overrides MaxEscalations for one process — the "fast
// mode" knob for the testdata\vision smoke gate (becky-AI-Agent-review-1.md
// §5): the gate sets this to 0 for every-build runs (450M + mandatory OCR
// only, no llama-server rungs spun up) and leaves it unset for the weekly
// full-ladder run. This is still a genuine no-flags call — same convention
// as BECKY_AVLM_VARIANT/BECKY_QWEN_MODEL elsewhere in this codebase: an env
// knob, never a CLI flag, so "becky-vision --image x --prompt y" stays THE
// one-dumb-call contract the review demands.
const EnvMaxEscalations = "BECKY_VISION_MAX_ESCALATIONS"

// MaxEscalations bounds the ladder at, by default, 4 total rungs (450M,
// 1.6B, Gemma-4 E4B, Gemma-4 12B) — the exact ladder named in
// becky-AI-Agent-review-1.md. Extra rungs added later stay capped at this
// many escalations beyond rung 0 unless this constant changes with them.
// Resolved once per process from EnvMaxEscalations so a subprocess (the
// smoke gate spawning becky-vision.exe) can cap it without a flag; an
// unset/invalid/negative env value keeps the built-in default of 3.
var MaxEscalations = resolveMaxEscalations(3)

func resolveMaxEscalations(def int) int {
	v := strings.TrimSpace(os.Getenv(EnvMaxEscalations))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	return n
}

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

	ocr := gatherOCRSignal(cfg, image, prompt, verbose)
	return runEscalationPolicy(image, prompt, rungs, ocr, verbose)
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
//
// ocr is pre-computed by the CALLER (gatherOCRSignal), not fetched here — the
// same seam fakeRungs uses for models, so ladder_test.go can inject canned OCR
// outcomes without spawning PaddleOCR. A zero-value ocrSignal{} (ran=false)
// means OCR was never attempted (the common "plain describe" case) and every
// OCR-aware check below is a no-op.
func runEscalationPolicy(image, prompt string, rungs []rungCall, ocr ocrSignal, verbose bool) vision.Result {
	res := vision.Result{Tool: vision.ToolName, Image: image, Prompt: prompt}
	textLikely := promptImpliesTextOrUI(prompt)

	var sources []vision.Source
	var texts []string // parallel to sources, for the post-loop per-source Agrees pass
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
		texts = append(texts, r.text)

		if !r.ok {
			needMore = true // nothing usable from this rung; keep climbing if budget allows
			continue
		}

		disagreed := finalRung >= 0 && disagree(bestText, r.text)
		ocrDisagreed := ocr.disagreesWithReady(r.text)
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
			// slice B's OCR-disagreement check (ocrDisagreed) now covers
			// exactly this — until then, a text/UI-implying prompt is never
			// trusted to the small tiers alone, so it always reaches E4B.
			needMore = textLikely || looksUncertain(r.text) || ocrDisagreed
		default:
			// Gemma-4 E4B (rung 2) is the verified-trustworthy tier for this
			// domain; only its OWN hedging, disagreeing with rung 1, or
			// contradicting a live on-screen prompt OCR actually read earns
			// the expensive climb to Gemma-4 12B.
			needMore = looksUncertain(r.text) || disagreed || ocrDisagreed
		}
	}

	// vlmEscalations counts only the model rungs run beyond the first — the
	// OCR corroboration source appended below is not a rung and must never
	// inflate this count.
	vlmEscalations := 0
	if n := len(sources) - 1; n > 0 {
		vlmEscalations = n
	}

	if finalRung < 0 {
		res.Sources = sources
		res.Escalations = vlmEscalations
		res.Degraded = true
		res.Error = "every escalation rung failed to produce a description (see sources)"
		return res
	}

	// Retroactively mark each VLM source's Agrees against the FINAL answer
	// (not the answer it saw at the time — the review's own envelope example
	// shows an early, since-overturned rung marked agrees:false).
	for i := range sources {
		sources[i].Agrees = !disagree(texts[i], bestText)
	}

	// Validated: stopped because agreement was actually found, not because the
	// ladder simply ran out of budget while still unsure (a single unconfirmed
	// rung — vlmEscalations==0 — is never "validated": that IS the review's F3
	// incident, an unvalidated guess with no cross-check).
	validated := vlmEscalations > 0 && !needMore
	confText := bestText // the model's own words, BEFORE any OCR text is appended (confidence reads this)

	if ocr.ran {
		sources = append(sources, vision.Source{
			Kind: "ocr", Model: "ppocr-v5",
			OK: ocr.ok, Agrees: ocr.agrees(bestText), KeyLines: ocr.keyLines,
		})
		if ocr.ok {
			validated = ocr.agrees(bestText)
			if len(ocr.keyLines) > 0 {
				bestText = appendOCRText(bestText, ocr.keyLines)
			}
		}
	}

	res.Sources = sources
	res.Escalations = vlmEscalations
	res.Model = bestModel
	res.Engine = bestEngine
	res.Confidence = applyOCRAdjustment(computeConfidence(finalRung, confText, needMore), ocr, confText)
	res.Validated = validated
	res.Degraded = false
	res.Description = bestText
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
//
// FIXED 2026-07-10 (slice B, spotted while re-verifying against the review's
// regression image): "appears to"/"appear to" used to be in this list and
// false-positived on ordinary scene-setting VLM prose ("The image appears to
// show a laptop screen...") that carries zero uncertainty about the actual
// answer — it cost one wasted escalation to Gemma-4 12B on IMG_7725.JPEG even
// though E4B (rung 2) had already answered correctly and confidently. Real
// hedges about the CONCLUSION ("it's hard to tell if...", "not sure whether
// it's stuck") still trip plenty of the phrases below.
var hedgePhrases = []string{
	"i think", "i'm not sure", "not sure", "unclear", "hard to tell",
	"can't tell", "cannot tell", "possibly",
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
// This is rule-based on the signals actually available (no real logprobs) —
// honest, not fabricated; applyOCRAdjustment layers real OCR agreement on top.
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
	return clampConfidence(conf)
}

func clampConfidence(conf float64) float64 {
	if conf < 0.05 {
		return 0.05
	}
	if conf > 0.99 {
		return 0.99
	}
	return math.Round(conf*100) / 100 // 2 decimals: keeps 0.80+0.05 from JSON-encoding as 0.8500000000000001
}

// --- OCR corroboration (slice B, becky-AI-Agent-review-1.md §4 #5) ----------

// ocrMinConfidence matches becky-ocr's own defaultMinConfidence: below this,
// a line is too shaky to assert as ground truth into another tool's answer.
const ocrMinConfidence = 0.5

// ocrMaxKeyLines caps how many OCR lines get fed into the description/sources
// envelope — enough to carry a short dialog's worth of buttons/prompts
// without ballooning the JSON on a dense screenshot.
const ocrMaxKeyLines = 8

// ocrSignal is the (optional) OCR corroboration for one runEscalationPolicy
// call, pre-computed by gatherOCRSignal OUTSIDE the pure decision function —
// the same test seam fakeRungs uses for models. The zero value (ran=false)
// means OCR was never attempted and every method below is a safe no-op.
type ocrSignal struct {
	ran      bool     // true when OCR was actually attempted (promptImpliesTextOrUI was true)
	ok       bool     // true when OCR produced at least one high-confidence line
	keyLines []string // high-confidence lines, highest-confidence first
}

// ocrPromptWords are OCR'd UI strings that signal an ACTIVE interactive
// prompt is on screen (a confirmation dialog, a Y/N prompt, a "press any key"
// banner) — the review's exact incident. None of these literally say "stuck"
// or "waiting" the way descriptive VLM prose does (polarity()'s stuckWords
// would miss "Do you want to proceed?" entirely), so this is a DELIBERATELY
// separate keyword list tuned to raw on-screen text rather than description.
var ocrPromptWords = []string{
	"do you want", "are you sure", "proceed", "confirm", "cancel",
	"esc to", "press any key", "press enter", "y/n", "yes/no",
	"continue?", "waiting for", "please wait",
}

// ocrLooksLikePrompt reports whether any OCR key line reads like an active,
// awaiting-response interactive prompt.
func ocrLooksLikePrompt(lines []string) bool {
	joined := strings.ToLower(strings.Join(lines, " \n "))
	for _, w := range ocrPromptWords {
		if strings.Contains(joined, w) {
			return true
		}
	}
	return false
}

// disagreesWithReady reports whether OCR found a live on-screen prompt while
// text's own polarity claims "ready/idle/done" — the review's exact incident
// shape: a VLM confidently says nothing is stuck while the pixels are
// literally a live Y/N dialog. Only fires when OCR actually ran and found
// usable lines; a photo with no OCR text (or OCR that degraded) never
// overrides the VLM signal.
func (o ocrSignal) disagreesWithReady(text string) bool {
	if !o.ok || !ocrLooksLikePrompt(o.keyLines) {
		return false
	}
	stuck, ready := polarity(text)
	return ready && !stuck
}

// agrees is the heuristic OCR "Agrees" signal for the envelope: true unless
// OCR positively contradicts text (see disagreesWithReady). Like disagree()
// elsewhere in this file, "not disagreeing" is treated as agreement — a
// documented simplification (no ground truth is available), not a claim of
// certainty. OCR that never ran or found nothing usable defaults to true
// (no evidence against the answer).
func (o ocrSignal) agrees(text string) bool {
	return !o.disagreesWithReady(text)
}

// gatherOCRSignal runs becky-ocr's engine (internal/vision.RunOCR — the SAME
// engine becky-ocr.exe uses) on image ONCE, up front, but ONLY when
// promptImpliesTextOrUI(prompt) — the cheap, cost-free "does this call even
// care about on-screen text" gate that keeps a plain "describe this photo"
// call at rung-0 speed (becky-AI-Agent-review-1.md's own regression gate,
// acceptance criterion 9). A true image-content screenshot detector (the
// review's "or unconditional for screenshots" alternative) would need to
// spend model/CV time on EVERY call to decide, which defeats that gate; the
// prompt heuristic is the honest cheap signal available today. Every failure
// (missing OCR deps, engine crash, no frames) degrades to ocrSignal{ran:true,
// ok:false} — never a panic, never a failed becky-vision call.
func gatherOCRSignal(cfg config.Config, image, prompt string, verbose bool) ocrSignal {
	if !promptImpliesTextOrUI(prompt) {
		return ocrSignal{}
	}
	beckyio.Logf(verbose, "becky-vision ladder: prompt implies on-screen text - running becky-ocr corroboration")
	hr, err := vision.RunOCR(cfg, []string{image}, "ppocr", false, verbose)
	if err != nil || len(hr.Results) == 0 {
		return ocrSignal{ran: true, ok: false}
	}
	lines := highConfidenceLines(hr.Results[0].Lines, ocrMinConfidence, ocrMaxKeyLines)
	return ocrSignal{ran: true, ok: len(lines) > 0, keyLines: lines}
}

// highConfidenceLines returns the trimmed, non-empty line texts at or above
// minConf, capped to max entries. PROMPT-SHAPED lines sort first (then by
// confidence): verified on the review's own regression image 2026-07-10 —
// pure confidence ordering buried the 0.97 "Do you want to proceed?" line
// under eight 1.00-confidence UI-chrome words ("expand", "popout",
// "CONSOLE"...), which would starve ocrLooksLikePrompt/disagreesWithReady of
// the exact evidence the corroboration exists to carry.
func highConfidenceLines(lines []vision.OCRLine, minConf float64, max int) []string {
	type scored struct {
		text   string
		conf   float64
		prompt bool
	}
	var kept []scored
	for _, l := range lines {
		text := strings.TrimSpace(l.Text)
		if text == "" || l.Confidence < minConf {
			continue
		}
		kept = append(kept, scored{text, l.Confidence, ocrLooksLikePrompt([]string{text})})
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].prompt != kept[j].prompt {
			return kept[i].prompt
		}
		return kept[i].conf > kept[j].conf
	})
	if len(kept) > max {
		kept = kept[:max]
	}
	out := make([]string, len(kept))
	for i, k := range kept {
		out[i] = k.text
	}
	return out
}

// appendOCRText feeds the OCR key lines into the final answer verbatim — the
// review's core complaint was that the correct, literal on-screen text was
// available "one corroboration call away" and never surfaced to the caller.
// This makes it part of the answer instead of only living in res.Sources.
func appendOCRText(desc string, lines []string) string {
	quoted := make([]string, len(lines))
	for i, l := range lines {
		quoted[i] = `"` + l + `"`
	}
	desc = strings.TrimRight(strings.TrimSpace(desc), ". ")
	return desc + ". On-screen text (OCR): " + strings.Join(quoted, "; ") + "."
}

// applyOCRAdjustment nudges confidence when OCR corroboration ran: agreement
// with an independent, deterministic OCR reading is real evidence (+0.05);
// a live on-screen prompt contradicting a "ready" answer is the review's
// exact incident and should make the caller trust the answer LESS even
// though a model did answer (-0.15).
func applyOCRAdjustment(conf float64, ocr ocrSignal, text string) float64 {
	if !ocr.ok {
		return conf
	}
	switch {
	case ocr.disagreesWithReady(text):
		conf -= 0.15
	case ocrLooksLikePrompt(ocr.keyLines):
		conf += 0.05
	}
	return clampConfidence(conf)
}
