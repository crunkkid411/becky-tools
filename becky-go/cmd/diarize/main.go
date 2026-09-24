// becky-diarize — speaker diarization with NVIDIA Nemotron-3-Diarization.
//
//	becky-diarize <input> [--output f] [--format json|srt|txt]
//	              [--min-speakers N] [--max-speakers N] [--device cpu|cuda]
//	              [--keep-temp] [--verbose]
//
// Takes an audio OR video file, extracts 16 kHz mono PCM, runs
// nvidia/Nemotron-3-Diarization (end-to-end streaming Sortformer, up to 8 speakers)
// through NeMo-Speech.cpp's native nemo-speech.exe, and emits speaker-labeled
// segments grouped by speaker. No LLM; deterministic. JSON to stdout (or --output);
// diagnostics to stderr; exit 0 on success.
//
// ENGINE SWAP (2026-09-24). This tool used to run sherpa-onnx (pyannote-seg-3.0 +
// CAM++ + clustering, pyhelpers/diarize_sherpa.py). Jordan: it "failed
// catastrophically" on real footage. Nemotron decides WHO speaks and HOW MANY people
// there are in one model, so the sherpa-era over-split guards (VAD gating, clustering
// --threshold, --min-speaker-frac / --min-speaker-duration) no longer apply. Those
// flags are still ACCEPTED so older callers don't break, and do nothing (--device as well).
// diarize_sherpa.py stays in the repo; becky-identify still uses it.
//
// Runtime: native C++ plus a 107 MB q8_0 GGUF (models\diar\), no Python or torch.
// The build there is CPU-only (upstream hard-codes 4 threads), so diarization never
// takes VRAM from the shared 8 GB GPU. Geometry is the model card's own benchmarked
// "very high latency (offline)" setting (DIHARD III DER 12.73); segment thresholds
// are NeMo-Speech.cpp's defaults for this checkpoint.
//
// Speaker ids are SPEAKER_00, SPEAKER_01, ... in order of FIRST appearance, with no gaps
// (renumber). Nemotron can mark two voices active at once, so segments of different
// speakers may overlap. --max-speakers N caps the count: when the model
// hears more voices than the caller says exist, the ones with the least speech are
// merged into whichever kept speaker talks nearest in time. The model cannot be
// forced UP to --min-speakers.
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
	"becky-go/internal/proc"
)

// modelID names the engine in the output so a reader knows what labelled the speech.
const modelID = "nvidia/Nemotron-3-Diarization"

// nemotronGeometry is the model card's "very high latency (offline)" configuration, in
// 80 ms encoder frames: speaker cache 264, FIFO 40, chunk 340, right context 40, cache
// update every 300 (a 30.4 s input buffer). Left context stays 0, as the checkpoint's
// own streaming config has it.
var nemotronGeometry = []string{
	"--diar-spkcache", "264", "--diar-fifo", "40", "--diar-chunk", "340",
	"--diar-rc", "40", "--diar-update-period", "300",
}

// flatSegment is one (start, end, speaker) span.
type flatSegment struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker string  `json:"speaker"`
}

// Segment is one speaker-labeled span in the output schema.
type Segment struct {
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
	Confidence float64 `json:"confidence"`
}

// Speaker groups all segments attributed to one voice.
type Speaker struct {
	ID       string    `json:"id"`
	Segments []Segment `json:"segments"`
}

// Output is the becky-diarize JSON contract. Model was added 2026-09-24; the rest is unchanged.
type Output struct {
	File     string    `json:"file"`
	Duration float64   `json:"duration"`
	Model    string    `json:"model,omitempty"`
	Speakers []Speaker `json:"speakers"`
}

// nemo-speech prints hard segments, not per-segment probabilities, so every segment
// carries a fixed confidence. Documented choice: 1.0 means "assigned", not a
// calibrated score.
const segmentConfidence = 1.0

func main() {
	out := flag.String("output", "", "output file (default: stdout)")
	format := flag.String("format", "json", "output format: json, srt, txt")
	minSpeakers := flag.Int("min-speakers", 1, "minimum number of speakers (informational: the model cannot be forced up)")
	maxSpeakers := flag.Int("max-speakers", 0, "maximum number of speakers (0 = no cap; extra voices merge into the nearest kept speaker)")
	// Sherpa-era knobs, kept so older callers don't fail on an unknown flag. Nemotron needs none of them.
	// --device too: nemo-speech exits 2 on "--device cuda" when built without CUDA, and the build is
	// CPU-only on purpose (the GPU is shared), so it always runs on the backend it was built with.
	_ = flag.String("device", "", "ignored (nemo-speech runs on the backend it was built with: CPU here)")
	_ = flag.Float64("threshold", 0.7, "ignored (sherpa-era clustering knob)")
	_ = flag.Float64("min-speaker-frac", 0.15, "ignored (sherpa-era outlier-merge knob)")
	_ = flag.Float64("min-speaker-duration", 1.5, "ignored (sherpa-era outlier-merge knob)")
	_ = flag.Int("num-threads", 4, "ignored (nemo-speech uses 4 CPU threads)")
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
	if !fileExists(cfg.NemoSpeech) {
		beckyio.Fatalf("diarization runtime not found: %q (set it up with scripts\\get-nemotron-diar.ps1)", cfg.NemoSpeech)
	}
	if !fileExists(cfg.DiarModel) {
		beckyio.Fatalf("diarization model not found: %q (set it up with scripts\\get-nemotron-diar.ps1)", cfg.DiarModel)
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

	beckyio.Logf(*verbose, "running %s (nemo-speech)...", modelID)
	segs, err := runNemo(cfg, wav, *verbose)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	if n := countSpeakers(segs); *maxSpeakers > 0 && n > *maxSpeakers {
		beckyio.Logf(*verbose, "model heard %d voices; caller says at most %d, merging the smallest", n, *maxSpeakers)
		segs = capSpeakers(segs, *maxSpeakers)
	}
	if n := countSpeakers(segs); *minSpeakers > 1 && n < *minSpeakers {
		beckyio.Logf(*verbose, "model heard %d voice(s); caller expected at least %d", n, *minSpeakers)
	}
	segs = renumber(segs)

	output := Output{
		File:     input,
		Duration: round3(info.Duration),
		Model:    modelID,
		Speakers: groupBySpeaker(segs),
	}
	beckyio.Logf(*verbose, "%d speaker(s), %d total segments", len(output.Speakers), len(segs))

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
	proc.NoWindow(cmd)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("ffmpeg: %v\n%s", err, tail(errBuf.String()))
	}
	return path, nil
}

