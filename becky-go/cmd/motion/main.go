// becky-motion — zero-VRAM Phase-1 motion localizer for forensic video.
//
//	becky-motion <video> [options]
//
// becky-validate samples a clip at ~1 fps, so the model sees a slideshow of stills:
// a quick touch, a grab, a flinch — sub-second events — fall BETWEEN the samples and
// are lost. becky-motion fixes that. It scans the clip at its TRUE source fps with a
// deterministic dense frame-difference (no model, no GPU) and emits a JSON timeline
// of motion bursts {window_start, window_end, peak_time, motion_score, frame
// indices}. Each burst is the EXACT short window to point the slow descriptive model
// (becky-validate, via --window/WindowStart) at — instead of blind 1-fps sampling.
//
// Honesty (FORENSIC-OUTPUT-PHILOSOPHY.md): motion detection finds WHEN something
// moved at frame precision. It does NOT say WHAT moved or who did it. Every burst is
// a [CANDIDATE] window for review, carrying its measured motion score as the basis.
//
// Options:
//
//	--window <a-b>     analyze only [a,b] seconds (default: whole clip)
//	--fps <n>          dense sample fps (default: source fps, capped by --max-fps)
//	--max-fps <n>      cap on dense fps to bound work on huge clips (default 60)
//	--k <f>            adaptive sensitivity: threshold = median + k*MAD (default 6)
//	--min-motion <f>   fixed 0..1 threshold (overrides adaptive --k when > 0)
//	--min-frames <n>   minimum frames a burst must span (default 2)
//	--merge-gap <n>    merge bursts within this many calm frames (default 8)
//	--pad <n>          context frames padded onto each burst window (default 3)
//	--local-win <n>    rolling-baseline half-width in frames; 0 = global (default 150)
//	--device <cpu|cuda> CUDA decode is best-effort with CPU fallback (default config)
//	--output <file>    write JSON here instead of stdout
//	--verbose          progress to stderr
//
// JSON to stdout (or --output); diagnostics to stderr; exit 0 on success. Degrades
// gracefully: a clip with no video, or too few frames, emits valid JSON with a
// skipped/reason note and exits 0. The source video is only read, never modified.
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/mediainfo"
	"becky-go/internal/osintexport"
)

const toolVersion = "becky-motion v1.0.0"

// maxFPSDefault caps dense decode so a very long/high-fps clip can't blow up work;
// 60 covers every consumer phone (most are 30) while leaving slow-mo headroom.
const maxFPSDefault = 60.0

// absMotionFloor is the minimum RAW peak motion (mean-abs grayscale delta, 0..255
// units) a clip must show before any burst is reported. Measured on real footage:
// genuine movement peaks at 16+ units; a static clip's libx264 dithering peaks ~0.01.
// 0.5 cleanly separates the two, so a truly static clip yields zero bursts instead of
// having per-clip normalization amplify codec noise into a false detection.
const absMotionFloor = 0.5

// options holds the parsed CLI flags.
type options struct {
	winStart, winEnd float64
	fps, maxFPS      float64
	k, minMotion     float64
	minFrames        int
	mergeGap         int
	pad              int
	localWin         int
	device           string
	output           string
	verbose          bool
}

func (o options) burstParams() burstParams {
	p := defaultBurstParams()
	if o.k > 0 {
		p.K = o.k
	}
	if o.minMotion > 0 {
		p.FixedThresh = o.minMotion
	}
	if o.minFrames > 0 {
		p.MinFrames = o.minFrames
	}
	if o.mergeGap >= 0 {
		p.MergeGap = o.mergeGap
	}
	if o.pad >= 0 {
		p.PadFrames = o.pad
	}
	if o.localWin >= 0 {
		p.LocalWin = o.localWin
	}
	return p
}

