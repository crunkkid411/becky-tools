// Package reel is the ffmpeg render engine behind becky-reel: it turns an
// edl.Reel (a multi-source clip list) into one frame-accurate compilation MP4
// with the forensic original-timecode lower-third burned in, plus frame-still
// and proxy helpers. It is the only place in becky that ALLOWS libx264 — as the
// degrade-never-crash fallback when h264_nvenc is unavailable (R-CUT §7).
//
// Design: the ffmpeg arg construction (args.go) and the drawtext builder
// (drawtext.go) are PURE and unit-tested without running ffmpeg. The exec
// happens here, guarded by an availability probe so the package is safe on a
// box with no ffmpeg (it returns a typed degrade result/error, never a panic).
// Source videos are opened READ-ONLY; only the chosen output path is written.
package reel

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"becky-go/internal/config"
	"becky-go/internal/edl"
	"becky-go/internal/mediainfo"
	"becky-go/internal/proc"
)

// Options configures a render. Zero values fall back to deterministic defaults:
// codec from config; and — crucially — the output dimensions + fps AUTO-MATCH the
// FIRST clip's source (probed via ffprobe), so the compilation keeps the footage's
// native resolution instead of being forced to a fixed size. Only if the first
// clip can't be probed (no ffprobe) do we fall back to 1280x720/30. The caller
// (cmd/reel) populates Output/Codec/Bitrate from flags; Width/Height/FPS are the
// power-user overrides (--width/--height/--fps) that win over the auto-match.
type Options struct {
	Output  string  // output MP4 path; "" -> "<reel-name>_reel.mp4" in cwd
	Codec   string  // "" -> config.Codec (h264_nvenc); falls back to libx264 on failure
	Bitrate string  // e.g. "12M"; "" -> codec-appropriate CQ/CRF quality
	FPS     float64 // output fps;    <=0 -> match the first clip (else 30)
	Width   int     // output width;  <=0 -> match the first clip (else 1280)
	Height  int     // output height; <=0 -> match the first clip (else 720)
	Verbose bool
}

// Result is the structured outcome of a render, serialized to JSON by the CLI.
type Result struct {
	Output      string  `json:"output"`
	Codec       string  `json:"codec"` // the codec ACTUALLY used (may differ from requested after fallback)
	Clips       int     `json:"clips"`
	DurationSec float64 `json:"duration_sec"`
	OutputMB    float64 `json:"output_mb"`
	Note        string  `json:"note,omitempty"` // degrade/fallback notes (e.g. nvenc->libx264)
}

// resolvedOpts is the fully-defaulted option set the pure arg-builder consumes.
type resolvedOpts struct {
	Output   string
	Codec    string
	Bitrate  string
	OutFPS   float64
	Width    int
	Height   int
	FontFile string

	// Audio turns on keeping the clips' sound in the compilation (set by Render
	// whenever ffprobe is available to detect streams). ClipHasAudio[i] says whether
	// clip i's source actually has an audio stream — clips without one are filled
	// with silence so the audio concat never errors. With Audio off the render is
	// visual-only (the old -an behaviour).
	Audio        bool
	ClipHasAudio []bool
}

const libx264 = "libx264"

