// Package vision is becky's thin, deterministic wrapper around a LOCAL
// LFM2.5-VL GGUF vision-language model run via llama.cpp's `llama-mtmd-cli`.
// It describes an image (frame triage / scene caption) or extracts text from
// it — image-only (Gemma-4 stays for AUDIO; see SPEC-BECKY-VISION-MODELS.md).
//
// Everything stays becky-shaped:
//   - Offline: the only "AI in the loop" is one explicit local .exe call.
//   - Deterministic: temperature 0 → same image+prompt → same description.
//   - Degrade, never crash: a missing binary / model / mmproj / image becomes a
//     typed Result{Degraded:true, Error:...} with a plain-language message — never
//     a panic, never half a result.
//
// The heavy compute lives in llama.cpp; Go only assembles the argv and captures
// stdout. The exec is hidden behind the small `runner` interface so unit tests
// exercise argument construction, model discovery, JSON shaping, and every
// degrade path WITHOUT a GPU, a model, or the binary present.
package vision

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"becky-go/internal/pathx"
)

// ToolName is the stable identifier emitted in every Result.
const ToolName = "becky-vision"

// Defaults that callers can override via flags/env. These are SELF-CONTAINED
// here on purpose — this package does not touch internal/config.
const (
	// DefaultBin is the verified-good llama.cpp multimodal CLI on Jordan's PC.
	DefaultBin = `C:/llama.cpp/build/bin/llama-mtmd-cli.exe`
	// DefaultModelDir is where the 450M LFM2.5-VL GGUF + mmproj live by default.
	DefaultModelDir = `X:/AI-2/becky-tools/models/lfm2.5-vl-450m/`
	// Dir16B is the escalation ladder's second rung: a stronger, still-fast
	// LFM2.5-VL-1.6B model tried when the 450M's answer looks uncertain or the
	// prompt implies reading on-screen text/UI state (see cmd/vision/ladder.go).
	Dir16B = `X:/AI-2/becky-tools/models/lfm2.5-vl-1.6b/`
	// DefaultPrompt is a concise, deterministic "describe this image" instruction.
	DefaultPrompt = "Describe this image concisely and factually."
	// DefaultNGL offloads all layers to the GPU (the tiny VLM fits with headroom).
	DefaultNGL = 99
)

// Env var names used as fallbacks when a flag is not provided.
const (
	EnvBin    = "BECKY_LLAMA_BIN"
	EnvModel  = "BECKY_LFM2VL_MODEL"
	EnvMMProj = "BECKY_LFM2VL_MMPROJ"
	EnvDir    = "BECKY_LFM2VL_DIR"
)

// Options configures one Describe call. Empty string / zero values mean "use the
// package default / discover it".
type Options struct {
	Image    string // REQUIRED: path to the image to describe / extract from
	Model    string // main GGUF (discovered in ModelDir when empty)
	MMProj   string // multimodal projector GGUF (discovered in ModelDir when empty)
	Bin      string // path to llama-mtmd-cli.exe (DefaultBin when empty)
	Prompt   string // the instruction (DefaultPrompt when empty)
	ModelDir string // where to discover Model/MMProj (DefaultModelDir when empty)
	NGL      int    // GPU layers to offload (DefaultNGL when <= 0)
}

// Result is the JSON-shaped outcome of one Describe call. On any recoverable
// failure Degraded is true and Error carries a plain-language reason; the tool
// still exits 0 so a pipeline never breaks on a missing model.
//
// Confidence/Escalations/Sources/Validated are the becky-AI-Agent-review-1.md
// escalation envelope (§4 acceptance criteria 3-5): populated by the
// escalation ladder in cmd/vision/ladder.go, the DEFAULT (no-flags) path. A
// caller that forces a single model via --gemma/--qwen still gets these
// fields (one Source, zero Escalations, a baseline Confidence, Validated
// false — a single unconfirmed opinion) so the envelope shape never has
// surprise gaps.
type Result struct {
	Tool        string   `json:"tool"`
	Image       string   `json:"image"`
	Model       string   `json:"model"`
	Engine      string   `json:"engine,omitempty"` // model family that produced this (default LFM2.5-VL; "Gemma-4" via --gemma)
	Prompt      string   `json:"prompt"`
	Description string   `json:"description"`
	Confidence  float64  `json:"confidence,omitempty"`  // 0.05-0.99 heuristic; higher rung + no hedging text = higher
	Escalations int      `json:"escalations,omitempty"` // rungs run BEYOND the first (450M); 0 = 450M answered confidently alone
	Validated   bool     `json:"validated"`             // true only when an INDEPENDENT source (OCR, or a second rung) cross-checked the answer and didn't disagree — never true for a single unconfirmed opinion (the review's F3 incident: "degraded:false" asserted over an unvalidated guess)
	Sources     []Source `json:"sources,omitempty"`     // every rung/corroboration source actually invoked, in order
	Degraded    bool     `json:"degraded"`
	Error       string   `json:"error,omitempty"`
}

