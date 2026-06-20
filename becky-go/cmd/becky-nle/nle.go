// nle.go — the toolkit-INDEPENDENT NLE core for becky-nle, shared by BOTH the Gio
// window (gui*.go, //go:build gui) and the headless CLI (main.go, //go:build !gui).
// It carries no Gio import, so it compiles in every configuration and is unit-tested
// without the GUI system libs.
//
// What lives here:
//   - Project: the tiny timeline model (one source video + its probed Info + in/out
//     marks + playhead). The window READS it and EMITS edits; this struct is the
//     single source of truth (GUI-RULES.md §2.1).
//   - exportRange: turns the marked [in,out] span into a one-clip edl.Reel and renders
//     a real MP4 next to the source via internal/reel (reusing becky's proven engine).
//   - timecode helpers: hours-aware H:MM:SS.mmm for the ruler/playhead readout.
//   - routeCommand: the one AI/command box, keyword-routed for now (GUI-RULES.md §4 —
//     the model upgrade is a later wave; the box must do something useful today).
//
// degrade-never-crash (CLAUDE.md §2): every helper returns a typed error or a clamped
// value; nothing here panics on a missing file / no-ffmpeg / out-of-range mark.
package main

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"becky-go/internal/edl"
	"becky-go/internal/pathx"
	"becky-go/internal/reel"
	"becky-go/internal/videopreview"
)

// Project is the live NLE timeline state. Immutable-by-convention: edits go through
// the small mutators (SetMarks/SetPlayhead) so the rules stay in one place and the
// window just renders the result.
type Project struct {
	Source string            // absolute path to the open source video ("" = nothing open)
	Info   videopreview.Info // probed geometry/fps/duration from video.open
	In     float64           // in-mark, seconds (>=0, <= Out)
	Out    float64           // out-mark, seconds (<= duration)
	Play   float64           // playhead, seconds (the scrub position)
}

// NewProject builds an empty project (nothing open).
func NewProject() *Project { return &Project{} }

// LoadInfo records a freshly-opened source + its Info and resets the marks to span
// the whole clip and the playhead to the start.
func (p *Project) LoadInfo(source string, info videopreview.Info) {
	p.Source = source
	p.Info = info
	p.In = 0
	p.Out = info.DurationSec
	p.Play = 0
}

// Duration is the open clip's length in seconds (0 when nothing is open).
func (p *Project) Duration() float64 {
	if p.Info.DurationSec > 0 {
		return p.Info.DurationSec
	}
	return 0
}

// IsOpen reports whether a source video is loaded.
func (p *Project) IsOpen() bool { return strings.TrimSpace(p.Source) != "" }

// SetPlayhead clamps t to [0,duration] and stores it. Returns the clamped value so
// the caller can use it directly for a frame request.
func (p *Project) SetPlayhead(t float64) float64 {
	p.Play = clamp(t, 0, p.Duration())
	return p.Play
}

// SetIn sets the in-mark to t (clamped below Out).
func (p *Project) SetIn(t float64) {
	t = clamp(t, 0, p.Duration())
	if t > p.Out {
		t = p.Out
	}
	p.In = t
}

// SetOut sets the out-mark to t (clamped above In).
func (p *Project) SetOut(t float64) {
	t = clamp(t, 0, p.Duration())
	if t < p.In {
		t = p.In
	}
	p.Out = t
}

// MarkDur is the marked range length in seconds (Out-In, clamped >= 0).
func (p *Project) MarkDur() float64 {
	d := p.Out - p.In
	if d < 0 {
		return 0
	}
	return d
}

// --- export ----------------------------------------------------------------------

// ExportResult is the outcome of an export, surfaced to the window as one line.
type ExportResult struct {
	Output      string  // absolute path of the written MP4
	DurationSec float64 // length of the exported range
	Codec       string  // the codec actually used (may have fallen back to libx264)
	Note        string  // degrade/fallback note (e.g. nvenc->libx264, no-audio)
}

