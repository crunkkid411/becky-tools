// becky-diarize — speaker diarization via sherpa-onnx (pyannote-seg-3.0 + CAM++).
//
//	becky-diarize <input> [--output f] [--format json|srt|txt]
//	              [--min-speakers N] [--max-speakers N] [--device cpu|cuda]
//	              [--threshold 0.5] [--keep-temp] [--verbose]
//
// Takes an audio OR video file, extracts 16 kHz mono PCM, runs sherpa-onnx
// offline diarization, and emits speaker-labeled segments grouped by speaker.
// Speaker count auto-detects (num_clusters=-1 + cosine threshold) unless
// --min/--max-speakers pin it. No LLM; deterministic. JSON to stdout (or
// --output); diagnostics to stderr; exit 0 on success.
//
// HARDENED OVER-SPLIT RULES (diarization is the #1 blunder source; these are the
// strict rules, verified on real clips — do not loosen without re-running the suite):
//
//	R1. VAD speech-gating ON (auto mode): diarize ONLY Silero speech regions, so
//	    music / intro stings / SFX never get embedded by CAM++ as phantom speakers.
//	    (Lives in diarize_sherpa.py; the Go side passes --vad-model.)
//	R2. Clustering threshold 0.7 (not the sherpa default): a higher cosine threshold
//	    refuses to split one talker's natural timbre variation into two clusters.
//	R3. Outlier-merge floor --min-speaker-frac 0.15 (HARDENED from 0.10): a cluster
//	    holding <15% of total speech is merged into the nearest real speaker. On real
//	    footage a brief cross-talk / background voice forms a ~13% spurious THIRD
//	    cluster that 0.10 let through (2-speakers-test.mp4 came back as 3). At 0.15 it
//	    merges, while genuine speakers (which each hold 20%+ on the real 2-speaker
//	    clips) both survive. --min-speaker-duration 1.5s is the absolute-time partner.
//
// Verified counts (real clips, 2026-06-08): single-speaker monologue -> 1; the
// 2-speaker clips (2-speakers-test.mp4, the Jordan+Shelby contact clip,
// different-person-test.mp4) -> 2. identify's internal diarization (cmd/identify/
// diarize.go) passes the SAME 0.15 floor so the two tools agree on speaker count.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/mediainfo"
	"becky-go/internal/pyhelpers"
)

// flatSegment is one (start, end, speaker) span as the Python helper emits it.
type flatSegment struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker string  `json:"speaker"`
}

// helperResult mirrors diarize_sherpa.py's stdout.
type helperResult struct {
	Skipped     bool          `json:"skipped"`
	Reason      string        `json:"reason"`
	Duration    float64       `json:"duration"`
	SampleRate  int           `json:"sample_rate"`
	NumSpeakers int           `json:"num_speakers"`
	Segments    []flatSegment `json:"segments"`
}

// Segment is one speaker-labeled span in the output schema.
type Segment struct {
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
	Confidence float64 `json:"confidence"`
}

// Speaker groups all segments attributed to one cluster.
type Speaker struct {
	ID       string    `json:"id"`
	Segments []Segment `json:"segments"`
}

// Output is the becky-diarize JSON contract.
type Output struct {
	File     string    `json:"file"`
	Duration float64   `json:"duration"`
	Speakers []Speaker `json:"speakers"`
}

// sherpa-onnx exposes no per-segment posterior, so we attach a fixed confidence.
// Documented choice: clustered diarization output is a hard assignment, not a
// probability — 1.0 signals "assigned" rather than a calibrated score.
const segmentConfidence = 1.0

