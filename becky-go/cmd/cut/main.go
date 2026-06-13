// becky-cut — auto-editor with Jordan's exact settings + a VAD post-pass filter.
//
//	becky-cut <input> [--output f] [--margin 0.04s,0.25s] [--export mp4|kdenlive]
//	          [--codec h264_nvenc] [--vad-threshold 0.5] [--vad-speech-pct 20]
//	          [--no-vad] [--dry-run] [--verbose]
//
// Architecture (do not change — ported from the proven ae-vad-wrapper.py):
//  1. auto-editor does native audio detection + margin + smoothing, exports a
//     Premiere XML "keep" timeline.
//  2. For each keep segment, run Silero VAD; if <vad-speech-pct% of frames are
//     speech, flip the segment to a cut (removes coughs, chair squeaks, etc.).
//  3. Render the modified v1 timeline through auto-editor with h264_nvenc.
//
// VAD is a segment-level post-pass filter, NEVER frame-level AND/OR logic.
package main

import (
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/mediainfo"
	"becky-go/internal/pyhelpers"
)

// chunk is one v1-timeline span. Speed 1.0 = keep, large speed = cut.
type chunk struct {
	Start int
	End   int
	Speed float64
}

const (
	keepSpeed     = 1.0
	cutSpeed      = 99999.0
	minVADSeconds = 0.3 // segments shorter than this are left alone (margins)
)

