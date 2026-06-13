// becky-review — per-media context review. Builds a per-file prompt (case
// context + transcript + events), sends it to a pluggable LLM backend, and emits
// context annotations (reference resolutions, notable moments) with rationale,
// confidence, and significance.
//
//	becky-review <video> --transcript <json> --events <json> [options]
//	  --output <file>        write JSON here (default: stdout)
//	  --backend <type>       claude-code (default) | openrouter | mock
//	  --model <name>         model id (default: claude-opus-4-8)
//	  --case-context <path>  case-context markdown (default: built-in guidance)
//	  --concurrency <int>    parallel reviews when batching (default: 3)
//	  --vision               include key frames at event timestamps (best-effort)
//	  --timeout <seconds>    per-file LLM timeout (default: 180)
//	  --verbose              progress to stderr
//
// becky-review is the ONE tool allowed to call an LLM. ONE LLM call per media
// file (preserves nuance — never batch multiple files into one call). JSON to
// stdout; diagnostics to stderr; exit 0 on success (degrades gracefully on
// backend failure rather than crashing).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/osintexport"
)

const defaultModel = "claude-opus-4-8"

func main() {
	out := flag.String("output", "", "output file (default: stdout)")
	backendName := flag.String("backend", "claude-code", "LLM backend: claude-code, openrouter, mock")
	model := flag.String("model", defaultModel, "model name")
	caseCtxPath := flag.String("case-context", "", "path to case context prompt file")
	concurrency := flag.Int("concurrency", 3, "parallel reviews when multiple files are processed")
	transcriptPath := flag.String("transcript", "", "path to becky-transcribe JSON (required)")
	eventsPath := flag.String("events", "", "path to becky-events JSON (required)")
	vision := flag.Bool("vision", false, "extract key frames at event timestamps for visual analysis")
	timeoutSec := flag.Int("timeout", 180, "per-file LLM timeout in seconds")
	verbose := flag.Bool("verbose", false, "show progress on stderr")

	video := parsePositional()
	if video == "" {
		beckyio.Fatalf("usage: becky-review <video> --transcript <json> --events <json> [options]")
	}
	if _, err := os.Stat(video); err != nil {
		beckyio.Fatalf("video not found: %s", video)
	}
	if *transcriptPath == "" {
		beckyio.Fatalf("--transcript <json> is required")
	}
	if *eventsPath == "" {
		beckyio.Fatalf("--events <json> is required")
	}
	if *concurrency < 1 {
		*concurrency = 1
	}

	backend, err := newBackend(*backendName)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}

	tr, err := loadTranscript(*transcriptPath)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	ev, err := loadEvents(*eventsPath)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}

	caseContext, err := loadCaseContext(*caseCtxPath, *verbose)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}

	cfg := config.Load()

	in := reviewInput{
		File:        video,
		Transcript:  tr,
		Events:      ev,
		CaseContext: caseContext,
		Model:       *model,
		Vision:      *vision,
		Verbose:     *verbose,
	}

	// --vision: extract one key frame per event timestamp (best-effort). Frame
	// extraction failures never block the review — they just omit frames.
	if *vision {
		in.Frames = extractKeyFrames(cfg, video, ev, *verbose)
		beckyio.Logf(*verbose, "vision: extracted %d key frame(s)", len(in.Frames))
	}

	// ONE LLM call per media file. The concurrency pool is in place for batch
	// callers; here it runs the single job with the configured per-file timeout.
	beckyio.Logf(*verbose, "reviewing %s with %s backend (timeout=%ds)...", video, backend.Name(), *timeoutSec)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutSec)*time.Second)
	defer cancel()

	res, err := backend.Review(ctx, in)
	if err != nil {
		// Backends are designed to degrade rather than error, but if one does
		// return an error we still emit valid JSON with a note (exit 0).
		res = reviewResult{Model: *model, Note: fmt.Sprintf("backend error: %v", err)}
	}

	report := Output{
		File:        video,
		ReviewedAt:  time.Now().UTC().Format(time.RFC3339),
		Backend:     backend.Name(),
		Model:       firstNonEmpty(res.Model, *model),
		Annotations: res.Annotations,
		Note:        res.Note,
	}
	if report.Annotations == nil {
		report.Annotations = []Annotation{} // emit [] not null
	}
	beckyio.Logf(*verbose, "done: %d annotation(s)%s", len(report.Annotations), noteSuffix(res.Note))

	if err := emit(report, *out); err != nil {
		beckyio.Fatalf("%v", err)
	}
}

// parsePositional pulls the first positional argument (the video) and re-parses
// any flags that followed it (Go's flag stops at the first non-flag token).
func parsePositional() string {
	flag.Parse()
	rest := flag.Args()
	if len(rest) == 0 {
		return ""
	}
	video := rest[0]
	if len(rest) > 1 {
		_ = flag.CommandLine.Parse(rest[1:])
	}
	return video
}

func loadTranscript(path string) (transcript, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return transcript{}, fmt.Errorf("read transcript json: %w", err)
	}
	var tr transcript
	if err := json.Unmarshal(data, &tr); err != nil {
		return transcript{}, fmt.Errorf("parse transcript json: %w", err)
	}
	return tr, nil
}

func loadEvents(path string) (eventsDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return eventsDoc{}, fmt.Errorf("read events json: %w", err)
	}
	var ev eventsDoc
	if err := json.Unmarshal(data, &ev); err != nil {
		return eventsDoc{}, fmt.Errorf("parse events json: %w", err)
	}
	return ev, nil
}

// loadCaseContext reads the case-context file, or returns the built-in default
// guidance when no path is given. A given-but-missing path is a hard error.
func loadCaseContext(path string, verbose bool) (string, error) {
	if path == "" {
		beckyio.Logf(verbose, "no --case-context supplied; using built-in default guidance")
		return defaultCaseContext, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read case-context file: %w", err)
	}
	beckyio.Logf(verbose, "loaded case context from %s (%d bytes)", path, len(data))
	return string(data), nil
}

// extractKeyFrames pulls one full-res frame per event timestamp into a temp dir.
// Best-effort: any failure is logged and that frame is skipped.
func extractKeyFrames(cfg config.Config, video string, ev eventsDoc, verbose bool) []string {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("becky_review_frames_%d", os.Getpid()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		beckyio.Logf(true, "warning: cannot create frame dir: %v", err)
		return nil
	}
	var frames []string
	for i, e := range ev.Events {
		ts := e.Timestamp
		if ts <= 0 {
			ts = e.Start
		}
		jpg := filepath.Join(dir, fmt.Sprintf("frame_%02d_%ds.jpg", i, int(ts)))
		if err := osintexport.ExtractFrame(cfg.FFmpeg, video, ts, jpg, "jpg", 3); err != nil {
			beckyio.Logf(verbose, "  frame at %.2fs failed: %v", ts, err)
			continue
		}
		frames = append(frames, filepath.ToSlash(jpg))
	}
	return frames
}

func emit(o Output, outPath string) error {
	if outPath == "" {
		beckyio.PrintJSON(o)
		return nil
	}
	b, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal output: %w", err)
	}
	if err := os.WriteFile(outPath, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func noteSuffix(note string) string {
	if note == "" {
		return ""
	}
	return " (note: " + note + ")"
}
