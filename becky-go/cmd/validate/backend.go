// backend.go — the pluggable validation-backend abstraction and its three
// implementations:
//
//   - gemma4-local (default): the real audio-visual path. Feeds frames + 16 kHz
//     mono audio + the question set to Gemma-4 E4B-it via internal/avlm
//     (llama-mtmd-cli, -ngl 99), parses a JSON array of cross-modal observations.
//   - fusion (graceful degrade): when the AV model can't load, feeds the
//     transcript + a TEXT-only description of frames + a coarse tone signal to
//     Gemma-4 (text/vision) and asks the same cross-modal questions. ~80% value.
//   - mock (offline deterministic): derives plausible observations from the real
//     transcript/events for CI without any model.
//
// Every backend takes the same inputs and returns observations + the resolved
// model id + an optional graceful-degradation note. A backend NEVER crashes the
// tool: on failure it returns an empty/partial observation set plus a note, and
// the caller still emits valid JSON and exits 0.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/avlm"
	"becky-go/internal/beckyio"
	"becky-go/internal/config"
)

// validateInput is everything a backend needs to validate one clip.
type validateInput struct {
	File        string
	Transcript  *transcript
	Events      *eventsDoc
	Identify    *identifyDoc
	Questions   []string
	WindowStart float64 // seconds into the clip to start (0 = from the beginning)
	WindowSec   float64
	FPS         float64
	Timeout     int
	ServerURL   string // optional already-running multimodal llama-server to reuse
	Verbose     bool
}

// validateResult is what a backend returns. Note is set when the backend
// degraded (model missing, NaN output, etc.).
type validateResult struct {
	Observations []Observation
	Model        string
	Note         string
}

// Backend is the small interface every validation engine implements.
type Backend interface {
	Name() string
	Validate(ctx context.Context, cfg config.Config, in validateInput) (validateResult, error)
}

// newBackend resolves a backend by name. Unknown names are an error.
func newBackend(name string) (Backend, error) {
	switch strings.ToLower(name) {
	case "mock":
		return mockBackend{}, nil
	case "gemma4-local", "gemma4", "local":
		return gemma4LocalBackend{}, nil
	case "fusion":
		return fusionBackend{}, nil
	default:
		return nil, fmt.Errorf("unknown backend %q (use gemma4-local, fusion, or mock)", name)
	}
}

const gemmaModelName = "gemma-4-E4B-it-Q4_K_M"

// ---------------------------------------------------------------------------
// gemma4-local backend — the real audio-visual path via internal/avlm.
// ---------------------------------------------------------------------------

type gemma4LocalBackend struct{}

func (gemma4LocalBackend) Name() string { return "gemma4-local" }

