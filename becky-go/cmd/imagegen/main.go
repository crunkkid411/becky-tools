// becky-imagegen — becky's DEFAULT local image generator AND editor. It renders a
// PNG from a text prompt, or edits reference images (--ref) to match the prompt,
// with Qwen-Image-2.1, run entirely on-device via stable-diffusion.cpp's sd-cli
// (docs/qwen_image_2.1.md). Qwen-Image-2.1 = its diffusion GGUF + its own VAE +
// Qwen3-VL-8B as the text encoder (+ that encoder's vision file for edits).
// --krea switches to the older FLUX.1 "Krea-2" model (Wan 2.1 VAE + Qwen3-VL-4B).
//
//	becky-imagegen --prompt "a lovely cat holding a sign that says 'becky'"
//	becky-imagegen --ref me.jpg --prompt "put me on a sunset beach" --out beach.png
//	becky-imagegen --prompt "..." --out art.png --seed 7
//	becky-imagegen --selftest        # offline, no-hardware proof of the argv
//	becky-imagegen --dry-run -p ...  # print the sd-cli command, don't run it
//
// becky-shaped: OFFLINE (one explicit local .exe call), DETERMINISTIC (fixed
// default seed → same prompt+params → same image), DEGRADE-NEVER-CRASH (a missing
// binary/model or a run that yields no file prints a plain note and exits 0 with
// degraded:true). Model paths come from ~/.becky/config.json (config.ImageGen),
// never hardcoded; flags/env override.
//
// Exit codes: 0 = ran (incl. a clean degrade or a passing selftest), 1 =
// unexpected error / selftest failure, 2 = usage.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"becky-go/internal/config"
	"becky-go/internal/imagegen"
)

// refList collects a repeatable --ref flag, in order.
type refList []string

func (r *refList) String() string     { return strings.Join(*r, ",") }
func (r *refList) Set(v string) error { *r = append(*r, v); return nil }

