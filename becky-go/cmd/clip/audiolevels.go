package main

// audiolevels.go exposes auto-editor's per-frame audio loudness envelope as the
// audio_levels verb. This is the SAME normalized peak-amplitude signal
// (max|sample| / 32767 per source frame) auto-editor thresholds internally when it
// cuts silence — so a threshold applied to it is frame-accurate to the source fps,
// unlike the coarse, windowed 8-bit waveform peak cache the timeline's "skip quiet
// parts" used before (which missed true silence near clip edges). The envelope is
// fetched ONCE per source and thresholded live in the UI as the user drags the
// threshold bar; auto-editor also disk-caches the levels, so a repeat call is cheap.

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"becky-go/internal/mediainfo"
	"becky-go/internal/proc"
)

// audioLevelsTimeout bounds one `auto-editor levels` exec. It decodes the whole
// audio stream once (then auto-editor caches it), so a long source can take a
// while the first time. BECKY_LEVELS_TIMEOUT (a Go duration) overrides it.
const audioLevelsTimeout = 10 * time.Minute

// AudioLevelsResult is the reply for the audio_levels verb: the per-frame loudness
// envelope plus the frame rate it is sampled at. Fps is also FORCED onto auto-editor
// (-tb) so Levels[i] maps to source seconds as i/Fps with zero drift. Levels is
// always a (possibly empty) array — never null. Note carries a plain-language reason
// when the result is empty (auto-editor missing, unresolved source, parse failure).
type AudioLevelsResult struct {
	Fps    float64   `json:"fps"`
	Levels []float64 `json:"levels"`
	Note   string    `json:"note,omitempty"`
}

// emptyLevels is the shared degrade reply: an empty (never null) level list + note.
func emptyLevels(note string) AudioLevelsResult {
	return AudioLevelsResult{Levels: []float64{}, Note: note}
}

// runAudioLevels is the seam over the real auto-editor exec. Tests override it with
// a fake that returns canned stdout so the parse flow is exercised offline.
var runAudioLevels = func(ctx context.Context, aeBin, videoPath string, fps float64) ([]byte, error) {
	// `auto-editor levels <path> --edit audio -tb <fps>` — -tb forces the per-frame
	// timebase to fps so level[i] is exactly the frame at source time i/fps, matching
	// the fps returned to the caller (no drift on a long NTSC 30000/1001 clip).
	cmd := exec.CommandContext(ctx, aeBin, "levels", videoPath,
		"--edit", "audio", "-tb", strconv.FormatFloat(fps, 'f', 6, 64))
	proc.NoWindow(cmd)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("auto-editor levels failed: %w%s", err, transcribeErrTail(errBuf.String()))
	}
	return out, nil
}

// parseAudioLevels parses auto-editor's `levels` stdout: one or more "@name" stream
// markers (e.g. "@start") each followed by one normalized float per source frame.
// We take the numbers only (blank lines + "@" markers skipped); the first stream is
// the audio envelope. A non-numeric line is skipped, never fatal. PURE.
func parseAudioLevels(stdout []byte) []float64 {
	var out []float64
	sc := bufio.NewScanner(bytes.NewReader(stdout))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "@") {
			continue
		}
		f, err := strconv.ParseFloat(line, 64)
		if err != nil {
			continue
		}
		out = append(out, f)
	}
	return out
}

// AudioLevels runs auto-editor's `levels` on the source named/pathed by source and
// returns its per-frame loudness envelope + fps. See the file header for why.
// Read-only: auto-editor's levels pass only decodes audio, never writes the source.
func (a *App) AudioLevels(source string) AudioLevelsResult {
	v, ok := a.resolveSourceForRead(source)
	if !ok {
		return emptyLevels("no such source: " + source)
	}
	bin := strings.TrimSpace(a.cfg.AutoEditor)
	if bin == "" || !fileExists(bin) {
		if p, err := exec.LookPath("auto-editor"); err == nil {
			bin = p
		} else {
			return emptyLevels("auto-editor not found — install it or set config auto_editor")
		}
	}
	ff := strings.TrimSpace(a.cfg.FFprobe)
	if ff == "" {
		ff = "ffprobe"
	}
	fps := 0.0
	if info, err := mediainfo.Probe(ff, v.Path); err == nil && info.FPS > 0 {
		fps = info.FPS
	}
	if fps <= 0 {
		fps = 30.0 // degrade: assume 30fps so levels still map to seconds
	}
	ctx, cancel := audioLevelsContext(context.Background())
	defer cancel()
	out, err := runAudioLevels(ctx, bin, v.Path, fps)
	if err != nil {
		return emptyLevels("auto-editor levels failed: " + firstLine(err))
	}
	levels := parseAudioLevels(out)
	if len(levels) == 0 {
		return emptyLevels("auto-editor levels returned no data for " + baseName(v.Path))
	}
	return AudioLevelsResult{Fps: fps, Levels: levels}
}

// audioLevelsContext builds a per-exec context with the (overridable) timeout.
func audioLevelsContext(parent context.Context) (context.Context, context.CancelFunc) {
	to := audioLevelsTimeout
	if d := strings.TrimSpace(os.Getenv("BECKY_LEVELS_TIMEOUT")); d != "" {
		if parsed, err := time.ParseDuration(d); err == nil && parsed > 0 {
			to = parsed
		}
	}
	return context.WithTimeout(parent, to)
}
