// Package imagegen is becky's thin, deterministic wrapper around LOCAL image
// generation and editing, run via stable-diffusion.cpp's `sd-cli` binary. It
// turns a text prompt (plus, for an edit, reference images) into a PNG on disk.
//
// The DEFAULT model is Qwen-Image-2.1 (Jordan, 2026-10-14; docs/
// qwen_image_2.1.md in leejet/stable-diffusion.cpp): the diffusion GGUF
// (--diffusion-model), its own Qwen-Image-2.1 VAE (--vae), Qwen3-VL-8B as the
// text encoder (--llm) and, for edits only, that model's vision file
// (--llm_vision) with each reference image passed as -r. It runs 40 steps (Qwen's
// own card) at CFG 6 (sd.cpp's docs). The older FLUX.1 "Krea-2" model (Wan 2.1 VAE
// + Qwen3-VL-4B, docs/krea2.md) still works when its files are selected. This
// package only assembles the argv and captures the result; the heavy compute
// lives in sd-cli.
//
// Everything stays becky-shaped:
//   - Offline: the only "AI in the loop" is one explicit local .exe call.
//   - Deterministic: a FIXED default seed → same prompt + params + model → same
//     image. (Same machine/backend; sd-cli is seed-deterministic.)
//   - Degrade, never crash: a missing binary / model / VAE / text encoder, or a
//     run that produces no output file, becomes a typed Result{Degraded:true,
//     Error:...} with a plain-language message — never a panic, never a half image.
//
// The exec is hidden behind the small `runner` interface so unit tests exercise
// argument construction, model defaulting, JSON shaping, and every degrade path
// WITHOUT a GPU, a model, or the sd-cli binary present. Plan() builds the exact
// argv with no side effects, which is what `becky-imagegen --selftest` asserts
// against as its offline, no-hardware proof.
package imagegen

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"becky-go/internal/pathx"
)

// ToolName is the stable identifier emitted in every Result.
const ToolName = "becky-imagegen"

// Defaults a caller can override via flags/env. Self-contained on purpose — the
// package does not import internal/config (the CLI passes config paths in).
const (
	// DefaultSDCli is stable-diffusion.cpp's CLI binary on Jordan's PC. The
	// master-964 release build: the older build/bin/Release one predates
	// Qwen-Image-2.1 support.
	DefaultSDCli = `C:/stable-diffusion.cpp/bin-master-964/sd-cli.exe`
	// DefaultOut is where the generated PNG is written when no --out is given.
	DefaultOut = "becky-image.png"

	// DefaultStepsQwen / DefaultCFGQwen: Qwen-Image-2.1's own card samples 40
	// steps; it needs real classifier-free guidance (sd.cpp's docs and unsloth's
	// card use 6.0), not FLUX's distilled --guidance.
	DefaultStepsQwen = 40
	DefaultCFGQwen   = 6.0

	// DefaultWidth / DefaultHeight: a 1-megapixel square (native for both models).
	DefaultWidth  = 1024
	DefaultHeight = 1024
	// DefaultStepsRaw / DefaultStepsTurbo: the Turbo variant is distilled for far
	// fewer steps. These are sane starting points; tune on real output.
	DefaultStepsRaw   = 28
	DefaultStepsTurbo = 8
	// DefaultCFGScale 1.0 disables classifier-free guidance (FLUX-class models use
	// distilled --guidance instead, which keeps generation single-pass/fast).
	DefaultCFGScale = 1.0
	DefaultGuidance = 4.5
	// DefaultSampler is sd-cli's stable Euler sampler.
	DefaultSampler = "euler"
	// DefaultSeed is FIXED so generation is reproducible by default (becky's
	// determinism invariant). Pass --seed -1 for a random image.
	DefaultSeed int64 = 42
)

// Env var fallbacks used when a flag is not provided.
const (
	EnvSDCli       = "BECKY_SDCLI_BIN"
	EnvModel       = "BECKY_KREA2_MODEL"
	EnvVAE         = "BECKY_KREA2_VAE"
	EnvTextEncoder = "BECKY_KREA2_LLM"
)

