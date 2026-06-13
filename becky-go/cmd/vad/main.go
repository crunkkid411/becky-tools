// becky-vad — Voice Activity Detection via Silero (sherpa-onnx).
//
//	becky-vad <audio_or_video> [--output f] [--format json|txt]
//	          [--threshold 0.5] [--min-speech-pct 20] [--fps 29.97]
//	          [--device cuda|cpu] [--keep-temp] [--verbose]
//
// Identifies which spans of a recording are speech vs silence/noise and emits a
// contiguous, alternating speech/non-speech timeline. Each segment carries its
// own speech percentage and a `keep` flag (speech_pct >= --min-speech-pct).
//
// This is a segment-level filter (NEVER frame-level AND/OR logic) and contains
// NO LLM. Heavy compute is Silero VAD inside sherpa-onnx; the embedded
// vad_silero.py is thin glue, materialized at runtime so the .exe is
// self-contained. JSON goes to stdout; diagnostics to stderr; exit 0 on success.
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

// Segment is one span of the alternating speech/non-speech timeline.
type Segment struct {
	Start     float64 `json:"start"`
	End       float64 `json:"end"`
	SpeechPct float64 `json:"speech_pct"`
	Keep      bool    `json:"keep"`
}

// Output is the becky-vad JSON contract.
type Output struct {
	File      string    `json:"file"`
	Duration  float64   `json:"duration"`
	FPS       float64   `json:"fps"`
	Threshold float64   `json:"threshold"`
	SpeechPct float64   `json:"speech_pct"`
	Segments  []Segment `json:"segments"`
}

// helperFullSeg mirrors one entry of vad_silero.py's --full-segments output.
type helperFullSeg struct {
	Start     float64 `json:"start"`
	End       float64 `json:"end"`
	SpeechPct float64 `json:"speech_pct"`
	IsSpeech  bool    `json:"is_speech"`
}

// helperResult mirrors vad_silero.py's --full-segments JSON.
type helperResult struct {
	Skipped      bool            `json:"skipped"`
	Reason       string          `json:"reason"`
	SampleRate   int             `json:"sample_rate"`
	Duration     float64         `json:"duration"`
	Threshold    float64         `json:"threshold"`
	SpeechPct    float64         `json:"speech_pct"`
	FullSegments []helperFullSeg `json:"full_segments"`
}