// Render assembles the reel into one MP4 with the lower-third burned in. It
// tries the requested/config codec (h264_nvenc by default) and, on an
// nvenc-specific failure, retries once with libx264 — recording the fallback in
// Result.Note. It never modifies a source file.
func Render(r edl.Reel, opts Options) (Result, error) {
	cfg := config.Load()
	if cfg.FFmpeg == "" || !available(cfg.FFmpeg) {
		return Result{}, fmt.Errorf("ffmpeg not available (config FFmpeg=%q); cannot render", cfg.FFmpeg)
	}
	if len(r.Clips) == 0 {
		return Result{}, fmt.Errorf("reel %q has no clips", r.Name)
	}
	if err := checkSourcesReadable(r); err != nil {
		return Result{}, err
	}

	ropts := resolveOptions(r, opts, cfg)

	// Keep the clips' AUDIO in the compilation — it is a record of WHAT WAS SAID; a
	// transcript/quote tool whose export is silent is useless. We can do this safely
	// only when ffprobe is available to tell which clips actually HAVE an audio
	// stream (clips without one get silence so the concat never errors). With no
	// ffprobe we degrade to a silent render and say so.
	var note string
	if cfg.FFprobe != "" && available(cfg.FFprobe) {
		ropts.Audio = true
		ropts.ClipHasAudio = make([]bool, len(r.Clips))
		for i, c := range r.Clips {
			if info, e := mediainfo.Probe(cfg.FFprobe, c.Source); e == nil {
				ropts.ClipHasAudio[i] = info.HasAudio
			}
		}
	} else {
		note = "audio omitted: ffprobe unavailable to detect audio streams"
	}

	args, err := buildRenderArgs(r, ropts)
	if err != nil {
		return Result{}, err
	}
	runErr := runFFmpeg(cfg.FFmpeg, opts.Verbose, args)
	if runErr != nil && shouldFallbackToLibx264(ropts.Codec) {
		// Degrade-never-crash: nvenc failed (GPU-less box or init error). Retry
		// with libx264 — identical correctness, ~20% slower (R-CUT §7).
		fallback := ropts
		fallback.Codec = libx264
		// Rebuild args so the quality flags match the new codec.
		if a2, e2 := buildRenderArgs(r, fallback); e2 == nil {
			if e3 := runFFmpeg(cfg.FFmpeg, opts.Verbose, a2); e3 == nil {
				ropts = fallback
				note = fmt.Sprintf("h264_nvenc unavailable (%v); fell back to libx264", firstLine(runErr))
				runErr = nil
			} else {
				runErr = fmt.Errorf("nvenc failed (%v) and libx264 fallback also failed (%v)", firstLine(runErr), firstLine(e3))
			}
		}
	}
	if runErr != nil {
		return Result{}, runErr
	}

	res := Result{
		Output:      ropts.Output,
		Codec:       ropts.Codec,
		Clips:       len(r.Clips),
		DurationSec: round3(r.Duration()),
		Note:        note,
	}
	if fi, e := os.Stat(ropts.Output); e == nil {
		res.OutputMB = round3(float64(fi.Size()) / (1024 * 1024))
	}
	// Prefer the probed duration of the real output when ffprobe is available.
	if cfg.FFprobe != "" && available(cfg.FFprobe) {
		if info, e := mediainfo.Probe(cfg.FFprobe, ropts.Output); e == nil && info.Duration > 0 {
			res.DurationSec = round3(info.Duration)
		}
	}
	return res, nil
}

// firstClipProbe is the seam over "read the first clip's pixel dimensions + fps".
// It defaults to an ffprobe-backed probe (probeFirstClip) but is overridable in
// tests so the auto-match logic is exercised without ffmpeg. ok=false means the
// source couldn't be probed (no ffprobe / unreadable) and the caller uses the
// 1280x720/30 fallback. Production never reassigns it.
var firstClipProbe = probeFirstClip

// resolveOptions fills every zero option with its deterministic default. When the
// caller left Width/Height/FPS unset, the output AUTO-MATCHES the FIRST clip's
// source resolution + fps (probed via firstClipProbe) — this is what makes "the
// project/export dimensions just match the first clip imported" true. Explicit
// --width/--height/--fps still win. If the first clip can't be probed, the
// classic 1280x720/30 fallback applies so a render still succeeds.
func resolveOptions(r edl.Reel, opts Options, cfg config.Config) resolvedOpts {
	ro := resolvedOpts{
		Output:   opts.Output,
		Codec:    opts.Codec,
		Bitrate:  opts.Bitrate,
		OutFPS:   opts.FPS,
		Width:    opts.Width,
		Height:   opts.Height,
		FontFile: fontFile(),
	}
	if ro.Codec == "" {
		ro.Codec = cfg.Codec
	}
	if ro.Codec == "" {
		ro.Codec = "h264_nvenc"
	}

	// Auto-match the first clip when any of width/height/fps is unset. Probe once.
	if (ro.Width <= 0 || ro.Height <= 0 || ro.OutFPS <= 0) && len(r.Clips) > 0 {
		if w, h, fps, ok := firstClipProbe(cfg.FFprobe, r.Clips[0].Source); ok {
			if ro.Width <= 0 && w > 0 {
				ro.Width = w
			}
			if ro.Height <= 0 && h > 0 {
				ro.Height = h
			}
			if ro.OutFPS <= 0 && fps > 0 {
				ro.OutFPS = fps
			}
		}
	}

	// Fallbacks if the probe was unavailable or returned nothing usable.
	if ro.OutFPS <= 0 {
		ro.OutFPS = defaultOutFPS
	}
	if ro.Width <= 0 {
		ro.Width = defaultWidth
	}
	if ro.Height <= 0 {
		ro.Height = defaultHeight
	}
	if ro.Output == "" {
		ro.Output = defaultReelOutput(r)
	}
	ro.Output = mustAbs(ro.Output)
	return ro
}