func main() {
	out := flag.String("output", "", "output file (default: <stem>_edited.mp4)")
	margin := flag.String("margin", "0.04s,0.25s", "padding before,after cuts")
	vadThreshold := flag.Float64("vad-threshold", 0.5, "VAD sensitivity 0.0-1.0")
	vadSpeechPct := flag.Float64("vad-speech-pct", 20.0, "min % speech to keep a segment")
	export := flag.String("export", "mp4", "export type: mp4, kdenlive")
	codec := flag.String("codec", "", "video codec (default from config: h264_nvenc)")
	noVAD := flag.Bool("no-vad", false, "skip the VAD post-pass")
	dryRun := flag.Bool("dry-run", false, "print edit decisions without encoding")
	emitTimeline := flag.String("emit-timeline", "", "write the v1 timeline JSON to <path> as a first-class artifact (works in dry-run too)")
	keepTemp := flag.Bool("keep-temp", false, "keep temp XML/timeline/segment files")
	verbose := flag.Bool("verbose", false, "show progress on stderr")

	input := parsePositional()
	if input == "" {
		beckyio.Fatalf("usage: becky-cut <input> [options]")
	}
	if _, err := os.Stat(input); err != nil {
		beckyio.Fatalf("input not found: %s", input)
	}

	cfg := config.Load()
	vcodec := cfg.Codec
	if *codec != "" {
		vcodec = *codec
	}

	stem := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
	dir := filepath.Dir(input)
	outputPath := *out
	if outputPath == "" {
		if *export == "kdenlive" {
			outputPath = filepath.Join(dir, stem+"_ALTERED.kdenlive")
		} else {
			outputPath = filepath.Join(dir, stem+"_edited.mp4")
		}
	}

	info, err := mediainfo.Probe(cfg.FFprobe, input)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}

	// Kdenlive export: hand off to auto-editor's own exporter (VAD post-pass is
	// only applied for mp4 rendering).
	if *export == "kdenlive" {
		beckyio.Logf(*verbose, "auto-editor kdenlive export (margin=%s)...", *margin)
		if err := runStream(*verbose, cfg.AutoEditor, input,
			"--edit", "audio", "--margin", *margin, "--export", "kdenlive",
			"-o", outputPath, "--progress", "none"); err != nil {
			beckyio.Fatalf("kdenlive export failed: %v", err)
		}
		beckyio.PrintJSON(map[string]any{
			"input": input, "output": outputPath, "export": "kdenlive",
			"vad_applied": false, "rendered": true,
		})
		return
	}

	// Step 1: auto-editor native detection -> Premiere XML.
	premiere := filepath.Join(os.TempDir(), fmt.Sprintf("becky_cut_premiere_%d.xml", os.Getpid()))
	beckyio.Logf(*verbose, "auto-editor detection (margin=%s)...", *margin)
	if err := runStream(*verbose, cfg.AutoEditor, input,
		"--edit", "audio", "--margin", *margin, "--export", "premiere",
		"-o", premiere, "--progress", "none"); err != nil {
		beckyio.Fatalf("auto-editor failed: %v", err)
	}
	if !*keepTemp {
		defer os.Remove(premiere)
	}

	fpsXML, segments, err := parsePremiere(premiere)
	if err != nil {
		beckyio.Fatalf("parse premiere xml: %v", err)
	}
	segments = dedupeSort(segments)
	if len(segments) == 0 {
		beckyio.Fatalf("no keep segments found in auto-editor output")
	}

	fps := fpsXML
	if fps <= 0 {
		fps = info.FPS
	}
	if fps <= 0 {
		fps = 30
	}

	srcFrames := int(math.Round(info.Duration * fps))
	chunks := buildChunks(segments, srcFrames)
	beckyio.Logf(*verbose, "fps=%.3f, %d keep segments, %d chunks", fps, len(segments), len(chunks))

	// Step 2: VAD post-pass filter (Silero via sherpa-onnx).
	removed := 0
	vadApplied := false
	if !*noVAD {
		switch {
		case cfg.SileroVADModel == "" || !fileExists(cfg.SileroVADModel):
			beckyio.Logf(true, "warning: silero_vad.onnx not found (%q); skipping VAD post-pass", cfg.SileroVADModel)
		default:
			vadScript, mErr := pyhelpers.Materialize("vad_silero.py", pyhelpers.VADSilero)
			if mErr != nil {
				beckyio.Logf(true, "warning: could not materialize VAD helper (%v); skipping VAD post-pass", mErr)
				break
			}
			vadApplied = true
			beckyio.Logf(*verbose, "VAD post-pass (threshold=%.2f, min_speech=%.0f%%)...", *vadThreshold, *vadSpeechPct)
			removed = vadPass(cfg, vadScript, input, fps, chunks, *vadThreshold, *vadSpeechPct, *keepTemp, *verbose)
			beckyio.Logf(*verbose, "VAD removed %d non-speech segments", removed)
		}
	}

	report := map[string]any{
		"input":          input,
		"output":         outputPath,
		"export":         "mp4",
		"codec":          vcodec,
		"fps":            round3(fps),
		"duration":       round3(info.Duration),
		"keep_segments":  len(segments),
		"total_chunks":   len(chunks),
		"removed_by_vad": removed,
		"vad_applied":    vadApplied,
	}

	// --emit-timeline: write the v1 timeline JSON to a caller-chosen path as a
	// first-class artifact (resolved to absolute) so becky-export can consume it
	// without depending on a temp file kept only by --keep-temp. Works in both
	// dry-run and normal render modes — the timeline is computed without rendering.
	srcAbs := mustAbs(input)
	if *emitTimeline != "" {
		emitPath := mustAbs(*emitTimeline)
		if err := writeTimeline(emitPath, srcAbs, chunks); err != nil {
			beckyio.Fatalf("emit timeline: %v", err)
		}
		report["timeline"] = emitPath
		beckyio.Logf(*verbose, "emitted timeline -> %s", emitPath)
	}

	if *dryRun {
		report["rendered"] = false
		report["decisions"] = decisions(chunks, fps)
		beckyio.PrintJSON(report)
		return
	}

	// Step 3: render the modified v1 timeline through auto-editor.
	tlPath := filepath.Join(os.TempDir(), fmt.Sprintf("becky_cut_timeline_%d.json", os.Getpid()))
	if err := writeTimeline(tlPath, srcAbs, chunks); err != nil {
		beckyio.Fatalf("write timeline: %v", err)
	}
	if !*keepTemp {
		defer os.Remove(tlPath)
	}
	beckyio.Logf(*verbose, "rendering timeline with %s...", vcodec)
	if err := runStream(*verbose, cfg.AutoEditor, tlPath,
		"-o", mustAbs(outputPath), "--progress", "none", "-c:v", vcodec); err != nil {
		beckyio.Fatalf("render failed: %v", err)
	}

	report["rendered"] = true
	if fi, e := os.Stat(outputPath); e == nil {
		report["output_mb"] = round3(float64(fi.Size()) / (1024 * 1024))
	}
	beckyio.PrintJSON(report)
}

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