func main() {
	prompt := flag.String("prompt", "", "REQUIRED (unless --selftest): text prompt to render, or the edit to make")
	flag.StringVar(prompt, "p", "", "alias for --prompt")
	negative := flag.String("negative", "", "optional negative prompt")
	out := flag.String("out", "", "output PNG path (default: "+imagegen.DefaultOut+")")
	var refs refList
	flag.Var(&refs, "ref", "reference image to EDIT (repeat for several, in order); omit to draw from scratch")
	flag.Var(&refs, "r", "alias for --ref")

	sdcli := flag.String("sd-cli", "", "path to sd-cli (default: from config / "+imagegen.DefaultSDCli+")")
	model := flag.String("model", "", "diffusion GGUF (default: Qwen-Image-2.1 from config)")
	vae := flag.String("vae", "", "the model's VAE safetensors (default: from config)")
	llm := flag.String("llm", "", "text-encoder GGUF (default: from config)")
	vision := flag.String("vision", "", "text encoder's vision file, used for --ref edits (default: from config)")

	krea := flag.Bool("krea", false, "use the older Krea-2 model instead of Qwen-Image-2.1")
	turbo := flag.Bool("turbo", false, "with --krea: use the Krea-2 Turbo variant (fewer steps, faster)")
	width := flag.Int("width", 0, "image width (default "+strconv.Itoa(imagegen.DefaultWidth)+")")
	flag.IntVar(width, "W", 0, "alias for --width")
	height := flag.Int("height", 0, "image height (default "+strconv.Itoa(imagegen.DefaultHeight)+")")
	flag.IntVar(height, "H", 0, "alias for --height")
	steps := flag.Int("steps", 0, "sampling steps (default 40 Qwen-Image / 28 Krea raw / 8 Krea turbo)")
	cfg := flag.Float64("cfg-scale", 0, "classifier-free guidance scale (default 6 Qwen-Image / 1 Krea)")
	guidance := flag.Float64("guidance", 0, "Krea-2 distilled guidance (default 4.5; Qwen-Image ignores it)")
	sampler := flag.String("sampling-method", "", "sampler (default "+imagegen.DefaultSampler+")")
	seed := flag.Int64("seed", imagegen.SeedUnset, "RNG seed; -1 = random (default "+strconv.FormatInt(imagegen.DefaultSeed, 10)+", fixed for reproducibility)")
	threads := flag.Int("threads", 0, "CPU threads (-t); 0 = sd-cli default")
	noFA := flag.Bool("no-flash-attn", false, "disable --diffusion-fa")
	offload := flag.Bool("offload-to-cpu", true, "offload weights to CPU to fit limited VRAM")
	verbose := flag.Bool("verbose", false, "show sd-cli progress on stderr")

	asJSON := flag.Bool("json", false, "emit JSON instead of a plain-language report")
	dryRun := flag.Bool("dry-run", false, "print the sd-cli command without running it")
	selftest := flag.Bool("selftest", false, "run the offline, no-hardware argv proof and exit")
	flag.Parse()

	if *selftest {
		os.Exit(runSelftest())
	}

	if strings.TrimSpace(*prompt) == "" {
		fmt.Fprintln(os.Stderr, "usage: becky-imagegen --prompt \"...\" [--ref photo.jpg] [--out art.png] [--seed N] [--krea] [--dry-run|--selftest]")
		os.Exit(2)
	}

	// Config drives every path (the invariant: tools never hardcode model paths).
	// A flag, then env (handled inside the package), then config, then the package
	// default — in that precedence.
	c := config.Load()
	cfgSDCli, cfgModel, cfgVAE, cfgLLM, cfgVision, _ := c.ImageGen(*krea, *turbo)

	opts := imagegen.Options{
		Prompt:     *prompt,
		Negative:   *negative,
		Out:        *out,
		SDCli:      firstNonEmpty(*sdcli, cfgSDCli),
		Model:      firstNonEmpty(*model, cfgModel),
		VAE:        firstNonEmpty(*vae, cfgVAE),
		LLM:        firstNonEmpty(*llm, cfgLLM),
		Vision:     firstNonEmpty(*vision, cfgVision),
		Refs:       refs,
		Turbo:      *turbo,
		Width:      *width,
		Height:     *height,
		Steps:      *steps,
		CFGScale:   *cfg,
		Guidance:   *guidance,
		Sampler:    *sampler,
		Threads:    *threads,
		FlashAttn:  !*noFA,
		OffloadCPU: *offload,
		Verbose:    *verbose,
	}
	if *seed != imagegen.SeedUnset {
		opts = opts.WithSeed(*seed)
	}

	if *dryRun {
		res := imagegen.Plan(opts)
		emit(res, *asJSON)
		return
	}

	res := imagegen.Generate(opts)
	emit(res, *asJSON)
}

// emit prints a Result as JSON or a plain-language report.
func emit(res imagegen.Result, asJSON bool) {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			fmt.Fprintln(os.Stderr, "encode:", err)
			os.Exit(1)
		}
		return
	}
	printReport(res)
}

func printReport(res imagegen.Result) {
	if res.Degraded {
		fmt.Println("becky-imagegen could not make the image.")
		fmt.Println("  reason:", res.Error)
		fmt.Println("  (this is a graceful degrade — the sd-cli binary, a model file or a reference image was missing, not a crash.)")
		return
	}
	if len(res.Refs) > 0 {
		fmt.Printf("Edited %d reference image(s) into a %dx%d image: %s\n", len(res.Refs), res.Width, res.Height, res.Output)
	} else {
		fmt.Printf("Generated %dx%d image: %s\n", res.Width, res.Height, res.Output)
	}
	fmt.Println(res.Provenance())
}