// Options configures one Generate call. Empty string / zero values mean "use the
// package default".
type Options struct {
	Prompt   string // REQUIRED: the text prompt to render
	Negative string // optional negative prompt (-n); omitted when empty
	Out      string // output PNG path (DefaultOut when empty)

	SDCli  string   // path to sd-cli (DefaultSDCli when empty)
	Model  string   // diffusion-transformer GGUF (REQUIRED; no safe default to invent)
	VAE    string   // the model's VAE safetensors (REQUIRED)
	LLM    string   // text-encoder GGUF (REQUIRED)
	Vision string   // text encoder's vision file (--llm_vision); Qwen-Image edits need it
	Refs   []string // reference images (-r, in order): set = edit these instead of drawing from scratch

	Width    int     // image width (DefaultWidth when <= 0)
	Height   int     // image height (DefaultHeight when <= 0)
	Steps    int     // sampling steps (variant default when <= 0)
	CFGScale float64 // classifier-free guidance scale (DefaultCFGScale when <= 0)
	Guidance float64 // distilled guidance (DefaultGuidance when <= 0)
	Sampler  string  // sampling method (DefaultSampler when empty)
	Seed     int64   // RNG seed; use SeedUnset to mean "fill the fixed default"
	Threads  int     // CPU threads (-t); omitted when <= 0

	Turbo      bool // select the lighter Turbo step default + label
	FlashAttn  bool // --diffusion-fa (recommended; on by default via the CLI)
	OffloadCPU bool // --offload-to-cpu (fits big models in limited VRAM)
	Verbose    bool // -v progress on stderr

	seedSet bool // internal: whether Seed was explicitly provided
}

// SeedUnset is the sentinel meaning "no explicit seed given — use DefaultSeed".
// Callers set it via WithSeed; a raw Options{} leaves Seed at 0 which is also a
// valid explicit seed, so the CLI uses WithSeed to disambiguate.
const SeedUnset int64 = -999999

// WithSeed records an explicit seed (including 0 or -1) on the Options.
func (o Options) WithSeed(seed int64) Options {
	o.Seed = seed
	o.seedSet = true
	return o
}

// Result is the JSON-shaped outcome of one Generate call. On any recoverable
// failure Degraded is true and Error carries a plain-language reason; the tool
// still exits 0 so a pipeline never breaks on a missing model.
type Result struct {
	Tool     string   `json:"tool"`
	Prompt   string   `json:"prompt"`
	Negative string   `json:"negative,omitempty"`
	Output   string   `json:"output"`
	Model    string   `json:"model"`
	Variant  string   `json:"variant"` // qwen-image-2.1 | krea-2-turbo | krea-2-raw
	Refs     []string `json:"refs,omitempty"`
	Seed     int64    `json:"seed"`
	Width    int      `json:"width"`
	Height   int      `json:"height"`
	Steps    int      `json:"steps"`
	Args     []string `json:"args"` // the exact argv passed to sd-cli (provenance)
	Degraded bool     `json:"degraded"`
	Error    string   `json:"error,omitempty"`
}

// runner abstracts the one external command so tests never spawn sd-cli.
type runner interface {
	run(bin string, args []string) (stdout string, err error)
}

// execRunner is the production runner: it invokes sd-cli and returns its stdout.
// Stderr (sd-cli's progress chatter) is only surfaced on failure.
type execRunner struct{}

func (execRunner) run(bin string, args []string) (string, error) {
	cmd := newCmd(bin, args)
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%w: %s", err, tail(errBuf.String()))
	}
	return out.String(), nil
}

// Generate runs one text→image inference and returns a Result. It never returns
// an error or panics — every failure is folded into a degraded Result so the
// caller can emit valid JSON and exit 0. Uses the production execRunner.
func Generate(opts Options) Result {
	return generateWith(execRunner{}, opts)
}