// parsePremiere streams the FCP7/Premiere XML and returns the sequence fps and
// the [in,out] source-frame spans of each clipitem (the "keep" segments).
func parsePremiere(path string) (float64, [][2]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, nil, err
	}
	defer f.Close()

	dec := xml.NewDecoder(f)
	var stack []string
	var segs [][2]int
	var timebase float64
	var ntsc string
	gotTimebase, gotNtsc := false, false
	inClip := false
	var inVal, outVal *int
	field := "" // which leaf's CharData we are currently capturing

	for {
		tok, e := dec.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return 0, nil, e
		}
		switch t := tok.(type) {
		case xml.StartElement:
			parent := top(stack)
			stack = append(stack, t.Name.Local)
			switch t.Name.Local {
			case "clipitem":
				inClip = true
				inVal, outVal = nil, nil
			case "in":
				if inClip && parent == "clipitem" {
					field = "in"
				}
			case "out":
				if inClip && parent == "clipitem" {
					field = "out"
				}
			case "timebase":
				if !gotTimebase {
					field = "timebase"
				}
			case "ntsc":
				if !gotNtsc {
					field = "ntsc"
				}
			}
		case xml.CharData:
			if field == "" {
				break
			}
			s := strings.TrimSpace(string(t))
			if s == "" {
				break
			}
			switch field {
			case "in":
				if v, e := strconv.ParseFloat(s, 64); e == nil {
					iv := int(v)
					inVal = &iv
				}
			case "out":
				if v, e := strconv.ParseFloat(s, 64); e == nil {
					ov := int(v)
					outVal = &ov
				}
			case "timebase":
				if v, e := strconv.ParseFloat(s, 64); e == nil {
					timebase = v
					gotTimebase = true
				}
			case "ntsc":
				ntsc = strings.ToUpper(s)
				gotNtsc = true
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			switch t.Name.Local {
			case "in", "out", "timebase", "ntsc":
				field = ""
			case "clipitem":
				if inVal != nil && outVal != nil && *outVal > *inVal {
					segs = append(segs, [2]int{*inVal, *outVal})
				}
				inClip = false
			}
		}
	}

	fps := timebase
	if ntsc == "TRUE" && timebase > 0 {
		fps = timebase * 1000.0 / 1001.0
	}
	return fps, segs, nil
}

// dedupeSort removes duplicate spans (Premiere XML repeats them across the video
// and audio tracks) and orders them by start frame.
func dedupeSort(segs [][2]int) [][2]int {
	seen := make(map[[2]int]bool, len(segs))
	var uniq [][2]int
	for _, s := range segs {
		if !seen[s] {
			seen[s] = true
			uniq = append(uniq, s)
		}
	}
	sort.Slice(uniq, func(i, j int) bool { return uniq[i][0] < uniq[j][0] })
	return uniq
}

// buildChunks turns keep segments into a full v1 timeline, inserting cut spans
// for the gaps before, between, and after the keeps.
func buildChunks(segments [][2]int, srcFrames int) []chunk {
	var chunks []chunk
	if segments[0][0] > 0 {
		chunks = append(chunks, chunk{0, segments[0][0], cutSpeed})
	}
	for i, s := range segments {
		chunks = append(chunks, chunk{s[0], s[1], keepSpeed})
		if i < len(segments)-1 {
			next := segments[i+1][0]
			if next > s[1] {
				chunks = append(chunks, chunk{s[1], next, cutSpeed})
			}
		}
	}
	last := segments[len(segments)-1][1]
	if srcFrames > last {
		chunks = append(chunks, chunk{last, srcFrames, cutSpeed})
	}
	return chunks
}

