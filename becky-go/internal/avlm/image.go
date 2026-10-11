// image.go — the SINGLE-STILL Gemma-4 path.
//
// AnalyzeImage runs ONE still image + a prompt through the same Gemma-4
// multimodal llama-server the clip path uses, and returns the model's free-text
// answer. There is no ffmpeg, no audio, no frame sampling — it is the
// high-quality "describe/inspect this one frame" route.
//
// Why this exists: becky-vision's fast path (llama-mtmd-cli + the tiny
// LFM2.5-VL) is the only image-only tool, and that small model is cat/dog
// confused and misses fine detail. The GOOD model (Gemma-4) could previously be
// pointed at a still ONLY by wrapping it in a throwaway clip or hand-driving
// llama-server. AnalyzeImage closes that gap so `becky-vision --gemma <frame>`
// (and any future caller) can ask the strong model about a single frame
// directly. llama-mtmd-cli still cannot serve Gemma (it hard-crashes), so this
// goes through llama-server exactly like Analyze.
package avlm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// ImageOptions configures one AnalyzeImage call. Zero values take safe defaults
// (the same determinism posture as the clip path: low temperature, fixed seed).
type ImageOptions struct {
	Prompt       string  // the question / instruction (a neutral describe prompt when empty)
	SystemPrompt string  // optional system framing (forensic neutrality, JSON contract, ...)
	MaxTokens    int     // generation cap (default 512)
	Temperature  float64 // low for determinism (default 0.2)
	Seed         int     // RNG seed for reproducibility (default 42)
	Verbose      bool    // progress to the Runner's Logf
}

// imageMIME maps a still's extension to the data-URL MIME type. Gemma frames are
// JPEG; PNG/WEBP are accepted too. Anything unrecognized is treated as JPEG
// (llama-server sniffs the bytes, so the label is a hint, not a hard gate).
func imageMIME(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		return "image/jpeg"
	}
}

// imageDefaults fills unset ImageOptions with the same safe values the clip path
// uses, and supplies a neutral describe prompt when none is given.
func imageDefaults(o *ImageOptions) {
	if o.MaxTokens <= 0 {
		o.MaxTokens = 512
	}
	if o.Temperature <= 0 {
		o.Temperature = 0.2
	}
	if o.Seed == 0 {
		o.Seed = 42
	}
	if strings.TrimSpace(o.Prompt) == "" {
		o.Prompt = "Describe this image factually and in detail."
	}
}

// AnalyzeImage feeds ONE still image + prompt to Gemma-4 via llama-server and
// returns the answer text. Every recoverable failure (model/mmproj/server
// missing, image missing or unreadable, empty model output) is a *DegradeError
// so the caller emits a clean JSON note and exits 0 — never a panic.
func (r *Runner) AnalyzeImage(ctx context.Context, imagePath string, opts ImageOptions) (Result, error) {
	return r.AnalyzeImages(ctx, []string{imagePath}, opts)
}

// AnalyzeImages is AnalyzeImage for several stills in ONE question, in order, so
// the prompt can say "picture 1 ... picture 2" (becky-identify's side-by-side face
// comparison). Same degrade-never-crash contract.
func (r *Runner) AnalyzeImages(ctx context.Context, imagePaths []string, opts ImageOptions) (Result, error) {
	if err := r.Ready(); err != nil {
		return Result{}, err
	}
	if len(imagePaths) == 0 {
		return Result{}, degrade("no image given", nil)
	}
	imageDefaults(&opts)
	// Text first, then the image parts (the order the clip path uses too).
	parts := []contentPart{{Type: "text", Text: strings.TrimSpace(opts.Prompt)}}
	for _, p := range imagePaths {
		if _, err := os.Stat(p); err != nil {
			return Result{}, degrade("image not found", err)
		}
		b64, err := readBase64(p)
		if err != nil {
			return Result{}, degrade("cannot read image", err)
		}
		parts = append(parts, contentPart{Type: "image_url", ImageURL: &imageURL{URL: "data:" + imageMIME(p) + ";base64," + b64}})
	}

	baseURL, cleanup, err := r.ensureServer(ctx)
	if err != nil {
		return Result{}, err // already a *DegradeError
	}
	defer cleanup()

	r.Logf("avlm: still request (%d picture(s), first %s, %d tok)...", len(imagePaths), filepath.Base(imagePaths[0]), opts.MaxTokens)
	text, err := r.chat(ctx, baseURL, opts.SystemPrompt, parts, opts.Temperature, opts.Seed, opts.MaxTokens)
	if err != nil {
		return Result{}, err // already a *DegradeError
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return Result{}, degrade("model returned empty output", nil)
	}
	return Result{Text: text, FrameCount: len(imagePaths), HadVideo: true}, nil
}