// exportRange renders the marked [In,Out] span of the open source to a NEW MP4 next
// to the source (never touching the original), reusing internal/reel's frame-accurate
// re-encode + audio-keeping engine. outPath="" defaults to "<dir>/<stem>_range.mp4".
//
// It builds a one-clip edl.Reel: the marks become the clip's in/out, and the forensic
// lower-third is OFF by default (this is a raw cut tool — the becky-clip compilation
// path is where the burned-in provenance lives). Returns a typed error when nothing
// is open, the range is empty, or ffmpeg is unavailable — never a panic.
func exportRange(p *Project, outPath string) (ExportResult, error) {
	if !p.IsOpen() {
		return ExportResult{}, fmt.Errorf("nothing to export: open a video first")
	}
	if p.MarkDur() <= 0 {
		return ExportResult{}, fmt.Errorf("the marked range is empty: set an out-mark past the in-mark")
	}

	abs, err := filepath.Abs(p.Source)
	if err != nil {
		abs = p.Source
	}
	r := edl.Reel{
		Version: "1",
		Name:    stem(abs) + "_range",
		Clips: []edl.Clip{{
			ID:     "c1",
			Source: abs,
			In:     p.In,
			Out:    p.Out,
			Label:  "",
			Meta:   edl.ClipMeta{SourceFPS: p.Info.FPS},
		}},
		Overlay: edl.Overlay{Enabled: false}, // raw cut: no burned-in lower-third
	}

	if outPath == "" {
		outPath = defaultExportPath(abs)
	}

	res, err := reel.Render(r, reel.Options{
		Output: outPath,
		// Codec/Width/Height/FPS left zero -> reel auto-matches the source + uses the
		// configured codec (h264_nvenc, libx264 fallback). Exactly what we want for a
		// 1:1 cut.
	})
	if err != nil {
		return ExportResult{}, err
	}
	return ExportResult{
		Output:      res.Output,
		DurationSec: res.DurationSec,
		Codec:       res.Codec,
		Note:        res.Note,
	}, nil
}

// defaultExportPath builds "<source-dir>/<stem>_range.mp4" — a NEW file next to the
// original (the becky protocol: outputs live next to the source, originals untouched).
func defaultExportPath(source string) string {
	dir := pathx.Dir(source)
	name := stem(source) + "_range.mp4"
	if dir == "" {
		return name
	}
	return dir + string(filepath.Separator) + name
}

// stem returns the basename of p without its extension (separator-agnostic).
func stem(p string) string {
	base := pathx.Base(p)
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		return base[:i]
	}
	return base
}

// --- timecode --------------------------------------------------------------------

// formatTC renders seconds as an hours-aware timecode H:MM:SS.mmm (or MM:SS.mmm when
// under an hour). Used for the ruler, playhead, and mark readouts. Negative -> 0.
func formatTC(sec float64) string {
	if sec < 0 || math.IsNaN(sec) {
		sec = 0
	}
	total := int(sec)
	ms := int(math.Round((sec - float64(total)) * 1000))
	if ms >= 1000 { // rounding spill
		total++
		ms = 0
	}
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d.%03d", h, m, s, ms)
	}
	return fmt.Sprintf("%02d:%02d.%03d", m, s, ms)
}