// vadPass extracts each keep segment's audio, runs Silero VAD on it, and flips
// segments whose speech percentage is below minPct to cuts. Returns the count.
func vadPass(cfg config.Config, vadScript, input string, fps float64, chunks []chunk, threshold, minPct float64, keepTemp, verbose bool) int {
	removed := 0
	for i := range chunks {
		if chunks[i].Speed != keepSpeed {
			continue
		}
		startSec := float64(chunks[i].Start) / fps
		endSec := float64(chunks[i].End) / fps
		if endSec-startSec < minVADSeconds {
			continue
		}
		seg := filepath.Join(os.TempDir(), fmt.Sprintf("becky_cut_seg_%d_%d.wav", os.Getpid(), i))
		if !extractSegment(cfg.FFmpeg, input, seg, startSec, endSec) {
			continue
		}
		speechPct, err := runVAD(cfg.Python, vadScript, cfg.SileroVADModel, seg, threshold, i)
		if !keepTemp {
			os.Remove(seg)
		}
		if err != nil {
			beckyio.Logf(verbose, "  segment %d: VAD error (%v) — kept", i, err)
			continue
		}
		if speechPct < minPct {
			chunks[i].Speed = cutSpeed
			removed++
			beckyio.Logf(verbose, "  segment %d: %.1fs-%.1fs %.0f%% speech -> CUT", i, startSec, endSec, speechPct)
		} else {
			beckyio.Logf(verbose, "  segment %d: %.1fs-%.1fs %.0f%% speech -> keep", i, startSec, endSec, speechPct)
		}
	}
	return removed
}

func extractSegment(ffmpeg, input, outPath string, startSec, endSec float64) bool {
	dur := endSec - startSec
	if dur <= 0 {
		return false
	}
	cmd := exec.Command(ffmpeg, "-y",
		"-ss", fmt.Sprintf("%.3f", startSec), "-i", input,
		"-t", fmt.Sprintf("%.3f", dur),
		"-vn", "-ar", "16000", "-ac", "1", "-acodec", "pcm_s16le",
		"-loglevel", "error", outPath)
	cmd.Run() // tolerate per-segment failures, like the proven wrapper
	return fileExists(outPath)
}

// vadResult mirrors vad_silero.py's stdout for a single segment.
type vadResult struct {
	Skipped   bool     `json:"skipped"`
	Reason    string   `json:"reason"`
	SpeechPct *float64 `json:"speech_pct"`
}

// runVAD runs the sherpa-onnx Silero helper on one segment WAV and returns the
// percentage of that segment that is speech.
func runVAD(python, script, model, audio string, threshold float64, idx int) (float64, error) {
	outFile := filepath.Join(os.TempDir(), fmt.Sprintf("becky_vad_%d_%d.json", os.Getpid(), idx))
	defer os.Remove(outFile)
	cmd := exec.Command(python, script,
		audio,
		"--model", model,
		"--threshold", fmt.Sprintf("%.3f", threshold),
		"--output", outFile)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("%v: %s", err, tail(errBuf.String()))
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		return 0, err
	}
	var res vadResult
	if err := json.Unmarshal(data, &res); err != nil {
		return 0, fmt.Errorf("unexpected VAD output: %s", tail(string(data)))
	}
	if res.Skipped {
		return 0, fmt.Errorf("vad skipped: %s", res.Reason)
	}
	if res.SpeechPct == nil {
		return 0, fmt.Errorf("vad output missing speech_pct")
	}
	return *res.SpeechPct, nil
}

// writeTimeline serializes chunks as an auto-editor v1 timeline JSON.
func writeTimeline(path, source string, chunks []chunk) error {
	arr := make([][]any, 0, len(chunks))
	for _, c := range chunks {
		arr = append(arr, []any{c.Start, c.End, c.Speed})
	}
	tl := map[string]any{"version": "1", "source": source, "chunks": arr}
	b, err := json.Marshal(tl)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func decisions(chunks []chunk, fps float64) []map[string]any {
	d := make([]map[string]any, 0, len(chunks))
	for _, c := range chunks {
		status := "keep"
		if c.Speed != keepSpeed {
			status = "cut"
		}
		d = append(d, map[string]any{
			"status": status,
			"start":  round3(float64(c.Start) / fps),
			"end":    round3(float64(c.End) / fps),
		})
	}
	return d
}

func runStream(verbose bool, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	var errBuf strings.Builder
	if verbose {
		cmd.Stderr = io.MultiWriter(os.Stderr, &errBuf)
	} else {
		cmd.Stderr = &errBuf
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %v\n%s", filepath.Base(name), err, tail(errBuf.String()))
	}
	return nil
}

func top(stack []string) string {
	if len(stack) == 0 {
		return ""
	}
	return stack[len(stack)-1]
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func mustAbs(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func round3(f float64) float64 { return float64(int(f*1000+0.5)) / 1000 }

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 800 {
		return s[len(s)-800:]
	}
	return s
}