// Source records one rung of the escalation ladder, or a corroboration pass
// (OCR), that was actually invoked. Kind is "vlm" for a model rung or "ocr"
// for the becky-ocr corroboration step (slice B, becky-AI-Agent-review-1.md
// §4 acceptance criterion 5). Agrees is a heuristic best-effort signal
// (polarity-based text comparison, not ground truth) — see ladder.go's
// disagree()/ocrDisagreesWithReady() for exactly what it checks. KeyLines is
// populated for "ocr" sources only: the high-confidence lines that were fed
// into the final answer and the agreement check.
type Source struct {
	Kind     string   `json:"kind"`
	Model    string   `json:"model"`
	OK       bool     `json:"ok"`     // true when this source produced usable output (non-degraded)
	Agrees   bool     `json:"agrees"` // heuristic: does this source's read match the FINAL answer's polarity
	KeyLines []string `json:"key_lines,omitempty"`
}

// runner abstracts the one external command so tests never spawn the model.
type runner interface {
	run(bin string, args []string) (stdout string, err error)
}

// execRunner is the production runner: it invokes llama-mtmd-cli and returns its
// stdout. Stderr (llama.cpp's progress chatter) is captured separately so it is
// only surfaced on failure, never mixed into the description.
type execRunner struct{}

func (execRunner) run(bin string, args []string) (string, error) {
	cmd := exec.Command(bin, args...)
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%w: %s", err, tail(errBuf.String()))
	}
	return out.String(), nil
}

// Describe runs one image→text inference and returns a Result. It never returns
// an error or panics — every failure is folded into a degraded Result so the
// caller can emit valid JSON and exit 0. It uses the production execRunner.
func Describe(opts Options) Result {
	return describeWith(execRunner{}, opts)
}

// describeWith is Describe with an injectable runner (the test seam).
func describeWith(r runner, opts Options) Result {
	opts = withDefaults(opts)
	res := Result{
		Tool:   ToolName,
		Image:  opts.Image,
		Model:  opts.Model,
		Prompt: opts.Prompt,
	}

	model, mmproj, err := resolveModels(opts)
	if err != nil {
		return degrade(res, err)
	}
	res.Model = model

	if err := checkInputs(opts.Bin, model, mmproj, opts.Image); err != nil {
		return degrade(res, err)
	}

	stdout, err := r.run(opts.Bin, BuildArgs(opts.Bin, model, mmproj, opts))
	if err != nil {
		return degrade(res, fmt.Errorf("llama-mtmd-cli failed: %w", err))
	}
	desc := strings.TrimSpace(stdout)
	if desc == "" {
		return degrade(res, fmt.Errorf("model returned empty output"))
	}
	res.Description = desc
	return res
}

// withDefaults fills unset Options from package defaults + env fallbacks. Flags
// (already in opts) win; then env; then the hardcoded default.
func withDefaults(o Options) Options {
	o.Bin = firstNonEmpty(o.Bin, os.Getenv(EnvBin), DefaultBin)
	o.ModelDir = firstNonEmpty(o.ModelDir, os.Getenv(EnvDir), DefaultModelDir)
	o.Model = firstNonEmpty(o.Model, os.Getenv(EnvModel))
	o.MMProj = firstNonEmpty(o.MMProj, os.Getenv(EnvMMProj))
	o.Prompt = firstNonEmpty(o.Prompt, DefaultPrompt)
	if o.NGL <= 0 {
		o.NGL = DefaultNGL
	}
	return o
}

