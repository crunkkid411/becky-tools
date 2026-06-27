// becky-validate — local, offline audio-visual validation of SHORT flagged
// clips. It feeds a clip's sampled frames + 16 kHz mono audio (+ optional
// transcript/events/identify context) and a question set to a local Gemma-4
// E4B-it model and emits cross-modal observations: what is SEEN vs HEARD (tone)
// vs SAID (content), a tone-vs-content flag, confidence, and a load-bearing
// "AI ANALYSIS — candidate, not conclusion" disclaimer.
//
//	becky-validate <clip> [options]
//	  --question <str>       cross-modal question (repeatable); default = built-in forensic set
//	  --transcript <json>    becky-transcribe JSON (optional context)
//	  --events <json>        becky-events JSON (optional context)
//	  --identify <json>      becky-identify JSON (optional speaker/face names)
//	  --motion <json>        becky-motion JSON; targets analysis at the highest-scored burst
//	  --backend <type>       gemma4-local (default) | fusion | mock
//	  --server-url <url>     reuse a running multimodal llama-server (default: spawn per call)
//	  --window <sec>         AV window length, <= 60 (default 30; overridden by --motion)
//	  --fps <float>          frame sample rate (default 1.0; overridden by --motion to 4.0)
//	  --device <cpu|cuda>    informational; GPU offload (-ngl 99) is always used
//	  --output <file>        write JSON here (default: stdout)
//	  --timeout <sec>        per-clip inference timeout (default 240)
//	  --verbose              progress to stderr
//
// When --motion is given, becky-validate reads the motion.json from becky-motion,
// finds the burst with the highest motion_score, and aims its analysis window at
// exactly that interval (padded with 1 s context) at 4 fps — implementing the
// two-tier flow from SPEC-VIDEO-ANALYSIS.md §3: cheap motion detection FINDS the
// burst, expensive AV description DESCRIBES it at the right spot.
//
// becky-validate is the SECOND sanctioned LLM-tool exception (after becky-review)
// to the "no LLM between pipeline steps" rule. It runs ONCE per clip, ONLY on
// short flagged clips, is fully OFFLINE (no remote backend), and NEVER crashes:
// any backend/model failure degrades to valid JSON + a note and exits 0.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
)

// stringList is a repeatable --question flag.
type stringList []string

