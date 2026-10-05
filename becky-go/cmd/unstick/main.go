// becky-unstick — look at a STUCK browser screenshot and say what to click.
//
//	becky-unstick --image <screenshot.png> [--goal "..."] [--server-url URL] [--verbose]
//
// The agent browser (X:\agent-browser\ff.mjs) calls this ONLY when a page looks
// stuck: a load past ~30s, or a click that changed nothing. It runs Microsoft's
// Fara1.5-4B (a Qwen3.5-4B fine-tuned purely for browser control) locally via
// llama-server, with Fara's own training-time system prompt (fara_system.txt,
// rendered verbatim from github.com/microsoft/fara), and prints ONE JSON object:
//
//	{"ok":true,"action":"left_click","x":853,"y":324,"thoughts":"A popup...","raw":"..."}
//
// x/y are in SCREENSHOT pixels (Fara answers on a fixed 1000x1000 grid; we scale
// it back using the image's real size). "action":"terminate" means Fara sees
// nothing blocking the page. The caller decides whether to click — this tool
// never touches the browser itself.
//
// Becky-shaped: offline (one local model call), deterministic (temperature 0 is
// approximated by avlm's low-temp fixed-seed request), headless (llama-server is
// spawned with no console window), and degrade-never-crash: a missing model,
// server or image yields {"ok":false,"degraded":true,"error":...} and exit 0.
//
// Exit codes: 0 = ran (incl. a clean degrade), 2 = usage.
package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"time"

	"becky-go/internal/avlm"
	"becky-go/internal/beckyio"
	"becky-go/internal/config"
)

//go:embed fara_system.txt
var faraSystem string

const (
	defaultModel  = `X:\AI-2\becky-tools\models\fara1.5-4b\Fara1.5-4B-Q4_K_M.gguf`
	defaultMMProj = `X:\AI-2\becky-tools\models\fara1.5-4b\mmproj-Fara1.5-4B-f16.gguf`
	defaultGoal   = "Something on this web page may be blocking it: a popup, cookie banner, sign-up box, ad overlay or dialog. " +
		"If something is blocking the page, close or dismiss it (prefer an X, Close, No thanks or Reject button; never sign in, " +
		"subscribe, pay or accept anything that shares data). If nothing is blocking the page, terminate and say so."
)

func main() {
	img := flag.String("image", "", "REQUIRED: screenshot of the stuck browser page (png/jpg)")
	goal := flag.String("goal", defaultGoal, "what Fara should try to do on this screen")
	model := flag.String("model", defaultModel, "Fara GGUF")
	mmproj := flag.String("mmproj", defaultMMProj, "Fara vision projector GGUF")
	serverURL := flag.String("server-url", "", "reuse a running llama-server that already holds Fara (else one is spawned, hidden)")
	timeoutSec := flag.Int("timeout", 180, "seconds before giving up")
	verbose := flag.Bool("verbose", false, "progress on stderr")
	flag.Parse()

	if *img == "" {
		beckyio.PrintJSON(map[string]any{"ok": false, "error": "usage: becky-unstick --image <screenshot.png> [--goal \"...\"]"})
		os.Exit(2)
	}
	beckyio.PrintJSON(run(*img, *goal, *model, *mmproj, *serverURL, *timeoutSec, *verbose))
}

func run(imgPath, goal, model, mmproj, serverURL string, timeoutSec int, verbose bool) map[string]any {
	fail := func(msg string) map[string]any { return map[string]any{"ok": false, "degraded": true, "error": msg} }

	w, h, err := imageSize(imgPath)
	if err != nil {
		return fail("cannot read screenshot: " + err.Error())
	}
	cfg := config.Load()
	logf := func(format string, a ...any) { beckyio.Logf(verbose, format, a...) }
	runner := avlm.New(model, mmproj, cfg.LlamaServer, serverURL, cfg.FFmpeg, cfg.FFprobe, logf)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()
	start := time.Now()
	out, err := runner.AnalyzeImage(ctx, imgPath, avlm.ImageOptions{
		SystemPrompt: faraSystem, Prompt: goal, MaxTokens: 768, Temperature: 0.01, Seed: 42,
	})
	if err != nil {
		return fail(err.Error())
	}
	act, err := parseAction(out.Text, w, h)
	if err != nil {
		r := fail(err.Error())
		r["raw"] = out.Text
		return r
	}
	act["ok"] = true
	act["seconds"] = fmt.Sprintf("%.1f", time.Since(start).Seconds())
	act["raw"] = out.Text
	return act
}

func imageSize(path string) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	c, _, err := image.DecodeConfig(f)
	return c.Width, c.Height, err
}
