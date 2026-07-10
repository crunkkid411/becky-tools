// becky-vision — describe / extract text from an image with a LOCAL LFM2.5-VL
// GGUF vision-language model, run via llama.cpp's llama-mtmd-cli.
//
//	becky-vision --image <path> [--prompt "..."] [--json] [options]
//
// It assembles the verified-good llama-mtmd-cli invocation
//
//	llama-mtmd-cli -m <model> --mmproj <mmproj> --image <img> -ngl 99 --temp 0 -p "<prompt>"
//
// runs it once, and prints either a plain-language description (default) or a
// JSON object (--json). The model + mmproj GGUFs are discovered in the model dir
// when not given explicitly.
//
// This stays becky-shaped: OFFLINE (the only AI-in-the-loop is one explicit
// local .exe call), DETERMINISTIC (temperature 0 → same image+prompt → same
// output), and DEGRADE-NEVER-CRASH (a missing binary/model/mmproj/image yields a
// plain-language note and exit 0 with degraded:true — never a panic). It is
// image-only by design; Gemma-4 stays for AUDIO (SPEC-BECKY-VISION-MODELS.md).
//
// Exit codes: 0 = ran (incl. a clean degrade), 1 = unexpected error, 2 = usage.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"becky-go/internal/avlm"
	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/vision"
)

