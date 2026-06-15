// becky-transcribe — Parakeet-TDT-0.6B-v3 speech-to-text via sherpa-onnx.
//
//	becky-transcribe <input> [--output f] [--format json|srt|txt|vtt]
//	                 [--lang en] [--device auto|cuda|cpu] [--num-threads N] [--verbose]
//
// --device defaults to "auto": run on CUDA when it works and fall back to CPU on
// an out-of-memory (or any GPU) failure, re-running the clip so a transcript is
// still produced. "cuda" forces GPU-only; "cpu" forces CPU-only.
//
// JSON goes to stdout (or --output); diagnostics go to stderr; exit 0 on success.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/mediainfo"
	"becky-go/internal/pyhelpers"
)

// Word is one recognized word with its timing.
type Word struct {
	Word       string   `json:"word"`
	Start      float64  `json:"start"`
	End        float64  `json:"end"`
	Confidence *float64 `json:"confidence"`
}

// Segment is a caption-sized grouping of words.
//
// SpeechPct / LowConfidence are populated by the VAD gate. A segment that
// overlaps a VAD speech span but with very little actual speech (a sub-second
// blip in an otherwise near-silent clip — the regime where ASR emits stock
// hallucinations like "Thank you") is KEPT but flagged low_confidence so a human
// or the index can treat it with suspicion instead of trusting it as speech.
// Segments with essentially no VAD speech are dropped entirely (see VADDropped).
type Segment struct {
	Start         float64  `json:"start"`
	End           float64  `json:"end"`
	Text          string   `json:"text"`
	SpeechPct     *float64 `json:"speech_pct,omitempty"`     // % of the segment VAD flagged as speech (nil if gate didn't run)
	LowConfidence bool     `json:"low_confidence,omitempty"` // true if too little real speech to trust as a transcript
}

// DroppedSegment is one segment the VAD speech-mask gate removed because it fell
// in a no-speech region (a likely ASR hallucination, e.g. "Thank you for
// watching" over silence). Kept as an audit trail so a human can see exactly
// what was filtered and why — the segment never enters the searchable index.
type DroppedSegment struct {
	Start     float64 `json:"start"`
	End       float64 `json:"end"`
	Text      string  `json:"text"`
	SpeechPct float64 `json:"speech_pct"` // % of the segment VAD flagged as speech
	Reason    string  `json:"reason"`
}

// Output is the becky-transcribe JSON contract.
type Output struct {
	File     string    `json:"file"`
	Duration float64   `json:"duration"`
	Model    string    `json:"model"`
	Language string    `json:"language"`
	Text     string    `json:"text"`
	Words    []Word    `json:"words"`
	Segments []Segment `json:"segments"`
	// VADApplied reports whether the VAD speech-mask gate ran. VADDropped lists
	// any segments removed as no-speech hallucinations (empty when none / gate
	// skipped). Both are honesty fields: they make the filtering auditable.
	VADApplied bool             `json:"vad_applied"`
	VADDropped []DroppedSegment `json:"vad_dropped,omitempty"`
}

// helperResult mirrors transcribe_parakeet.py's stdout.
type helperResult struct {
	Skipped  bool   `json:"skipped"`
	Reason   string `json:"reason"`
	Model    string `json:"model"`
	Version  string `json:"version"`
	Language string `json:"language"`
	Text     string `json:"text"`
	Words    []Word `json:"words"`
	// Device is the provider that actually produced the transcript (cpu|cuda).
	// FellBack is true when CUDA was tried first and we dropped to CPU (e.g. GPU
	// out-of-memory); FallbackReason carries the GPU error in that case.
	Device         string `json:"device"`
	FellBack       bool   `json:"fell_back"`
	FallbackReason string `json:"fallback_reason"`
}

// Segmentation thresholds: start a new caption segment when speech pauses or the
// running line gets long. These are soft heuristics, not hard rules.
const (
	segGapSeconds = 0.6
	segMaxChars   = 80
)