// generateWith is Generate with an injectable runner (the test seam).
func generateWith(r runner, opts Options) Result {
	opts = withDefaults(opts)
	res := newResult(opts)

	if err := checkInputs(opts); err != nil {
		return degrade(res, err)
	}

	if _, err := r.run(opts.SDCli, res.Args); err != nil {
		return degrade(res, fmt.Errorf("sd-cli failed: %w", err))
	}
	if _, err := os.Stat(opts.Out); err != nil {
		return degrade(res, fmt.Errorf("sd-cli ran but produced no image at %s", opts.Out))
	}
	return res
}

// Plan resolves defaults and builds the exact argv for one generation WITHOUT
// running anything or touching the filesystem. It is the basis of --selftest:
// a no-hardware, deterministic proof that the right Krea-2 invocation is built.
func Plan(opts Options) Result {
	return newResult(withDefaults(opts))
}

// newResult fills a Result (incl. the argv) from already-defaulted Options.
func newResult(o Options) Result {
	return Result{
		Tool:     ToolName,
		Prompt:   o.Prompt,
		Negative: o.Negative,
		Output:   o.Out,
		Model:    o.Model,
		Variant:  variantLabel(o),
		Refs:     o.Refs,
		Seed:     o.Seed,
		Width:    o.Width,
		Height:   o.Height,
		Steps:    o.Steps,
		Args:     BuildArgs(o),
	}
}

// withDefaults fills unset Options from package defaults + env fallbacks. Flags
// (already in opts) win; then env; then the hardcoded default.
func withDefaults(o Options) Options {
	o.SDCli = firstNonEmpty(o.SDCli, os.Getenv(EnvSDCli), DefaultSDCli)
	o.Model = firstNonEmpty(o.Model, os.Getenv(EnvModel))
	o.VAE = firstNonEmpty(o.VAE, os.Getenv(EnvVAE))
	o.LLM = firstNonEmpty(o.LLM, os.Getenv(EnvTextEncoder))
	o.Out = firstNonEmpty(o.Out, DefaultOut)
	o.Sampler = firstNonEmpty(o.Sampler, DefaultSampler)
	// A Turbo model file implies the Turbo step profile even without --turbo, so
	// becky's default (Krea-2 Turbo) generates at its distilled ~8 steps instead of
	// Raw's 28. (krea's card marks Raw/Base "not for inference"; Turbo is the
	// release model.) An explicit --steps still wins via the o.Steps<=0 guard below.
	if !o.Turbo && strings.Contains(strings.ToLower(pathx.Base(o.Model)), "turbo") {
		o.Turbo = true
	}
	if o.Width <= 0 {
		o.Width = DefaultWidth
	}
	if o.Height <= 0 {
		o.Height = DefaultHeight
	}
	qwen := isQwenImage(o)
	if o.Steps <= 0 {
		switch {
		case qwen:
			o.Steps = DefaultStepsQwen
		case o.Turbo:
			o.Steps = DefaultStepsTurbo
		default:
			o.Steps = DefaultStepsRaw
		}
	}
	if o.CFGScale <= 0 {
		o.CFGScale = DefaultCFGScale
		if qwen {
			o.CFGScale = DefaultCFGQwen
		}
	}
	if o.Guidance <= 0 {
		o.Guidance = DefaultGuidance
	}
	if !o.seedSet {
		o.Seed = DefaultSeed
	}
	return o
}