// Validate runs the TWO-STAGE forensic flow on the clip: caption each frame
// individually (Gemma-4-E4B drops subtle/occluded contact when given many frames
// at once), analyze the audio tone once, then synthesize a timestamped neutral
// anatomical incident log. All failure modes (model/mmproj missing, server crash,
// timeout, empty output, unparseable JSON) degrade to a note + empty observations;
// never a hard error.
func (gemma4LocalBackend) Validate(ctx context.Context, cfg config.Config, in validateInput) (validateResult, error) {
	logf := func(format string, a ...any) { beckyio.Logf(in.Verbose, format, a...) }
	runner := avlm.New(cfg.GemmaModel, cfg.GemmaMMProj, cfg.LlamaServer, in.ServerURL, cfg.FFmpeg, cfg.FFprobe, logf)

	if err := runner.Ready(); err != nil {
		return validateResult{
			Model: gemmaModelName,
			Note:  "gemma4-local skipped: " + err.Error(),
		}, nil
	}

	// Optional case context (transcript/events/identify) is woven into the
	// synthesis instruction so names + said-words enrich the incident log.
	preamble := buildContextPreamble(in.Transcript, in.Events, in.Identify)
	synthPrompt := synthUserPrompt
	if preamble != "" {
		synthPrompt = synthUserPrompt + "\n\n# Case context (optional)\n" + preamble
	}

	// Persist the sampled frames so any contact observation can link to a real,
	// human-openable image (the frame-linking gate below depends on this). Frames
	// land under the OS temp dir keyed by the clip stem; the source video is never
	// touched.
	framesDir := validateFramesDir(in.File)

	res, err := runner.AnalyzeFrameByFrame(ctx, avlm.TwoStageOptions{
		Clip:          in.File,
		CaptionSystem: captionSystemPrompt,
		CaptionPrompt: captionUserPrompt,
		AudioSystem:   audioSystemPrompt,
		AudioPrompt:   audioUserPrompt,
		SynthSystem:   synthSystemPrompt,
		SynthPrompt:   synthPrompt,
		WindowStart:   in.WindowStart,
		WindowSec:     in.WindowSec,
		FPS:           in.FPS,
		FramesDir:     framesDir,
		Temperature:   0.2,
		Seed:          42,
		Verbose:       in.Verbose,
	})
	if err != nil {
		// avlm returns *DegradeError for every recoverable failure.
		return validateResult{
			Model: gemmaModelName,
			Note:  "gemma4-local degraded: " + err.Error(),
		}, nil
	}

	obs, ok := parseObservations(res.SynthesisText)
	if !ok {
		return validateResult{
			Model: gemmaModelName,
			Note:  "gemma4-local degraded: synthesis output was not a JSON array: " + tail(res.SynthesisText),
		}, nil
	}

	// Gate 1 (F2): no physical_contact / possible_contact may be emitted without a
	// linked frame a human can open. Resolve cited frames to real paths; downgrade
	// any unlinkable contact claim to a plain visual observation.
	obs = gateContactFrames(obs, res.Captions)

	// Gate 2 (F5): if VAD shows ~no speech, suppress any asserted audio_tone so we
	// never report "subdued / deliberate" on silence. Bounded to the same window
	// the AV model saw; degrades to "leave tone alone" if VAD can't run.
	sp := clipSpeechPct(ctx, cfg, in.File, in.WindowStart, in.WindowSec, logf)
	obs = suppressToneOnSilence(obs, sp)

	logf("gemma4-local: %d observation(s) from %d frame caption(s)+%.1fs audio (speech=%.1f%%, %.2fs)",
		len(obs), len(res.Captions), res.AudioSec, sp.Pct, sp.Seconds)
	return validateResult{Observations: obs, Model: gemmaModelName}, nil
}

// validateFramesDir returns a stable per-clip directory under the OS temp dir
// where the sampled frames are persisted for human verification of any contact
// observation. The source video is never written to.
func validateFramesDir(clip string) string {
	stem := strings.TrimSuffix(filepath.Base(clip), filepath.Ext(clip))
	if stem == "" {
		stem = "clip"
	}
	return filepath.Join(os.TempDir(), "becky-validate-frames", stem)
}

// ---------------------------------------------------------------------------
// fusion backend — graceful-degrade DIY path.
//
// When the AV model can't load, we still want ~80% of the value. We feed the
// VISION side (sampled frames) + the becky-transcribe transcript + a coarse
// tone signal (derived as TEXT from the events context) to Gemma-4 in
// text+vision mode and ask the same cross-modal questions. This keeps the tool
// useful and OFFLINE without depending on the fragile audio encoder.
//
// A dedicated speech-emotion (prosody) model is intentionally NOT wired in yet:
// the briefing says "keep simple if a tone model isn't readily available;
// document". We pass the transcript + events as the tone proxy and label the
// output as fusion-derived so a human knows tone is approximate.
// ---------------------------------------------------------------------------

type fusionBackend struct{}

func (fusionBackend) Name() string { return "fusion" }

func (fusionBackend) Validate(ctx context.Context, cfg config.Config, in validateInput) (validateResult, error) {
	logf := func(format string, a ...any) { beckyio.Logf(in.Verbose, format, a...) }
	runner := avlm.New(cfg.GemmaModel, cfg.GemmaMMProj, cfg.LlamaServer, in.ServerURL, cfg.FFmpeg, cfg.FFprobe, logf)

	if err := runner.Ready(); err != nil {
		return validateResult{
			Model: gemmaModelName,
			Note:  "fusion skipped (no vision model available): " + err.Error(),
		}, nil
	}

	// Fusion prompt: frames (vision) + transcript + events-as-tone-proxy, asked
	// as the same cross-modal questions, with the tone signal explicitly flagged
	// as text-derived.
	preamble := buildContextPreamble(in.Transcript, in.Events, in.Identify)
	fusionNote := "NOTE: tone is approximated from the transcript + detected events (text), " +
		"not a true audio encoder. Treat tone findings as lower-confidence."
	prompt := buildUserPrompt(strings.TrimSpace(preamble+"\n"+fusionNote), in.Questions)

	res, err := runner.Analyze(ctx, avlm.Options{
		Clip:         in.File,
		Prompt:       prompt,
		SystemPrompt: systemPrompt,
		WindowStart:  in.WindowStart,
		WindowSec:    in.WindowSec,
		FPS:          in.FPS,
		MaxTokens:    768,
		Temperature:  0.2,
		Seed:         42,
		Verbose:      in.Verbose,
	})
	if err != nil {
		return validateResult{
			Model: gemmaModelName,
			Note:  "fusion degraded: " + err.Error(),
		}, nil
	}

	obs, ok := parseObservations(res.Text)
	if !ok {
		return validateResult{
			Model: gemmaModelName,
			Note:  "fusion degraded: model output was not a JSON array: " + tail(res.Text),
		}, nil
	}
	// Mark every fusion observation so a human knows tone is approximate.
	for i := range obs {
		obs[i].AudioTone = "(fusion: text-approximated) " + obs[i].AudioTone
	}
	logf("fusion: parsed %d observation(s) (frames+transcript+events tone proxy)", len(obs))
	return validateResult{
		Observations: obs,
		Model:        gemmaModelName,
		Note:         "fusion backend: tone is text-approximated, not from the audio encoder.",
	}, nil
}

