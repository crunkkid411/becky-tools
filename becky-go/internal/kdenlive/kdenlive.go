// Package kdenlive turns a forensic cut-list (a list of {source video, in-point,
// out-point}) into a VALID kdenlive/MLT XML project, and renders that project
// headless via the kdenlive-bundled melt.exe. It is the bridge that lets becky
// DRIVE the real, already-installed kdenlive instead of hand-rolling an NLE: the
// same .kdenlive file opens in the kdenlive GUI for a human (becky-nle --open)
// AND renders deterministically from the command line (becky-nle --render).
//
// Why this shape (proven against melt 7.37 on real footage):
//   - The MLT model is: one <profile> (the timeline geometry) + one <producer>
//     per distinct source file (carrying its on-disk resource + true length) +
//     a <playlist> of <entry> clip refs that carry the actual in/out CUT points
//     (frame indices into the producer) + a final <tractor> that binds the
//     playlist as track 0 (the timeline).
//   - CRITICAL: producers MUST use mlt_service="avformat" (the VALIDATING reader)
//     and declare a real `length`. With "avformat-novalidate" melt reports an
//     unknown source length, so every playlist <entry> collapses to ~1 frame and
//     the render is 2 frames long. (This was the single non-obvious failure; it
//     is now locked down by the package + a rendered-duration check in the CLI.)
//   - CRITICAL: the `xmlns:kdenlive` namespace MUST be declared on <mlt> or strict
//     "Gen-2" kdenlive parsers reject the project on open. kdenlive-only metadata
//     lives under the `kdenlive:` prefix so melt ignores it during a render.
//
// Pure data + encoding/xml here; the only exec is Render (melt). Source videos
// are opened READ-ONLY — only the chosen .kdenlive / .mp4 output paths are written.
// Degrade-never-crash (CLAUDE.md §2): a bad cut / missing melt / unreadable source
// returns a typed error, never a panic.
package kdenlive

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"becky-go/internal/pathx"
)

// DefaultFPS is the last-resort frame rate when a clip carries none and no probe
// supplied one. 30 matches becky's other timecode code (edl.DefaultFPS).
const DefaultFPS = 30.0

// Clip is one [In,Out] span of a single source video, placed in order on the
// kdenlive timeline. Times are SECONDS into Source. Source is read-only.
type Clip struct {
	Source string  // path to the source video (read-only); Windows or POSIX
	In     float64 // in-point, seconds into the source (>=0)
	Out    float64 // out-point, seconds into the source (> In)
	Name   string  // optional human label shown in the kdenlive bin (kdenlive:clipname)
}

// Dur is the clip's length in seconds (Out-In, clamped >= 0).
func (c Clip) Dur() float64 {
	d := c.Out - c.In
	if d < 0 {
		return 0
	}
	return d
}

// Source carries the probed metadata for one distinct source file: its true
// length (seconds) and frame rate. Length is what makes the validating-avformat
// producer expose the whole clip so the playlist cuts land correctly. A zero
// LengthSec is allowed (the builder falls back to the farthest cut + a pad), but
// supplying the real ffprobe duration is strongly preferred.
type Source struct {
	Path      string
	LengthSec float64 // total source duration in seconds (from ffprobe); 0 = unknown
	FPS       float64 // source frame rate; <=0 falls back to the project fps
}

// Project is everything needed to emit one .kdenlive file. Build it directly or
// via NewProject + AddClip. Width/Height/FPS define the <profile> (the render
// geometry); when zero they auto-match the first clip's source (the becky rule
// "the project matches the first clip imported").
type Project struct {
	Title   string            // kdenlive bin / project label (kdenlive:clipname etc.)
	Width   int               // <=0 -> caller fills from the first source's probe, else fallback
	Height  int               // <=0 -> caller fills from the first source's probe, else fallback
	FPS     float64           // <=0 -> first source's fps else DefaultFPS
	Clips   []Clip            // the ordered cut-list (the timeline)
	Sources map[string]Source // probed metadata keyed by source path (optional but recommended)
}