// BuildArgs assembles the sd-cli argv for one deterministic run. It mirrors the
// documented invocations (docs/qwen_image_2.1.md, docs/krea2.md:
// --diffusion-model / --llm / --vae / -p ...) and adds the standard
// output/seed/size/steps/sampler flags. Qwen-Image takes no distilled
// --guidance; an edit adds --llm_vision and one -r per reference image. Order
// is fixed for stable, testable argv and reproducible provenance.
func BuildArgs(o Options) []string {
	args := []string{
		"--diffusion-model", o.Model,
		"--llm", o.LLM,
		"--vae", o.VAE,
	}
	if len(o.Refs) > 0 && o.Vision != "" {
		args = append(args, "--llm_vision", o.Vision)
	}
	for _, r := range o.Refs {
		args = append(args, "-r", r)
	}
	args = append(args, "-p", o.Prompt)
	if o.Negative != "" {
		args = append(args, "-n", o.Negative)
	}
	args = append(args, "--cfg-scale", trimFloat(o.CFGScale))
	if !isQwenImage(o) {
		args = append(args, "--guidance", trimFloat(o.Guidance))
	}
	args = append(args,
		"--sampling-method", o.Sampler,
		"--steps", strconv.Itoa(o.Steps),
		"-W", strconv.Itoa(o.Width),
		"-H", strconv.Itoa(o.Height),
		"--seed", strconv.FormatInt(o.Seed, 10),
	)
	if o.Threads > 0 {
		args = append(args, "-t", strconv.Itoa(o.Threads))
	}
	args = append(args, "-o", o.Out)
	if o.FlashAttn {
		args = append(args, "--diffusion-fa")
	}
	if isQwenImage(o) {
		// Qwen's 5 GB text encoder never fits on the 8 GB card next to the
		// diffusion model (sd-cli fails "workspace capacity check"); on the CPU it
		// reads a prompt in ~10 s (measured 2026-10-15). sd.cpp's auto-fit places
		// the rest, so --offload-to-cpu is not passed.
		args = append(args, "--backend", "te=cpu", "--mmap")
	} else if o.OffloadCPU {
		args = append(args, "--offload-to-cpu")
	}
	if o.Verbose {
		args = append(args, "-v")
	}
	return args
}

// checkInputs verifies the binary + the model files (+ any reference images)
// exist and a prompt was given, so a missing piece becomes a precise degrade
// note instead of an exec failure. The output is NOT checked here (it does not
// exist yet).
func checkInputs(o Options) error {
	if strings.TrimSpace(o.Prompt) == "" {
		return fmt.Errorf("no --prompt given (a text prompt is required)")
	}
	checks := []struct{ path, what string }{
		{o.SDCli, "sd-cli binary"},
		{o.Model, "diffusion model GGUF"},
		{o.VAE, "VAE"},
		{o.LLM, "text encoder GGUF"},
	}
	if len(o.Refs) > 0 && isQwenImage(o) {
		checks = append(checks, struct{ path, what string }{o.Vision, "text encoder vision file (mmproj, needed to edit an image)"})
	}
	for _, r := range o.Refs {
		checks = append(checks, struct{ path, what string }{r, "reference image"})
	}
	for _, c := range checks {
		if strings.TrimSpace(c.path) == "" {
			return fmt.Errorf("%s path not set (configure it in ~/.becky/config.json or pass the flag)", c.what)
		}
		if _, err := os.Stat(c.path); err != nil {
			return fmt.Errorf("%s not found at %s", c.what, c.path)
		}
	}
	return nil
}

// variantLabel reports which model variant an Options resolves to, for the
// Result and the plain-language report.
func variantLabel(o Options) string {
	switch {
	case isQwenImage(o):
		return "qwen-image-2.1"
	case o.Turbo || strings.Contains(strings.ToLower(pathx.Base(o.Model)), "turbo"):
		return "krea-2-turbo"
	}
	return "krea-2-raw"
}

// isQwenImage reports whether the diffusion model file is a Qwen-Image one
// (unsloth names it qwen-image-*, leejet/Comfy-Org qwen_image_*).
func isQwenImage(o Options) bool {
	b := strings.ToLower(pathx.Base(o.Model))
	return strings.Contains(b, "qwen-image") || strings.Contains(b, "qwen_image")
}

// degrade folds an error into a degraded Result (never returns the error).
func degrade(res Result, err error) Result {
	res.Degraded = true
	res.Error = err.Error()
	return res
}

// Provenance is the one-line "what produced this" string for the plain-language
// report.
func (r Result) Provenance() string {
	return fmt.Sprintf("(produced by %s via local stable-diffusion.cpp %s: %s, seed %d)",
		ToolName, r.Variant, pathx.Base(r.Model), r.Seed)
}

// trimFloat formats a float without a trailing ".0" noise (1.0 → "1", 4.5 → "4.5").
func trimFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// tail trims sd-cli's verbose stderr to its last 500 chars for error context.
func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 500 {
		return s[len(s)-500:]
	}
	return s
}