// probeFirstClip reads a source video's pixel width/height + frame rate via
// ffprobe (internal/mediainfo). ok=false when ffprobe is unavailable or the
// source has no usable video stream, so the caller falls back to the fixed
// default. Read-only: it only inspects the source, never writes it.
func probeFirstClip(ffprobe, source string) (w, h int, fps float64, ok bool) {
	if ffprobe == "" || !available(ffprobe) {
		return 0, 0, 0, false
	}
	info, err := mediainfo.Probe(ffprobe, source)
	if err != nil || info.Width <= 0 || info.Height <= 0 {
		return 0, 0, 0, false
	}
	return info.Width, info.Height, info.FPS, true
}

// shouldFallbackToLibx264 reports whether a failed render with the given codec
// should be retried with libx264 (only when the requested codec was an nvenc
// encoder — there's nothing to fall back from if libx264 itself failed).
func shouldFallbackToLibx264(codec string) bool {
	return strings.Contains(codec, "nvenc")
}

// checkSourcesReadable confirms every clip source exists and is readable. A
// missing source is a clear error rather than an opaque ffmpeg failure.
func checkSourcesReadable(r edl.Reel) error {
	for _, c := range r.Clips {
		if c.Source == "" {
			return fmt.Errorf("clip %q has no source", c.ID)
		}
		if _, err := os.Stat(c.Source); err != nil {
			return fmt.Errorf("clip %q source not found: %s", c.ID, c.Source)
		}
	}
	return nil
}

// defaultReelOutput builds "<reel-name>_reel.mp4" (slugified) in the cwd.
func defaultReelOutput(r edl.Reel) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "becky"
	}
	return slug(name) + "_reel.mp4"
}

// fontFile resolves the forensic font: BECKY_REEL_FONT override, else default.
func fontFile() string {
	if f := strings.TrimSpace(os.Getenv("BECKY_REEL_FONT")); f != "" {
		return f
	}
	return defaultFont
}

// runFFmpeg execs ffmpeg with the given args, capturing stderr for diagnostics.
func runFFmpeg(ffmpeg string, verbose bool, args []string) error {
	cmd := exec.Command(ffmpeg, args...)
	proc.NoWindow(cmd) // no console-window flash when the GUI (windowsgui) spawns ffmpeg
	var errBuf strings.Builder
	if verbose {
		cmd.Stderr = teeStderr(&errBuf)
	} else {
		cmd.Stderr = &errBuf
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg: %v\n%s", err, tail(errBuf.String()))
	}
	return nil
}

// teeStderr mirrors ffmpeg's stderr to both os.Stderr (for --verbose) and the
// capture buffer (for the error message).
func teeStderr(buf *strings.Builder) io.Writer {
	return io.MultiWriter(os.Stderr, buf)
}

// available reports whether a binary path looks runnable: it exists on disk, or
// resolves on PATH (a bare name like "ffmpeg").
func available(bin string) bool {
	if bin == "" {
		return false
	}
	if _, err := os.Stat(bin); err == nil {
		return true
	}
	if _, err := exec.LookPath(bin); err == nil {
		return true
	}
	return false
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

func firstLine(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// slug lowercases and replaces runs of non-alphanumeric chars with a single '-'.
func slug(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "becky"
	}
	return out
}