func main() {
	out := flag.String("output", "", "output file (default: stdout)")
	format := flag.String("format", "json", "output format: json, srt, txt, vtt")
	_ = flag.String("model", "parakeet-v3", "model name (informational)")
	lang := flag.String("lang", "en", "language code")
	device := flag.String("device", "", "device: auto, cuda, cpu (default auto: GPU with automatic CPU fallback on OOM)")
	numThreads := flag.Int("num-threads", 4, "ONNX inference threads")
	keepTemp := flag.Bool("keep-temp", false, "keep the extracted temp WAV")
	noVAD := flag.Bool("no-vad", false, "skip the VAD speech-mask gate (keep ASR segments over silence)")
	verbose := flag.Bool("verbose", false, "show progress on stderr")

	input := parsePositional()
	if input == "" {
		beckyio.Fatalf("usage: becky-transcribe <input> [options]")
	}
	if _, err := os.Stat(input); err != nil {
		beckyio.Fatalf("input not found: %s", input)
	}

	cfg := config.Load()
	// becky-transcribe defaults to "auto" — use CUDA when it works and fall back
	// to CPU on an OOM/GPU failure (handled in transcribe_parakeet.py). This is
	// transcribe-specific on purpose: the shared cfg.Device default stays "cpu" so
	// the other sherpa/ST helpers (diarize, voice, embed) are unaffected. A
	// --device flag still overrides (auto|cuda|cpu).
	dev := "auto"
	if *device != "" {
		dev = *device
	}

	info, err := mediainfo.Probe(cfg.FFprobe, input)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}

	beckyio.Logf(*verbose, "extracting 16kHz mono audio with ffmpeg...")
	wav, err := extractAudio(cfg.FFmpeg, input)
	if err != nil {
		beckyio.Fatalf("audio extraction failed: %v", err)
	}
	if !*keepTemp {
		defer os.Remove(wav)
	}

	script, err := pyhelpers.Materialize("transcribe_parakeet.py", pyhelpers.TranscribeParakeet)
	if err != nil {
		beckyio.Fatalf("materialize helper: %v", err)
	}

	beckyio.Logf(*verbose, "running Parakeet-v3 (device=%s, model=%s)...", dev, cfg.ParakeetModelDir)
	res, err := runHelper(cfg.Python, script, wav, cfg.ParakeetModelDir, dev, *lang, *numThreads, *verbose)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	if res.Skipped {
		beckyio.Fatalf("transcription skipped: %s", res.Reason)
	}
	if res.FellBack {
		beckyio.Logf(*verbose, "GPU run failed (%s) — fell back to CPU", res.FallbackReason)
	} else if res.Device != "" {
		beckyio.Logf(*verbose, "transcribed on %s", res.Device)
	}

	// Force non-nil slices so the "words"/"segments" fields marshal as [] (not
	// null) on a zero-word clip — downstream consumers (becky-embed) expect arrays.
	words := res.Words
	if words == nil {
		words = []Word{}
	}
	segments := segmentize(res.Words)

	// VAD speech-mask gate (F4): drop segments that fall in no-speech regions so
	// ASR hallucinations over silence ("Thank you for watching") never enter the
	// index as real speech. The WAV is still on disk here (removed by defer).
	// Degrades gracefully: if VAD can't run, segments pass through unchanged.
	var vadDropped []DroppedSegment
	vadApplied := false
	if !*noVAD {
		beckyio.Logf(*verbose, "VAD speech-mask gate...")
		segments, vadDropped, vadApplied = gateSegmentsByVAD(cfg, wav, segments, *verbose)
		if vadApplied && len(vadDropped) > 0 {
			beckyio.Logf(*verbose, "VAD gate dropped %d no-speech segment(s)", len(vadDropped))
		}
	}

	// Keep the recognizer's own text unless the VAD gate actually removed
	// something; only then rebuild "text" from the kept segments so it no longer
	// contains a dropped hallucination.
	text := res.Text
	if len(vadDropped) > 0 {
		text = segmentsText(segments)
	}

	output := Output{
		File:       input,
		Duration:   round3(info.Duration),
		Model:      res.Model,
		Language:   res.Language,
		Text:       text,
		Words:      words,
		Segments:   segments,
		VADApplied: vadApplied,
		VADDropped: vadDropped,
	}
	beckyio.Logf(*verbose, "%d words, %d segments (%d dropped by VAD)",
		len(output.Words), len(output.Segments), len(vadDropped))

	rendered, err := render(output, *format)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	if *out == "" {
		fmt.Print(rendered)
	} else {
		if err := os.WriteFile(*out, []byte(rendered), 0o644); err != nil {
			beckyio.Fatalf("write output: %v", err)
		}
		beckyio.Logf(*verbose, "wrote %s", *out)
	}
}

// parsePositional parses leading flags, extracts the first positional argument,
// then re-parses any flags that came after it (Go's flag stops at the first
// non-flag token, so this enables `becky-transcribe in.mp4 --verbose`).
func parsePositional() string {
	flag.Parse()
	rest := flag.Args()
	if len(rest) == 0 {
		return ""
	}
	input := rest[0]
	if len(rest) > 1 {
		_ = flag.CommandLine.Parse(rest[1:])
	}
	return input
}

func extractAudio(ffmpeg, input string) (string, error) {
	tmp, err := os.CreateTemp("", "becky_asr_*.wav")
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	tmp.Close()
	cmd := exec.Command(ffmpeg, "-y", "-i", input,
		"-vn", "-ar", "16000", "-ac", "1", "-acodec", "pcm_s16le",
		"-loglevel", "error", path)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("ffmpeg: %v\n%s", err, tail(errBuf.String()))
	}
	return path, nil
}