func main() {
	opts, input := parseArgs()

	if _, err := os.Stat(input); err != nil {
		beckyio.Fatalf("input not found: %s", input)
	}

	cfg := config.Load()
	dev := cfg.Device
	if opts.device != "" {
		dev = opts.device
	}

	info, err := mediainfo.Probe(cfg.FFprobe, input)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}

	srcSHA, err := osintexport.SHA256File(input)
	if err != nil {
		beckyio.Fatalf("sha256 source: %v", err)
	}

	srcFPS := info.FPS
	if srcFPS <= 0 {
		srcFPS = 30 // ffprobe gave nothing usable; assume the phone-standard rate
	}

	// Window: default whole clip. Whole-clip dur passes 0 to ffmpeg (= to end).
	start := opts.winStart
	dur := 0.0
	winEnd := info.Duration
	if opts.winEnd > 0 && opts.winEnd > opts.winStart {
		dur = opts.winEnd - opts.winStart
		winEnd = opts.winEnd
	}

	base := baseOutput(input, srcSHA, srcFPS, info.Duration, [2]float64{round3(start), round3(winEnd)})

	// Graceful degrade: no video stream -> valid JSON, exit 0.
	if !info.HasVideo {
		base.Notes.Skipped = "motion detection"
		base.Notes.Reason = "input has no video stream"
		base.Method = "n/a"
		emitOrDie(base, opts.output)
		return
	}

	sampleFPS := chooseSampleFPS(srcFPS, opts.fps, opts.maxFPS)
	base.SampleFPS = round3(sampleFPS)

	beckyio.Logf(opts.verbose, "%s: %.2fs @ %.3f fps source; scanning [%.2f,%.2f]s at %.3f sample fps (device=%s)",
		input, info.Duration, srcFPS, start, winEnd, sampleFPS, dev)

	sig, err := motionSignal(cfg.FFmpeg, input, start, dur, sampleFPS, strings.EqualFold(dev, "cuda"), opts.verbose)
	if err != nil {
		// Decode/too-few-frames is a graceful skip, not a crash: emit valid JSON.
		base.Notes.Skipped = "motion detection"
		base.Notes.Reason = err.Error()
		base.Method = "dense frame-difference (failed)"
		beckyio.Logf(true, "warning: motion signal unavailable: %v", err)
		emitOrDie(base, opts.output)
		return
	}

	p := opts.burstParams()
	base.Method = "dense per-frame difference (mean abs grayscale delta) at true source fps; " + methodSummary(p)

	// Absolute-motion guard: a clip whose raw peak is below the floor is genuinely
	// static (only codec noise). Report zero bursts rather than let per-clip
	// normalization turn dithering into a false detection. This is the calm-clip
	// no-over-fire safeguard at the physical (raw-units) level.
	_, thrInfo := chooseThreshold(sig.Norm, p)
	base.Threshold = thrInfo
	if sig.RawPeak < absMotionFloor {
		base.Notes.Warning = fmt.Sprintf("no motion above the absolute floor (raw peak %.3f < %.1f units); clip is effectively static", sig.RawPeak, absMotionFloor)
		beckyio.Logf(opts.verbose, "raw peak %.3f below absolute floor %.1f; reporting 0 bursts (static clip)", sig.RawPeak, absMotionFloor)
		emitOrDie(base, opts.output)
		return
	}

	raw, _ := detectBursts(sig.Norm, p)
	bursts := buildBursts(raw, sig.Norm, p, sampleFPS, srcFPS, start)
	base.MotionBursts = bursts
	base.BurstCount = len(bursts)

	beckyio.Logf(opts.verbose, "%d motion burst(s) above threshold %.4f (baseline median %.4f, MAD %.4f, raw peak %.2f)",
		len(bursts), thrInfo.Value, thrInfo.BaselineMed, thrInfo.BaselineMAD, sig.RawPeak)

	emitOrDie(base, opts.output)
}

// buildBursts converts signal-index bursts into output Bursts with frame-precise
// timestamps, source frame indices, peak detection, padding, and a ready-to-use
// becky-validate hand-off. Signal index i is the transition INTO sampled frame i+1
// (the moment the motion is observed).
func buildBursts(raw []rawBurst, sig []float64, p burstParams, sampleFPS, srcFPS, winStart float64) []Burst {
	out := make([]Burst, 0, len(raw)) // [] not null when calm
	frameDur := 1.0 / sampleFPS
	for _, b := range raw {
		// Pad in signal space, clamped.
		s := clampInt(b.start-p.PadFrames, 0, len(sig)-1)
		e := clampInt(b.end+p.PadFrames, 0, len(sig)-1)

		peakIdx, peak, sum := s, 0.0, 0.0
		for i := s; i <= e; i++ {
			sum += sig[i]
			if sig[i] > peak {
				peak = sig[i]
				peakIdx = i
			}
		}
		n := e - s + 1

		startSec := winStart + float64(s+1)*frameDur
		endSec := winStart + float64(e+1)*frameDur
		peakSec := winStart + float64(peakIdx+1)*frameDur

		// Source frame indices at TRUE source fps (what a frame extractor needs).
		srcStart := int(math.Round(startSec * srcFPS))
		srcEnd := int(math.Round(endSec * srcFPS))
		srcPeak := int(math.Round(peakSec * srcFPS))

		durSec := endSec - startSec
		sub := durSec < 1.0
		between := math.Floor(startSec) == math.Floor(endSec) && sub // whole burst inside one 1-fps cell

		// Hand-off window for becky-validate: round outward with lead-in/out so the LLM
		// sees the approach; fps inside the short window can be generous (the spec notes
		// a short window affords 4-8 fps cheaply, far better than 1 fps clip-wide).
		vStart := math.Max(0, math.Floor(startSec*10)/10-0.3)
		vLen := math.Ceil((endSec-vStart)*10)/10 + 0.3
		validate := fmt.Sprintf("becky-validate <clip> --window %.1f --fps 6  # start at %.1fs", round1(vLen), round1(vStart))

		out = append(out, Burst{
			WindowStart:     round3(startSec),
			WindowEnd:       round3(endSec),
			PeakTime:        round3(peakSec),
			DurationSec:     round3(durSec),
			MotionScore:     round4(peak),
			MeanScore:       round4(sum / float64(n)),
			FrameIndexStart: srcStart,
			FrameIndexEnd:   srcEnd,
			FrameIndexPeak:  srcPeak,
			SubSecond:       sub,
			BetweenSamples:  between,
			RecommendReview: true,
			RouteTo:         "becky-validate",
			ValidateArgs:    validate,
		})
	}
	return out
}