func main() {
	out := flag.String("output", "", "output file (default: stdout)")
	format := flag.String("format", "json", "output format: json, srt, txt")
	minSpeakers := flag.Int("min-speakers", 1, "minimum number of speakers")
	maxSpeakers := flag.Int("max-speakers", 0, "maximum number of speakers (0 = auto)")
	device := flag.String("device", "", "device: cpu, cuda (default from config)")
	threshold := flag.Float64("threshold", 0.7, "clustering cosine threshold (auto mode)")
	// minSpeakerFrac: auto-mode outlier-merge floor. A cluster holding less than this
	// fraction of total speech is merged into the nearest real speaker. HARDENED to
	// 0.15 (was the helper's 0.10 default): on real footage a brief cross-talk /
	// background voice forms a ~13% spurious third cluster that 0.10 let survive
	// (2-speakers-test came back as 3). 0.15 merges it while genuine speakers — which
	// hold 20%+ of the speech each on the real 2-speaker clips — both survive. Tunable
	// for clips with a real, sparse third speaker.
	minSpeakerFrac := flag.Float64("min-speaker-frac", 0.15,
		"auto mode: merge clusters below this fraction of total speech (hardened over-split guard)")
	minSpeakerDur := flag.Float64("min-speaker-duration", 1.5,
		"auto mode: merge clusters with less than this many seconds of total speech")
	numThreads := flag.Int("num-threads", 4, "ONNX inference threads")
	keepTemp := flag.Bool("keep-temp", false, "keep the extracted temp WAV")
	verbose := flag.Bool("verbose", false, "show progress on stderr")

	input := parsePositional()
	if input == "" {
		beckyio.Fatalf("usage: becky-diarize <input> [options]")
	}
	if _, err := os.Stat(input); err != nil {
		beckyio.Fatalf("input not found: %s", input)
	}

	cfg := config.Load()
	dev := cfg.Device
	if *device != "" {
		dev = *device
	}
	if cfg.DiarSegModel == "" || !fileExists(cfg.DiarSegModel) {
		beckyio.Fatalf("segmentation model not found: %q", cfg.DiarSegModel)
	}
	if cfg.SpeakerEmbModel == "" || !fileExists(cfg.SpeakerEmbModel) {
		beckyio.Fatalf("speaker embedding model not found: %q", cfg.SpeakerEmbModel)
	}

	info, err := mediainfo.Probe(cfg.FFprobe, input)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	if !info.HasAudio {
		beckyio.Fatalf("input has no audio stream: %s", input)
	}

	beckyio.Logf(*verbose, "extracting 16kHz mono audio with ffmpeg...")
	wav, err := extractAudio(cfg.FFmpeg, input)
	if err != nil {
		beckyio.Fatalf("audio extraction failed: %v", err)
	}
	if !*keepTemp {
		defer os.Remove(wav)
	}

	script, err := pyhelpers.Materialize("diarize_sherpa.py", pyhelpers.DiarizeSherpa)
	if err != nil {
		beckyio.Fatalf("materialize helper: %v", err)
	}

	numClusters := resolveNumClusters(*minSpeakers, *maxSpeakers)
	beckyio.Logf(*verbose, "running diarization (device=%s, num_clusters=%d, threshold=%.2f, min-speaker-frac=%.2f)...",
		dev, numClusters, *threshold, *minSpeakerFrac)
	res, err := runHelper(cfg, script, wav, dev, numClusters, *threshold, *minSpeakerFrac, *minSpeakerDur, *numThreads, *verbose)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	if res.Skipped {
		beckyio.Fatalf("diarization skipped: %s", res.Reason)
	}

	duration := res.Duration
	if duration <= 0 {
		duration = info.Duration
	}
	output := Output{
		File:     input,
		Duration: round3(duration),
		Speakers: groupBySpeaker(res.Segments),
	}
	beckyio.Logf(*verbose, "%d speaker(s), %d total segments", len(output.Speakers), len(res.Segments))

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
// non-flag token, so this enables `becky-diarize in.mp4 --verbose`).
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

// resolveNumClusters maps --min/--max-speakers onto sherpa's num_clusters knob.
// -1 means auto-detect (cosine threshold decides). When both bounds agree on a
// single count (or only a min>1 is given), we pin that count; otherwise we stay
// in auto mode and let the threshold pick.
func resolveNumClusters(minSpk, maxSpk int) int {
	if maxSpk > 0 && minSpk == maxSpk {
		return maxSpk
	}
	if minSpk > 1 && maxSpk == 0 {
		return minSpk
	}
	return -1
}

func extractAudio(ffmpeg, input string) (string, error) {
	tmp, err := os.CreateTemp("", "becky_diar_*.wav")
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

func runHelper(cfg config.Config, script, wav, device string, numClusters int, threshold, minSpeakerFrac, minSpeakerDur float64, numThreads int, verbose bool) (helperResult, error) {
	args := []string{script, wav,
		"--seg-model", cfg.DiarSegModel,
		"--embedding-model", cfg.SpeakerEmbModel,
		"--num-clusters", fmt.Sprintf("%d", numClusters),
		"--threshold", fmt.Sprintf("%.3f", threshold),
		"--min-speaker-frac", fmt.Sprintf("%.3f", minSpeakerFrac),
		"--min-speaker-duration", fmt.Sprintf("%.3f", minSpeakerDur),
		"--num-threads", fmt.Sprintf("%d", numThreads),
		"--device", device}
	// Pass the Silero model so the helper gates diarization to speech-only
	// regions (default ON in auto mode): this strips music / intro stings / SFX
	// that CAM++ would otherwise embed as phantom speakers on social-media
	// footage (the "single talker -> 5 speakers" bug). Helper enables gating
	// whenever a vad-model is supplied.
	if cfg.SileroVADModel != "" && fileExists(cfg.SileroVADModel) {
		args = append(args, "--vad-model", cfg.SileroVADModel)
	}
	if verbose {
		args = append(args, "--verbose")
	}
	cmd := exec.Command(cfg.Python, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	if verbose {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = &stderr
	}
	if err := cmd.Run(); err != nil {
		return helperResult{}, fmt.Errorf("diarize helper failed: %v\n%s", err, tail(stderr.String()))
	}
	res, ok := parseHelperJSON(stdout.String())
	if !ok {
		return helperResult{}, fmt.Errorf("could not parse diarize helper output:\n%s", tail(stdout.String()))
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
	if json.Unmarshal([]byte(s), &r) == nil && (r.Skipped || r.Segments != nil || r.SampleRate > 0) {
		return r, true
	}
	return helperResult{}, false
}

// groupBySpeaker turns the flat (start,end,speaker) list into the schema's
// speakers[] array, ordered by speaker id, each speaker's segments by start.
func groupBySpeaker(flat []flatSegment) []Speaker {
	bySpeaker := make(map[string][]Segment)
	var order []string
	for _, f := range flat {
		if _, seen := bySpeaker[f.Speaker]; !seen {
			order = append(order, f.Speaker)
		}
		bySpeaker[f.Speaker] = append(bySpeaker[f.Speaker], Segment{
			Start:      round3(f.Start),
			End:        round3(f.End),
			Confidence: segmentConfidence,
		})
	}
	sort.Strings(order)
	speakers := make([]Speaker, 0, len(order))
	for _, id := range order {
		segs := bySpeaker[id]
		sort.Slice(segs, func(i, j int) bool { return segs[i].Start < segs[j].Start })
		speakers = append(speakers, Speaker{ID: id, Segments: segs})
	}
	return speakers
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
		return renderTxt(o), nil
	case "srt":
		return renderSRT(o), nil
	default:
		return "", fmt.Errorf("unknown format: %s (use json, srt, txt)", format)
	}
}

// timed pairs a flattened segment with its speaker for chronological rendering.
type timed struct {
	start   float64
	end     float64
	speaker string
}

// flatten merges all speakers' segments back into one start-ordered timeline,
// used by the srt/txt renderers.
func flatten(o Output) []timed {
	var all []timed
	for _, sp := range o.Speakers {
		for _, s := range sp.Segments {
			all = append(all, timed{start: s.Start, end: s.End, speaker: sp.ID})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].start < all[j].start })
	return all
}

func renderTxt(o Output) string {
	var b strings.Builder
	for _, t := range flatten(o) {
		fmt.Fprintf(&b, "[%s --> %s] %s\n", srtTime(t.start), srtTime(t.end), t.speaker)
	}
	return b.String()
}

func renderSRT(o Output) string {
	var b strings.Builder
	for i, t := range flatten(o) {
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, srtTime(t.start), srtTime(t.end), t.speaker)
	}
	return b.String()
}

func srtTime(sec float64) string {
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
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

func round3(f float64) float64 {
	return float64(int(f*1000+0.5)) / 1000
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 800 {
		return s[len(s)-800:]
	}
	return s
}