// runSelftest is the one-command, OFFLINE, no-hardware proof of the real code
// path: it builds the exact sd-cli argv with synthetic paths and asserts the
// Qwen-Image-2.1 invocation is correct (the three model pieces, a deterministic
// seed, the prompt, its steps/CFG, the edit flags) and that --krea's Krea-2 path
// still resolves. It needs no GPU, no binary, and no model — it exercises Plan/
// BuildArgs, the same code Generate uses. Prints PASS/FAIL and returns the exit
// code. This is becky's "provable handoff" gate for cloud→local.
func runSelftest() int {
	res := imagegen.Plan(imagegen.Options{
		Prompt: "a lovely cat holding a sign that says 'becky'",
		Model:  "qwen-image-2.1-Q5_K_S.gguf",
		VAE:    "qwen_image_2.1_vae_bf16.safetensors",
		LLM:    "Qwen3-VL-8B-Instruct-UD-Q4_K_XL.gguf",
		Vision: "mmproj-F16.gguf",
		Out:    "selftest.png",
	})
	edit := imagegen.Plan(imagegen.Options{
		Prompt: "put me on a sunset beach",
		Model:  "qwen-image-2.1-Q5_K_S.gguf",
		VAE:    "qwen_image_2.1_vae_bf16.safetensors",
		LLM:    "Qwen3-VL-8B-Instruct-UD-Q4_K_XL.gguf",
		Vision: "mmproj-F16.gguf",
		Refs:   []string{"me.jpg"},
		Out:    "edit.png",
	})
	krea := imagegen.Plan(imagegen.Options{
		Prompt: "x", Model: "Krea-2-Turbo-Q4_K_M.gguf", VAE: "wan_2.1_vae.safetensors",
		LLM: "Qwen3VL-4B-Instruct-Q8_0.gguf", Out: "krea.png",
	})

	type check struct {
		name string
		ok   bool
	}
	checks := []check{
		{"diffusion-model is the Qwen-Image-2.1 GGUF", argVal(res.Args, "--diffusion-model") == "qwen-image-2.1-Q5_K_S.gguf"},
		{"vae is the Qwen-Image-2.1 VAE", argVal(res.Args, "--vae") == "qwen_image_2.1_vae_bf16.safetensors"},
		{"llm is the Qwen3-VL-8B text encoder", argVal(res.Args, "--llm") == "Qwen3-VL-8B-Instruct-UD-Q4_K_XL.gguf"},
		{"prompt is passed via -p", argVal(res.Args, "-p") == "a lovely cat holding a sign that says 'becky'"},
		{"output is passed via -o", argVal(res.Args, "-o") == "selftest.png"},
		{"seed is the fixed deterministic default", argVal(res.Args, "--seed") == strconv.FormatInt(imagegen.DefaultSeed, 10)},
		{"steps default to Qwen's 40", argVal(res.Args, "--steps") == strconv.Itoa(imagegen.DefaultStepsQwen)},
		{"cfg-scale defaults to 6 (real guidance)", argVal(res.Args, "--cfg-scale") == "6"},
		{"no FLUX --guidance for Qwen-Image", argVal(res.Args, "--guidance") == ""},
		{"no vision file / -r when not editing", argVal(res.Args, "--llm_vision") == "" && argVal(res.Args, "-r") == ""},
		{"text encoder runs on the CPU (--backend te=cpu)", argVal(res.Args, "--backend") == "te=cpu"},
		{"size defaults to 1024x1024", argVal(res.Args, "-W") == "1024" && argVal(res.Args, "-H") == "1024"},
		{"variant resolves to qwen-image-2.1", res.Variant == "qwen-image-2.1"},
		{"edit passes the reference via -r", argVal(edit.Args, "-r") == "me.jpg"},
		{"edit loads the vision file via --llm_vision", argVal(edit.Args, "--llm_vision") == "mmproj-F16.gguf"},
		{"--krea path: Krea-2 Turbo, 8 steps, --guidance kept", krea.Variant == "krea-2-turbo" && argVal(krea.Args, "--steps") == "8" && argVal(krea.Args, "--guidance") == "4.5"},
		{"not degraded (Plan is pure)", !res.Degraded && !edit.Degraded},
	}

	failed := 0
	for _, c := range checks {
		status := "PASS"
		if !c.ok {
			status = "FAIL"
			failed++
		}
		fmt.Printf("[%s] %s\n", status, c.name)
	}
	fmt.Println()
	fmt.Println("argv:", strings.Join(res.Args, " "))
	fmt.Println("edit argv:", strings.Join(edit.Args, " "))
	fmt.Println()
	if failed == 0 {
		fmt.Printf("becky-imagegen selftest: PASS (%d/%d checks)\n", len(checks), len(checks))
		return 0
	}
	fmt.Printf("becky-imagegen selftest: FAIL (%d/%d checks failed)\n", failed, len(checks))
	return 1
}

func argVal(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