func runHelper(python, script, wav, modelDir, device, lang string, numThreads int, verbose bool) (helperResult, error) {
	args := []string{script, wav, "--model-dir", modelDir,
		"--num-threads", fmt.Sprintf("%d", numThreads),
		"--device", device, "--lang", lang}
	cmd := exec.Command(python, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	if verbose {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = &stderr
	}
	if err := cmd.Run(); err != nil {
		return helperResult{}, fmt.Errorf("transcribe helper failed: %v\n%s", err, tail(stderr.String()))
	}
	res, ok := parseHelperJSON(stdout.String())
	if !ok {
		return helperResult{}, fmt.Errorf("could not parse transcribe helper output:\n%s", tail(stdout.String()))
	}
	return res, nil
}

// parseHelperJSON tolerates leading C++ log noise by scanning lines bottom-up
// for the first that unmarshals into the expected shape.
func parseHelperJSON(s string) (helperResult, bool) {
	if r, ok := tryUnmarshal(strings.TrimSpace(s)); ok {
		return r, true
	}
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if r, ok := tryUnmarshal(line); ok {
			return r, true
		}
	}
	return helperResult{}, false
}

func tryUnmarshal(s string) (helperResult, bool) {
	var r helperResult
	if json.Unmarshal([]byte(s), &r) == nil && (r.Skipped || r.Model != "" || r.Text != "" || len(r.Words) > 0) {
		return r, true
	}
	return helperResult{}, false
}

// segmentsText joins the kept segments' text into the top-level transcript, so
// the human-facing "text" reflects only speech that survived the VAD gate (it
// never re-narrates a dropped hallucination). "words" stays raw as the
// lowest-level recognizer audit; "vad_dropped" documents the difference.
func segmentsText(segs []Segment) string {
	parts := make([]string, 0, len(segs))
	for _, s := range segs {
		if t := strings.TrimSpace(s.Text); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " ")
}

// segmentize groups words into caption-sized segments by pause and length.
func segmentize(words []Word) []Segment {
	segs := []Segment{} // non-nil so the JSON "segments" field is [] (not null) when empty
	if len(words) == 0 {
		return segs
	}
	var cur []string
	start := words[0].Start
	end := words[0].End
	prevEnd := words[0].Start
	flush := func() {
		if len(cur) == 0 {
			return
		}
		segs = append(segs, Segment{Start: round3(start), End: round3(end), Text: strings.Join(cur, " ")})
		cur = nil
	}
	for i, w := range words {
		gap := w.Start - prevEnd
		runningLen := len(strings.Join(cur, " "))
		if i > 0 && (gap > segGapSeconds || runningLen >= segMaxChars) {
			flush()
			start = w.Start
		}
		cur = append(cur, w.Word)
		end = w.End
		prevEnd = w.End
	}
	flush()
	return segs
}

func render(o Output, format string) (string, error) {
	switch strings.ToLower(format) {
	case "json", "":
		b, err := json.MarshalIndent(o, "", "  ")
		if err != nil {
			return "", err
		}
		return string(b) + "\n", nil
	case "txt":
		return o.Text + "\n", nil
	case "srt":
		var b strings.Builder
		for i, s := range o.Segments {
			fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, srtTime(s.Start), srtTime(s.End), s.Text)
		}
		return b.String(), nil
	case "vtt":
		var b strings.Builder
		b.WriteString("WEBVTT\n\n")
		for _, s := range o.Segments {
			fmt.Fprintf(&b, "%s --> %s\n%s\n\n", vttTime(s.Start), vttTime(s.End), s.Text)
		}
		return b.String(), nil
	default:
		return "", fmt.Errorf("unknown format: %s (use json, srt, txt, vtt)", format)
	}
}

func srtTime(sec float64) string { return timecode(sec, ",") }
func vttTime(sec float64) string { return timecode(sec, ".") }

// timecode renders seconds as HH:MM:SS<sep>mmm (clip-relative, not a date).
func timecode(sec float64, sep string) string {
	if sec < 0 {
		sec = 0
	}
	ms := int(sec*1000 + 0.5)
	h := ms / 3600000
	ms -= h * 3600000
	m := ms / 60000
	ms -= m * 60000
	s := ms / 1000
	ms -= s * 1000
	return fmt.Sprintf("%02d:%02d:%02d%s%03d", h, m, s, sep, ms)
}

func round3(f float64) float64 {
	return float64(int(f*1000+0.5)) / 1000
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 800 {
		return s[len(s)-800:]
	}
	return s
}
