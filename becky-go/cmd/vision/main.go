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
	"encoding/json"
	"flag"
	"fmt"
	"os"

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
	flag.Parse()

	if *image == "" {
		fmt.Fprintln(os.Stderr, "usage: becky-vision --image <path> [--prompt \"...\"] [--json] [options]")
		os.Exit(2)
	}

	res := vision.Describe(vision.Options{
		Image:    *image,
		Model:    *model,
		MMProj:   *mmproj,
		Bin:      *bin,
		Prompt:   *prompt,
		ModelDir: *dir,
		NGL:      *ngl,
	})

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
}
