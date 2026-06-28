// Package imagegen is becky's thin, deterministic wrapper around LOCAL text→image
// generation with the FLUX.1 "Krea-2" model, run via stable-diffusion.cpp's
// `sd-cli` binary. It turns a text prompt into a PNG on disk.
//
// Krea-2 has three pieces, all local GGUF/safetensors (docs/krea2.md in
// leejet/stable-diffusion.cpp): a Krea-2 diffusion transformer (--diffusion-model),
// the Wan 2.1 VAE (--vae), and Qwen3-VL-4B as the text encoder (--llm). This
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
	// DefaultSDCli is stable-diffusion.cpp's CLI binary on Jordan's PC.
	DefaultSDCli = `C:/stable-diffusion.cpp/build/bin/Release/sd-cli.exe`
	// DefaultModelDir is where the Krea-2 GGUFs + VAE + text encoder live.
	DefaultModelDir = `X:/AI-2/becky-tools/models/krea2/`
	// DefaultOut is where the generated PNG is written when no --out is given.
	DefaultOut = "becky-image.png"

	// DefaultWidth / DefaultHeight are Krea-2's native 1-megapixel square.
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

	SDCli string // path to sd-cli (DefaultSDCli when empty)
	Model string // diffusion-transformer GGUF (REQUIRED; no safe default to invent)
	VAE   string // Wan 2.1 VAE safetensors (REQUIRED)
	LLM   string // Qwen3-VL-4B text-encoder GGUF (REQUIRED)

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
	Variant  string   `json:"variant"` // krea-2-raw | krea-2-turbo
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
	if o.Width <= 0 {
		o.Width = DefaultWidth
	}
	if o.Height <= 0 {
		o.Height = DefaultHeight
	}
	if o.Steps <= 0 {
		if o.Turbo {
			o.Steps = DefaultStepsTurbo
		} else {
			o.Steps = DefaultStepsRaw
		}
	}
	if o.CFGScale <= 0 {
		o.CFGScale = DefaultCFGScale
	}
	if o.Guidance <= 0 {
		o.Guidance = DefaultGuidance
	}
	if !o.seedSet {
		o.Seed = DefaultSeed
	}
	return o
}

// BuildArgs assembles the sd-cli argv for one deterministic text→image run. It
// mirrors the verified-good Krea-2 invocation from docs/krea2.md
// (--diffusion-model / --llm / --vae / -p ...) and adds the standard
// output/seed/size/steps/sampler flags. Order is fixed for stable, testable
// argv and reproducible provenance.
func BuildArgs(o Options) []string {
	args := []string{
		"--diffusion-model", o.Model,
		"--llm", o.LLM,
		"--vae", o.VAE,
		"-p", o.Prompt,
	}
	if o.Negative != "" {
		args = append(args, "-n", o.Negative)
	}
	args = append(args,
		"--cfg-scale", trimFloat(o.CFGScale),
		"--guidance", trimFloat(o.Guidance),
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
	if o.OffloadCPU {
		args = append(args, "--offload-to-cpu")
	}
	if o.Verbose {
		args = append(args, "-v")
	}
	return args
}

// checkInputs verifies the binary + the three Krea-2 model files exist (and a
// prompt was given) so a missing piece becomes a precise degrade note instead of
// an exec failure. The output is NOT checked here (it does not exist yet).
func checkInputs(o Options) error {
	if strings.TrimSpace(o.Prompt) == "" {
		return fmt.Errorf("no --prompt given (a text prompt is required)")
	}
	checks := []struct{ path, what string }{
		{o.SDCli, "sd-cli binary"},
		{o.Model, "Krea-2 diffusion model GGUF"},
		{o.VAE, "Wan 2.1 VAE"},
		{o.LLM, "Qwen3-VL-4B text encoder GGUF"},
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

// variantLabel reports which Krea-2 variant an Options resolves to, for the
// Result and the plain-language report.
func variantLabel(o Options) string {
	if o.Turbo || strings.Contains(strings.ToLower(pathx.Base(o.Model)), "turbo") {
		return "krea-2-turbo"
	}
	return "krea-2-raw"
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