// resolveModels returns the (model, mmproj) pair: explicit values win; otherwise
// they are discovered in ModelDir. A missing pair is a degrade reason, not a crash.
func resolveModels(o Options) (model, mmproj string, err error) {
	model, mmproj = o.Model, o.MMProj
	if model != "" && mmproj != "" {
		return model, mmproj, nil
	}
	dm, dmm, derr := DiscoverModels(o.ModelDir)
	if derr != nil {
		return "", "", derr
	}
	if model == "" {
		model = dm
	}
	if mmproj == "" {
		mmproj = dmm
	}
	return model, mmproj, nil
}

// DiscoverModels finds the main model GGUF and the mmproj GGUF in dir. The mmproj
// filename contains "mmproj"; the main model is a *.gguf that is NOT the mmproj
// (preferring one whose name contains "Q8_0" per the known-good 450M layout).
func DiscoverModels(dir string) (model, mmproj string, err error) {
	if dir == "" {
		return "", "", fmt.Errorf("no model directory to search (set --model/--mmproj or --dir/%s)", EnvDir)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.gguf"))
	var mains []string
	for _, m := range matches {
		if isMMProj(m) {
			if mmproj == "" {
				mmproj = m
			}
			continue
		}
		mains = append(mains, m)
	}
	model = pickMain(mains)
	if model == "" {
		return "", "", fmt.Errorf("no model GGUF found in %s (need a non-mmproj *.gguf)", dir)
	}
	if mmproj == "" {
		return "", "", fmt.Errorf("no mmproj GGUF found in %s (need a *mmproj*.gguf)", dir)
	}
	return model, mmproj, nil
}

// pickMain chooses the main model from candidate GGUFs: prefer a Q8_0 build (the
// known-good 450M quant), else the first candidate for deterministic selection.
func pickMain(candidates []string) string {
	for _, c := range candidates {
		if strings.Contains(strings.ToLower(pathx.Base(c)), "q8_0") {
			return c
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return ""
}

// isMMProj reports whether a GGUF filename is the multimodal projector.
func isMMProj(path string) bool {
	return strings.Contains(strings.ToLower(pathx.Base(path)), "mmproj")
}

// BuildArgs assembles the llama-mtmd-cli argv for one deterministic, image-only
// inference. Mirrors the verified-good invocation: -m / --mmproj / --image /
// -ngl N / --temp 0 / -p "<prompt>".
func BuildArgs(_ string, model, mmproj string, o Options) []string {
	return []string{
		"-m", model,
		"--mmproj", mmproj,
		"--image", o.Image,
		"-ngl", itoa(o.NGL),
		"--temp", "0",
		"-p", o.Prompt,
	}
}

// checkInputs verifies the binary, model, mmproj, and image all exist on disk so
// a missing piece becomes a precise degrade note instead of an exec failure.
func checkInputs(bin, model, mmproj, image string) error {
	if image == "" {
		return fmt.Errorf("no --image given (an image path is required)")
	}
	checks := []struct{ path, what string }{
		{bin, "llama-mtmd-cli binary"},
		{model, "model GGUF"},
		{mmproj, "mmproj GGUF"},
		{image, "image"},
	}
	for _, c := range checks {
		if _, err := os.Stat(c.path); err != nil {
			return fmt.Errorf("%s not found at %s", c.what, c.path)
		}
	}
	return nil
}

// degrade folds an error into a degraded Result (never returns the error).
func degrade(res Result, err error) Result {
	res.Degraded = true
	res.Error = err.Error()
	res.Description = ""
	return res
}

// Provenance is the one-line "which model produced this" string for the
// plain-language report. Engine defaults to LFM2.5-VL (the fast still-image
// path) and is "Gemma-4" when the stronger --gemma model produced the answer.
func (r Result) Provenance() string {
	engine := r.Engine
	if engine == "" {
		engine = "LFM2.5-VL"
	}
	return fmt.Sprintf("(produced by %s via local %s: %s)", ToolName, engine, pathx.Base(r.Model))
}

// firstNonEmpty returns the first non-empty string, or "" if all are empty.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func itoa(i int) string { return fmt.Sprintf("%d", i) }

// tail trims llama.cpp's verbose stderr to its last 500 chars for error context.
func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 500 {
		return s[len(s)-500:]
	}
	return s
}