func baseOutput(input, sha string, srcFPS, dur float64, win [2]float64) Output {
	return Output{
		Tool:           toolVersion,
		SourceFile:     input,
		SourceSHA256:   sha,
		SourceFPS:      round3(srcFPS),
		SampleFPS:      round3(srcFPS),
		DurationSec:    round3(dur),
		AnalyzedWindow: win,
		MotionBursts:   []Burst{},
		AnalyzedAt:     time.Now().UTC().Format(time.RFC3339),
		Notes:          Notes{Honesty: honestyNote},
	}
}

// chooseSampleFPS prefers the source fps (true temporal resolution) but honors an
// explicit --fps and a --max-fps cap so a long or slow-mo clip stays bounded.
func chooseSampleFPS(srcFPS, want, max float64) float64 {
	fps := srcFPS
	if want > 0 {
		fps = want
	}
	if max <= 0 {
		max = maxFPSDefault
	}
	if fps > max {
		fps = max
	}
	if fps <= 0 {
		fps = 30
	}
	return fps
}

func emitOrDie(o Output, outPath string) {
	if outPath == "" {
		beckyio.PrintJSON(o)
		return
	}
	b, err := marshalIndent(o)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	if err := os.WriteFile(outPath, b, 0o644); err != nil {
		beckyio.Fatalf("write output: %v", err)
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }
func round3(f float64) float64 { return math.Round(f*1000) / 1000 }

// parseArgs reads the positional <video> then re-parses trailing flags (Go's flag
// stops at the first non-flag token), mirroring the other becky tools.
func parseArgs() (options, string) {
	var o options
	win := flag.String("window", "", "analyze only A-B seconds, e.g. 10-25 (default: whole clip)")
	flag.Float64Var(&o.fps, "fps", 0, "dense sample fps (default: source fps, capped by --max-fps)")
	flag.Float64Var(&o.maxFPS, "max-fps", maxFPSDefault, "cap on dense sample fps")
	flag.Float64Var(&o.k, "k", 0, "adaptive sensitivity: threshold = median + k*MAD (default 6)")
	flag.Float64Var(&o.minMotion, "min-motion", 0, "fixed 0..1 motion threshold (overrides --k when > 0)")
	flag.IntVar(&o.minFrames, "min-frames", 0, "minimum frames a burst must span (default 2)")
	flag.IntVar(&o.mergeGap, "merge-gap", -1, "merge bursts within this many calm frames (default 8)")
	flag.IntVar(&o.pad, "pad", -1, "context frames padded onto each burst window (default 3)")
	flag.IntVar(&o.localWin, "local-win", -1, "rolling-baseline half-width in frames; 0 = global baseline (default 150)")
	flag.StringVar(&o.device, "device", "", "cpu|cuda (default from config; CUDA decode is best-effort)")
	flag.StringVar(&o.output, "output", "", "write JSON here instead of stdout")
	flag.BoolVar(&o.verbose, "verbose", false, "show progress on stderr")

	flag.Parse()
	rest := flag.Args()
	if len(rest) == 0 {
		beckyio.Fatalf("usage: becky-motion <video> [--window A-B] [--fps N] [--k F] [options]")
	}
	input := rest[0]
	if len(rest) > 1 {
		_ = flag.CommandLine.Parse(rest[1:])
	}
	o.winStart, o.winEnd = parseDashRange(*win)
	return o, input
}

// parseDashRange parses "a-b" (seconds) for --window; supports decimals. Invalid
// input is fatal (bad user input the caller must fix), matching the other tools.
func parseDashRange(s string) (float64, float64) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0
	}
	idx := strings.IndexByte(s[1:], '-') // first '-' that isn't at position 0
	if idx < 0 {
		beckyio.Fatalf("--window must be A-B seconds, e.g. 10-25 (got %q)", s)
	}
	idx++ // account for the s[1:] offset
	a, errA := strconv.ParseFloat(strings.TrimSpace(s[:idx]), 64)
	b, errB := strconv.ParseFloat(strings.TrimSpace(s[idx+1:]), 64)
	if errA != nil || errB != nil {
		beckyio.Fatalf("--window must be A-B seconds, e.g. 10-25 (got %q)", s)
	}
	if b <= a {
		beckyio.Fatalf("--window end (%.3f) must be greater than start (%.3f)", b, a)
	}
	return a, b
}