// runNemo runs `nemo-speech diarize <wav> --format json` and returns its segments.
func runNemo(cfg config.Config, wav string, verbose bool) ([]flatSegment, error) {
	args := append([]string{"diarize", wav, "--model", cfg.DiarModel, "--format", "json"}, nemotronGeometry...)
	cmd := exec.Command(cfg.NemoSpeech, args...)
	proc.NoWindow(cmd)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	if verbose {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = &stderr
	}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("nemo-speech diarize failed: %v\n%s", err, tail(stderr.String()))
	}
	return parseNemoJSON(stdout.String())
}

// parseNemoJSON reads nemo-speech's {"file","segments":[{"start","end","speaker"}]} where
// speaker is 1-based in arrival order, and names speakers SPEAKER_00, SPEAKER_01, ... It
// skips anything printed before the JSON object.
func parseNemoJSON(s string) ([]flatSegment, error) {
	i := strings.Index(s, "{")
	if i < 0 {
		return nil, fmt.Errorf("nemo-speech printed no JSON:\n%s", tail(s))
	}
	var v struct {
		Segments []struct {
			Start   float64 `json:"start"`
			End     float64 `json:"end"`
			Speaker int     `json:"speaker"`
		} `json:"segments"`
	}
	if err := json.Unmarshal([]byte(s[i:]), &v); err != nil {
		return nil, fmt.Errorf("could not read nemo-speech output: %v\n%s", err, tail(s))
	}
	segs := make([]flatSegment, 0, len(v.Segments))
	for _, g := range v.Segments {
		if g.End <= g.Start || g.Speaker < 1 {
			continue
		}
		segs = append(segs, flatSegment{Start: g.Start, End: g.End, Speaker: fmt.Sprintf("SPEAKER_%02d", g.Speaker-1)})
	}
	return segs, nil
}

func countSpeakers(segs []flatSegment) int {
	seen := map[string]bool{}
	for _, s := range segs {
		seen[s.Speaker] = true
	}
	return len(seen)
}

// capSpeakers keeps the max speakers with the most total speech and hands every segment of the
// others to the kept speaker whose segment midpoint is nearest in time.
// ponytail: nearest-in-time is a heuristic; a voice-embedding match would pick the right person
// when two kept speakers talk equally close by. Add it if a caller-pinned count ever mislabels.
func capSpeakers(segs []flatSegment, max int) []flatSegment {
	talk := map[string]float64{}
	for _, s := range segs {
		talk[s.Speaker] += s.End - s.Start
	}
	if max < 1 || len(talk) <= max {
		return segs
	}
	ids := make([]string, 0, len(talk))
	for id := range talk {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if talk[ids[i]] != talk[ids[j]] {
			return talk[ids[i]] > talk[ids[j]]
		}
		return ids[i] < ids[j]
	})
	keep := map[string]bool{}
	for _, id := range ids[:max] {
		keep[id] = true
	}
	out := make([]flatSegment, len(segs))
	for i, s := range segs {
		out[i] = s
		if keep[s.Speaker] {
			continue
		}
		mid, best := (s.Start+s.End)/2, -1.0
		for _, k := range segs {
			if !keep[k.Speaker] {
				continue
			}
			if d := abs((k.Start+k.End)/2 - mid); best < 0 || d < best {
				out[i].Speaker, best = k.Speaker, d
			}
		}
	}
	return out
}

// renumber names speakers SPEAKER_00, SPEAKER_01, ... in order of first appearance, with no gaps.
// The model can open a speaker slot that post-processing then drops entirely (festival clip: slots
// 1 and 3 kept, 2 gone), and capSpeakers leaves holes too; callers expect a contiguous set.
func renumber(segs []flatSegment) []flatSegment {
	out := append([]flatSegment(nil), segs...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	ids := map[string]string{}
	for i := range out {
		id, ok := ids[out[i].Speaker]
		if !ok {
			id = fmt.Sprintf("SPEAKER_%02d", len(ids))
			ids[out[i].Speaker] = id
		}
		out[i].Speaker = id
	}
	return out
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

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
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