// formatTCShort renders seconds as H:MM:SS / M:SS without milliseconds (compact
// labels on the ruler).
func formatTCShort(sec float64) string {
	if sec < 0 || math.IsNaN(sec) {
		sec = 0
	}
	total := int(math.Round(sec))
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// --- the one command box (keyword-routed for now) --------------------------------

// commandResult is what routeCommand asks the window to do. Exactly one action field
// is set (or none, with a Reply explaining). This keeps the AI box deterministic and
// testable today; a model-backed planner is a later wave (GUI-RULES.md §4).
type commandResult struct {
	Reply        string  // a one-line answer to show in the status bar
	SeekTo       float64 // when Seek is true, the seconds to scrub to
	Seek         bool
	MarkIn       bool // set the in-mark at the playhead
	MarkOut      bool // set the out-mark at the playhead
	Export       bool // run an export of the marked range
	OpenPicker   bool // open a file
	WantWindow   bool // pop the dedicated sidecar preview window
	Unrecognized bool
}

// routeCommand maps a plain-English instruction to one NLE action against the project.
// Deterministic keyword routing (the deterministic floor under the future model). It
// NEVER mutates the project — it returns the intent; the window applies it (so the
// "show-me / explicit-apply" discipline holds, GUI-RULES.md §4).
func routeCommand(text string, p *Project) commandResult {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return commandResult{Reply: "type what you want (e.g. mark in, mark out, export, go to 1:23, open)"}
	}
	switch {
	case hasAny(t, "mark in", "set in", "in point", "in mark"):
		if !p.IsOpen() {
			return commandResult{Reply: "open a video first"}
		}
		return commandResult{MarkIn: true, Reply: "in-mark set at " + formatTC(p.Play)}
	case hasAny(t, "mark out", "set out", "out point", "out mark"):
		if !p.IsOpen() {
			return commandResult{Reply: "open a video first"}
		}
		return commandResult{MarkOut: true, Reply: "out-mark set at " + formatTC(p.Play)}
	case hasAny(t, "export", "render", "save clip", "cut it", "make the clip"):
		if !p.IsOpen() {
			return commandResult{Reply: "open a video first"}
		}
		return commandResult{Export: true, Reply: "exporting the marked range…"}
	case hasAny(t, "window", "popout", "pop out", "big preview", "scrub window"):
		return commandResult{WantWindow: true, Reply: "opening the GPU preview window…"}
	case hasAny(t, "open", "load", "import"):
		return commandResult{OpenPicker: true, Reply: "opening a video…"}
	case hasPrefixWord(t, "go to", "seek", "jump to", "goto"):
		if !p.IsOpen() {
			return commandResult{Reply: "open a video first"}
		}
		if sec, ok := parseTimeArg(t); ok {
			to := clamp(sec, 0, p.Duration())
			return commandResult{Seek: true, SeekTo: to, Reply: "scrubbing to " + formatTC(to)}
		}
		return commandResult{Reply: "say a time like 'go to 1:23' or 'seek 90'"}
	default:
		// A bare time ("1:23", "90") is treated as a seek — the most common ask.
		if sec, ok := parseTimecode(t); ok && p.IsOpen() {
			to := clamp(sec, 0, p.Duration())
			return commandResult{Seek: true, SeekTo: to, Reply: "scrubbing to " + formatTC(to)}
		}
		return commandResult{Unrecognized: true, Reply: "not sure — try: mark in / mark out / export / go to 1:23 / open"}
	}
}

// parseTimeArg pulls a time token off the end of a "go to <time>" style instruction.
func parseTimeArg(t string) (float64, bool) {
	fields := strings.Fields(t)
	if len(fields) == 0 {
		return 0, false
	}
	return parseTimecode(fields[len(fields)-1])
}

// parseTimecode parses "H:MM:SS", "MM:SS", "SS", or "SS.mmm" into seconds.
func parseTimecode(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	parts := strings.Split(s, ":")
	var total float64
	for _, part := range parts {
		var v float64
		if _, err := fmt.Sscanf(part, "%f", &v); err != nil {
			return 0, false
		}
		total = total*60 + v
	}
	return total, true
}

func hasAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// hasPrefixWord reports whether s starts with any of the given phrases.
func hasPrefixWord(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// clamp constrains v to [lo,hi]. When hi<=0 (no duration known) only the lower bound
// applies.
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		v = lo
	}
	if hi > 0 && v > hi {
		v = hi
	}
	return v
}

// friendlySidecarErr turns the sidecar-missing error into Jordan-language (shared by the
// headless CLI and the window). Lives here (build-tag-neutral) so both builds see it.
func friendlySidecarErr(err error) string {
	return "the GPU video preview isn't built yet — run Build Becky NLE (it builds becky-video-preview). Detail: " + err.Error()
}