// Fallback geometry when nothing can be probed — a sane 16:9 SD default that
// always renders. Real projects override this from the first clip's probe.
const (
	fallbackWidth  = 1280
	fallbackHeight = 720
)

// NewProject returns an empty project with the given title.
func NewProject(title string) *Project {
	return &Project{Title: title, Sources: map[string]Source{}}
}

// AddClip appends a cut to the timeline.
func (p *Project) AddClip(c Clip) { p.Clips = append(p.Clips, c) }

// SetSource records probed metadata (length + fps) for a source path so the
// emitted producer exposes the full clip. Safe to call once per distinct source.
func (p *Project) SetSource(s Source) {
	if p.Sources == nil {
		p.Sources = map[string]Source{}
	}
	p.Sources[s.Path] = s
}

// fps returns the project frame rate, falling back to the first clip's recorded
// source fps, then DefaultFPS. Always > 0.
func (p *Project) fps() float64 {
	if p.FPS > 0 {
		return p.FPS
	}
	if len(p.Clips) > 0 {
		if s, ok := p.Sources[p.Clips[0].Source]; ok && s.FPS > 0 {
			return s.FPS
		}
	}
	return DefaultFPS
}

// dims returns the project width/height, falling back to fallbackWidth/Height.
// (Pixel dimensions can't be derived from a Source here — the caller fills
// Width/Height from a probe; this only guards the zero case so a render never
// fails for want of a geometry.)
func (p *Project) dims() (int, int) {
	w, h := p.Width, p.Height
	if w <= 0 {
		w = fallbackWidth
	}
	if h <= 0 {
		h = fallbackHeight
	}
	return w, h
}

// secToFrame converts seconds to an inclusive MLT frame index at fps, rounding
// to the nearest frame. Never negative.
func secToFrame(sec, fps float64) int {
	if fps <= 0 {
		fps = DefaultFPS
	}
	if sec < 0 || math.IsNaN(sec) {
		sec = 0
	}
	f := int(math.Round(sec * fps))
	if f < 0 {
		f = 0
	}
	return f
}

// producerLengthFrames computes the frame `length`/`out` a producer must expose
// for a source so every cut that references it is in range. It prefers the probed
// LengthSec; if that's unknown it falls back to the farthest Out among the clips
// that use this source, padded by one second, so the cuts never exceed the
// producer. Always >= 1.
func (p *Project) producerLengthFrames(srcPath string, fps float64) int {
	var lengthSec float64
	if s, ok := p.Sources[srcPath]; ok && s.LengthSec > 0 {
		lengthSec = s.LengthSec
	} else {
		// Unknown source length: cover the farthest cut + 1s pad.
		var maxOut float64
		for _, c := range p.Clips {
			if c.Source == srcPath && c.Out > maxOut {
				maxOut = c.Out
			}
		}
		lengthSec = maxOut + 1.0
	}
	frames := secToFrame(lengthSec, fps)
	if frames < 1 {
		frames = 1
	}
	return frames
}

// Validate checks the project is renderable: at least one clip, every clip has a
// source and a positive duration. Returns a typed error listing the first
// problem (degrade-never-crash — the caller surfaces this in plain language).
func (p *Project) Validate() error {
	if len(p.Clips) == 0 {
		return fmt.Errorf("kdenlive: project %q has no clips", p.Title)
	}
	for i, c := range p.Clips {
		if strings.TrimSpace(c.Source) == "" {
			return fmt.Errorf("kdenlive: clip %d has no source", i+1)
		}
		if c.Dur() <= 0 {
			return fmt.Errorf("kdenlive: clip %d (%s) has an empty range (out %.3f must be > in %.3f)",
				i+1, pathx.Base(c.Source), c.Out, c.In)
		}
	}
	return nil
}

// resourcePath normalizes a source path for the MLT <property name="resource">.
// kdenlive writes native paths; melt accepts both, but mixing separators inside
// one string is what trips strict parsers. We canonicalize to forward slashes
// (valid on Windows melt and POSIX) and leave a drive letter intact.
func resourcePath(p string) string {
	return filepath.ToSlash(strings.TrimSpace(p))
}