func (s *stringList) String() string { return fmt.Sprintf("%v", []string(*s)) }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func main() {
	var questions stringList
	flag.Var(&questions, "question", "cross-modal question (repeatable); default = built-in forensic set")
	transcriptPath := flag.String("transcript", "", "path to becky-transcribe JSON (optional context)")
	eventsPath := flag.String("events", "", "path to becky-events JSON (optional context)")
	identifyPath := flag.String("identify", "", "path to becky-identify JSON (optional names)")
	motionPath := flag.String("motion", "", "path to becky-motion JSON; targets analysis at the highest-scored burst window")
	backendName := flag.String("backend", "gemma4-local", "backend: gemma4-local, fusion, mock")
	serverURL := flag.String("server-url", "", "reuse a running multimodal llama-server (default: spawn one per call)")
	window := flag.Float64("window", 30, "AV window length in seconds (<= 60); overridden by --motion")
	fps := flag.Float64("fps", 1.0, "frame sample rate; overridden by --motion to 4.0 for a short targeted burst")
	device := flag.String("device", "", "cpu|cuda (informational; GPU offload always used)")
	out := flag.String("output", "", "output file (default: stdout)")
	timeoutSec := flag.Int("timeout", 240, "per-clip inference timeout in seconds")
	verbose := flag.Bool("verbose", false, "show progress on stderr")

	clip := parsePositional()
	if clip == "" {
		beckyio.Fatalf("usage: becky-validate <clip> [--question ...] [--backend gemma4-local|fusion|mock] [options]")
	}
	if _, err := os.Stat(clip); err != nil {
		beckyio.Fatalf("clip not found: %s", clip)
	}
	if *timeoutSec < 1 {
		*timeoutSec = 240
	}
	_ = *device // accepted for CLI compatibility; -ngl 99 is always used

	backend, err := newBackend(*backendName)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}

	// Optional context: a missing path is fine (nil); a given-but-broken path is
	// a hard error (bad input the caller must fix).
	tr, err := loadTranscript(*transcriptPath)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	ev, err := loadEvents(*eventsPath)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	id, err := loadIdentify(*identifyPath)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}

	// Motion targeting: when --motion is given, derive the analysis window from the
	// highest-scored burst. motionWindow degrades gracefully (returns zeros + a note)
	// on any error, so we never hard-fail on a missing/bad motion file.
	mStart, mDur, mFPS, mNote := motionWindow(*motionPath)
	motionTargeted := mDur > 0
	if motionTargeted {
		*window = mDur
		*fps = mFPS
		beckyio.Logf(*verbose, "%s", mNote)
	} else if mNote != "" {
		beckyio.Logf(*verbose, "%s", mNote)
	}

	qs := []string(questions)
	if len(qs) == 0 {
		qs = defaultQuestions
		beckyio.Logf(*verbose, "no --question supplied; using built-in forensic %d-question set", len(qs))
	}

	cfg := config.Load()

	in := validateInput{
		File:        clip,
		Transcript:  tr,
		Events:      ev,
		Identify:    id,
		Questions:   qs,
		WindowStart: mStart,
		WindowSec:   *window,
		FPS:         *fps,
		Timeout:     *timeoutSec,
		ServerURL:   *serverURL,
		Verbose:     *verbose,
	}

	beckyio.Logf(*verbose, "validating %s with %s backend (start=%.1fs window=%.0fs fps=%.2f timeout=%ds)...",
		clip, backend.Name(), mStart, *window, *fps, *timeoutSec)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutSec)*time.Second)
	defer cancel()

	res, err := backend.Validate(ctx, cfg, in)
	if err != nil {
		// Backends degrade rather than error, but if one does we still emit
		// valid JSON with a note (exit 0).
		res = validateResult{Model: gemmaModelName, Note: fmt.Sprintf("backend error: %v", err)}
	}
	if res.Observations == nil {
		res.Observations = []Observation{} // emit [] not null
	}

	// Combine backend note with any motion-targeting note so the output is self-documenting.
	outputNote := joinNotes(res.Note, mNote)

	report := Output{
		File:              clip,
		ValidatedAt:       time.Now().UTC().Format(time.RFC3339),
		Backend:           backend.Name(),
		Model:             firstNonEmpty(res.Model, gemmaModelName),
		Disclaimer:        Disclaimer,
		WindowStart:       mStart,
		WindowSec:         clampWindowSec(*window),
		FPS:               *fps,
		MotionTargeted:    motionTargeted,
		Observations:      res.Observations,
		ToneVsContentFlag: anyMismatch(res.Observations),
		Note:              outputNote,
	}
	beckyio.Logf(*verbose, "done: %d observation(s), tone_vs_content_flag=%v%s",
		len(report.Observations), report.ToneVsContentFlag, noteSuffix(outputNote))

	if err := emit(report, *out); err != nil {
		beckyio.Fatalf("%v", err)
	}
}

// parsePositional pulls the first positional argument (the clip) and re-parses
// any flags that followed it (Go's flag stops at the first non-flag token).
func parsePositional() string {
	flag.Parse()
	rest := flag.Args()
	if len(rest) == 0 {
		return ""
	}
	clip := rest[0]
	if len(rest) > 1 {
		_ = flag.CommandLine.Parse(rest[1:])
	}
	return clip
}

// loadTranscript reads optional becky-transcribe JSON. "" path -> nil, no error.
func loadTranscript(path string) (*transcript, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read transcript json: %w", err)
	}
	var tr transcript
	if err := json.Unmarshal(data, &tr); err != nil {
		return nil, fmt.Errorf("parse transcript json: %w", err)
	}
	return &tr, nil
}

// loadEvents reads optional becky-events JSON. "" path -> nil, no error.
func loadEvents(path string) (*eventsDoc, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read events json: %w", err)
	}
	var ev eventsDoc
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil, fmt.Errorf("parse events json: %w", err)
	}
	return &ev, nil
}

// loadIdentify reads optional becky-identify JSON. "" path -> nil, no error.
func loadIdentify(path string) (*identifyDoc, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read identify json: %w", err)
	}
	var id identifyDoc
	if err := json.Unmarshal(data, &id); err != nil {
		return nil, fmt.Errorf("parse identify json: %w", err)
	}
	return &id, nil
}

// clampWindowSec mirrors the model's video cap for the reported window.
func clampWindowSec(sec float64) float64 {
	if sec <= 0 {
		return 30
	}
	if sec > 60 {
		return 60
	}
	return sec
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
	if a != "" {
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

// joinNotes combines two optional note strings; both may be empty.
func joinNotes(a, b string) string {
	switch {
	case a == "" && b == "":
		return ""
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "; " + b
	}
}