func main() {
	image := flag.String("image", "", "REQUIRED: path to the image to describe / extract from")
	model := flag.String("model", "", "main model GGUF (default: discover in the model dir)")
	mmproj := flag.String("mmproj", "", "multimodal projector GGUF (default: discover in the model dir)")
	bin := flag.String("bin", "", "path to llama-mtmd-cli.exe (default: "+vision.DefaultBin+")")
	prompt := flag.String("prompt", "", "instruction for the model (default: a concise describe-this-image prompt)")
	dir := flag.String("dir", "", "directory to discover model/mmproj in (default: "+vision.DefaultModelDir+")")
	ngl := flag.Int("ngl", vision.DefaultNGL, "GPU layers to offload (99 = full)")
	asJSON := flag.Bool("json", false, "emit JSON instead of a plain-language report")
	gemma := flag.Bool("gemma", false, "use the stronger Gemma-4 model (via llama-server) for this still, instead of the fast LFM2.5-VL (better for fine detail the tiny model gets wrong)")
	qwen := flag.Bool("qwen", false, "use Qwen3.5-4B (a DIFFERENT family, for SINGLE-IMAGE corroboration) via llama-server instead of LFM2.5-VL; image-only, NEVER video (video+audio is becky-validate/Gemma-4)")
	serverURL := flag.String("server-url", "", "(with --gemma/--qwen) reuse a running multimodal llama-server instead of spawning one per call")
	timeoutSec := flag.Int("timeout", 240, "(with --gemma/--qwen) per-image inference timeout in seconds")
	verbose := flag.Bool("verbose", false, "show progress on stderr (used by --gemma/--qwen)")
	flag.Parse()

	if *image == "" {
		fmt.Fprintln(os.Stderr, "usage: becky-vision --image <path> [--prompt \"...\"] [--gemma|--qwen] [--json] [options]")
		os.Exit(2)
	}

	// Four routes, same vision.Result shape, ALL single-image: --qwen / --gemma
	// remain manual single-model escape hatches (debugging, back-compat); an
	// explicit --model/--mmproj/--dir/--bin also forces the old single-Describe
	// call exactly as before. The plain NO-FLAGS call — becky-vision --image
	// <p> --prompt "<q>", nothing else — is THE compiled-in escalation core
	// (becky-AI-Agent-review-1.md): becky decides for itself how far to climb
	// 450M -> 1.6B -> Gemma-4 E4B -> Gemma-4 12B; the caller never picks a model.
	// None of these watch video — for WATCHING a video segment with audio, use
	// becky-validate (Gemma-4 E4B->12B).
	var res vision.Result
	switch {
	case *qwen:
		res = describeWithQwen(*image, *prompt, *serverURL, *timeoutSec, *verbose)
	case *gemma:
		res = describeWithGemma(*image, *prompt, *serverURL, *timeoutSec, *verbose)
	case *model != "" || *mmproj != "" || *dir != "" || *bin != "":
		res = vision.Describe(vision.Options{
			Image:    *image,
			Model:    *model,
			MMProj:   *mmproj,
			Bin:      *bin,
			Prompt:   *prompt,
			ModelDir: *dir,
			NGL:      *ngl,
		})
	default:
		res = runLadder(*image, *prompt, *verbose)
	}

	if *asJSON {
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

// printReport writes a plain-language result for a non-developer: the
// description plus a one-line provenance, or an honest degrade note.
func printReport(res vision.Result) {
	if res.Degraded {
		fmt.Println("becky-vision could not describe the image.")
		fmt.Println("  reason:", res.Error)
		fmt.Println("  (this is a graceful degrade — the model or a file was missing, not a crash.)")
		return
	}
	fmt.Println(res.Description)
	fmt.Println()
	fmt.Println(res.Provenance())
	if res.Confidence > 0 {
		line := fmt.Sprintf("  confidence: %.0f%%", res.Confidence*100)
		if res.Escalations > 0 {
			line += fmt.Sprintf(" (escalated %d rung(s) past the fast model)", res.Escalations)
		}
		if res.Validated {
			line += " [validated: a second source corroborated this]"
		}
		fmt.Println(line)
	}
}

// describeWithGemma runs ONE still through the stronger Gemma-4 model via
// llama-server (internal/avlm), returning the same vision.Result shape as the
// LFM path so --json and printReport are unchanged. Model paths come from config
// (BECKY_AVLM_VARIANT=12b selects the bigger verify-tier model when present).
// Every failure degrades to Result{Degraded:true} — never a panic.
func describeWithGemma(image, prompt, serverURL string, timeoutSec int, verbose bool) vision.Result {
	if prompt == "" {
		prompt = "Describe this image factually and in detail."
	}
	cfg := config.Load()
	model, mmproj, label := cfg.GemmaAVLM()
	res := vision.Result{
		Tool:   vision.ToolName,
		Image:  image,
		Model:  label, // a model NAME, not a path; Provenance shows it as-is
		Engine: "Gemma-4",
		Prompt: prompt,
	}
	logf := func(format string, a ...any) { beckyio.Logf(verbose, format, a...) }
	runner := avlm.New(model, mmproj, cfg.LlamaServer, serverURL, cfg.FFmpeg, cfg.FFprobe, logf)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	out, err := runner.AnalyzeImage(ctx, image, avlm.ImageOptions{Prompt: prompt, Verbose: verbose})
	if err != nil {
		res.Degraded = true
		res.Error = err.Error()
		return res
	}
	res.Description = out.Text
	return res
}

// describeWithQwen runs ONE still through Qwen3.5-4B via llama-server
// (internal/avlm.AnalyzeImage) — a DIFFERENT model family than Gemma-4/LFM, for
// SINGLE-IMAGE corroboration (an agreeing read across families is real evidence).
// Same vision.Result shape as the other paths so --json and printReport are
// unchanged. Model paths come from config.Qwen() (BECKY_QWEN_MODEL overrides).
// IMAGE-ONLY by construction (AnalyzeImage sends exactly one still, no frames, no
// audio) — Qwen3.5-4B never watches video; that is becky-validate/Gemma-4's job.
// Every failure degrades to Result{Degraded:true} — never a panic.
func describeWithQwen(image, prompt, serverURL string, timeoutSec int, verbose bool) vision.Result {
	if prompt == "" {
		prompt = "Describe this image factually and in detail."
	}
	cfg := config.Load()
	model, mmproj, label := cfg.Qwen()
	res := vision.Result{
		Tool:   vision.ToolName,
		Image:  image,
		Model:  label, // a model NAME, not a path; Provenance shows it as-is
		Engine: "Qwen3.5-4B",
		Prompt: prompt,
	}
	logf := func(format string, a ...any) { beckyio.Logf(verbose, format, a...) }
	runner := avlm.New(model, mmproj, cfg.LlamaServer, serverURL, cfg.FFmpeg, cfg.FFprobe, logf)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	out, err := runner.AnalyzeImage(ctx, image, avlm.ImageOptions{Prompt: prompt, Verbose: verbose})
	if err != nil {
		res.Degraded = true
		res.Error = err.Error()
		return res
	}
	res.Description = out.Text
	return res
}