func main() {
	out := flag.String("output", "", "output file (default: stdout)")
	format := flag.String("format", "json", "output format: json, txt")
	threshold := flag.Float64("threshold", 0.5, "VAD sensitivity 0.0-1.0")
	minSpeechPct := flag.Float64("min-speech-pct", 20.0, "min %% speech to keep a segment")
	fps := flag.Float64("fps", 29.97, "video frame rate for frame-accurate fields")
	device := flag.String("device", "cuda", "device: cuda, cpu (informational; VAD runs on CPU)")
	keepTemp := flag.Bool("keep-temp", false, "keep the extracted temp WAV")
	verbose := flag.Bool("verbose", false, "show progress on stderr")

	input := parsePositional()
	if input == "" {
		beckyio.Fatalf("usage: becky-vad <audio_or_video> [options]")
	}
	if _, err := os.Stat(input); err != nil {
		beckyio.Fatalf("input not found: %s", input)
	}
	_ = *device // accepted for interface parity; sherpa Silero VAD runs on CPU.

	cfg := config.Load()
	if cfg.SileroVADModel == "" || !fileExists(cfg.SileroVADModel) {
		beckyio.Fatalf("silero_vad.onnx not found (config silero_vad_model=%q)", cfg.SileroVADModel)
	}

	info, err := mediainfo.Probe(cfg.FFprobe, input)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}

	// Always feed sherpa a 16 kHz mono PCM WAV. For an audio or video input we
	// extract one with ffmpeg; the standard recipe also normalizes odd WAVs.
	beckyio.Logf(*verbose, "extracting 16kHz mono audio with ffmpeg...")
	wav, err := extractAudio(cfg.FFmpeg, input)
	if err != nil {
		beckyio.Fatalf("audio extraction failed: %v", err)
	}
	if !*keepTemp {
		defer os.Remove(wav)
	} else {
		beckyio.Logf(*verbose, "kept temp wav: %s", wav)
	}

	script, err := pyhelpers.Materialize("vad_silero.py", pyhelpers.VADSilero)
	if err != nil {
		beckyio.Fatalf("materialize VAD helper: %v", err)
	}

	beckyio.Logf(*verbose, "running Silero VAD (threshold=%.2f)...", *threshold)
	res, err := runHelper(cfg.Python, script, cfg.SileroVADModel, wav, *threshold, *verbose)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	if res.Skipped {
		beckyio.Fatalf("VAD skipped: %s", res.Reason)
	}

	// Prefer the helper's measured duration; fall back to the ffprobe value.
	duration := res.Duration
	if duration <= 0 {
		duration = info.Duration
	}
	frameRate := *fps
	if frameRate <= 0 {
		frameRate = info.FPS
	}

	segments := make([]Segment, 0, len(res.FullSegments))
	for _, s := range res.FullSegments {
		segments = append(segments, Segment{
			Start:     round3(s.Start),
			End:       round3(s.End),
			SpeechPct: round2(s.SpeechPct),
			Keep:      s.SpeechPct >= *minSpeechPct,
		})
	}

	output := Output{
		File:      input,
		Duration:  round3(duration),
		FPS:       round3(frameRate),
		Threshold: *threshold,
		SpeechPct: round2(res.SpeechPct),
		Segments:  segments,
	}

	kept, speech := 0, 0
	for _, s := range segments {
		if s.Keep {
			kept++
		}
		if s.SpeechPct > 0 {
			speech++
		}
	}
	beckyio.Logf(*verbose, "%d segments (%d speech, %d kept), %.1f%% speech overall",
		len(segments), speech, kept, output.SpeechPct)

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
// non-flag token, so this enables `becky-vad in.mp4 --verbose`).
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

// extractAudio writes a 16 kHz mono PCM WAV for sherpa using the standard recipe.
func extractAudio(ffmpeg, input string) (string, error) {
	tmp, err := os.CreateTemp("", "becky_vad_*.wav")
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	tmp.Close()
	cmd := exec.Command(ffmpeg, "-y", "-i", input,
		"-vn", "-ac", "1", "-ar", "16000", "-acodec", "pcm_s16le",
		"-loglevel", "error", path)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("ffmpeg: %v\n%s", err, tail(errBuf.String()))
	}
	return path, nil
}

// runHelper runs vad_silero.py in --full-segments mode and parses its JSON.
func runHelper(python, script, model, wav string, threshold float64, verbose bool) (helperResult, error) {
	cmd := exec.Command(python, script, wav,
		"--model", model,
		"--threshold", fmt.Sprintf("%.3f", threshold),
		"--full-segments")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	if verbose {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = &stderr
	}
	if err := cmd.Run(); err != nil {
		return helperResult{}, fmt.Errorf("VAD helper failed: %v\n%s", err, tail(stderr.String()))
	}
	res, ok := parseHelperJSON(stdout.String())
	if !ok {
		return helperResult{}, fmt.Errorf("could not parse VAD helper output:\n%s", tail(stdout.String()))
	}
	return res, nil
}

// parseHelperJSON tolerates leading sherpa C++ log noise by scanning lines
// bottom-up for the first that unmarshals into the expected shape.
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
	if json.Unmarshal([]byte(s), &r) == nil && (r.Skipped || r.FullSegments != nil || r.SampleRate > 0) {
		return r, true
	}
	return helperResult{}, false
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
		var b strings.Builder
		for _, s := range o.Segments {
			status := "cut"
			if s.Keep {
				status = "keep"
			}
			fmt.Fprintf(&b, "%.3f\t%.3f\t%.1f\t%s\n", s.Start, s.End, s.SpeechPct, status)
		}
		return b.String(), nil
	default:
		return "", fmt.Errorf("unknown format: %s (use json, txt)", format)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }
func round3(f float64) float64 { return float64(int(f*1000+0.5)) / 1000 }

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 800 {
		return s[len(s)-800:]
	}
	return s
}