// ---------------------------------------------------------------------------
// mock backend — deterministic, offline, the guaranteed CI/test path.
// ---------------------------------------------------------------------------

type mockBackend struct{}

func (mockBackend) Name() string { return "mock" }

// Validate derives plausible, deterministic cross-modal observations from the
// REAL transcript + events context (no model, fully reproducible). For each
// question it pairs the question with the nearest transcript segment as
// "content" and a neutral tone, so CI exercises the full output schema.
func (mockBackend) Validate(_ context.Context, _ config.Config, in validateInput) (validateResult, error) {
	var obs []Observation

	seg := firstSegment(in.Transcript)
	for i, q := range in.Questions {
		start, end := 0.0, 0.0
		content := ""
		if seg != nil {
			start, end, content = seg.Start, seg.End, strings.TrimSpace(seg.Text)
		}
		// The mock asserts a (deterministic) match so the schema's bool is
		// exercised; significance scales with the question index for variety.
		obs = append(obs, Observation{
			Type:             "cross_modal",
			SegmentStart:     start,
			SegmentEnd:       end,
			Question:         q,
			Visual:           "(mock) no vision model run; visual analysis unavailable",
			AudioTone:        "(mock) tone not analyzed; neutral assumed",
			Content:          content,
			Finding:          fmt.Sprintf("(mock) deterministic stub for question %d; a real backend would reason cross-modally.", i+1),
			ToneContentMatch: boolPtr(true),
			Confidence:       0.3,
			Significance:     significanceForIndex(i),
			Rationale:        "Mock backend: derived from transcript context only, for offline/CI testing.",
			Reviewed:         false,
		})
	}

	// Also surface one observation per detected event, so events context flows
	// through deterministically.
	if in.Events != nil {
		for _, e := range in.Events.Events {
			obs = append(obs, Observation{
				Type:             "visual",
				SegmentStart:     e.Start,
				SegmentEnd:       e.End,
				Question:         "Is the detected event consistent with the surrounding context?",
				Visual:           fmt.Sprintf("(mock) becky-events flagged a %s here", e.Type),
				AudioTone:        "(mock) not analyzed",
				Content:          e.Description,
				Finding:          fmt.Sprintf("(mock) %s event surfaced for cross-modal review", e.Type),
				ToneContentMatch: nil,
				Confidence:       round3(nonZero(e.Confidence, 0.5)),
				Significance:     "low",
				Rationale:        "Mock backend: event passthrough for offline/CI testing.",
				Reviewed:         false,
			})
		}
	}

	beckyio.Logf(in.Verbose, "mock backend produced %d observation(s) from %d question(s)", len(obs), len(in.Questions))
	return validateResult{
		Observations: normalize(obs),
		Model:        "mock-deterministic-v1",
	}, nil
}

// ---------------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------------

func firstSegment(tr *transcript) *transcriptSegment {
	if tr == nil || len(tr.Segments) == 0 {
		return nil
	}
	return &tr.Segments[0]
}

func significanceForIndex(i int) string {
	if i == 0 {
		return "medium"
	}
	return "low"
}

func nonZero(v, fallback float64) float64 {
	if v <= 0 {
		return fallback
	}
	return v
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 400 {
		return s[len(s)-400:]
	}
	return s
}
