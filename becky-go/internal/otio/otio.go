// Package otio converts a becky Reel (internal/edl) into editor-agnostic
// timeline interchange files, so the same forensic clip-list can be reviewed in
// whatever snappy NLE the user prefers:
//
//   - WriteOTIO     — OpenTimelineIO JSON. Opens NATIVELY in DaVinci Resolve and
//     kdenlive 25.04+ (File > Import > Timeline).
//   - WriteVegasList — the plain "review list" text file consumed by the VEGAS
//     Pro 18 script in /vegas/BeckyReviewTimeline.cs (VEGAS
//     imports neither OTIO nor FCPXML, so it builds the timeline
//     via its scripting API from this list).
//
// CMX3600 EDL output reuses edl.WriteEDL directly (the dumb last-resort format
// every editor opens). This package is PURE Go: no exec, no ffmpeg, no models,
// no network. Times come straight from the Reel (seconds into each source) and
// are converted to OTIO frames at each clip's fps. Source media is never touched
// — only small text/JSON timeline files are written. See SPEC-BECKY-OTIO.md.
package otio

import (
	"encoding/json"
	"io"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"becky-go/internal/edl"
	"becky-go/internal/pathx"
)

// ---- OpenTimelineIO model (the subset becky emits) -------------------------
//
// OTIO serializes as JSON with "OTIO_SCHEMA" type tags. These structs marshal to
// exactly the shape Resolve/kdenlive read. Schema version strings are the stable
// core versions and must be emitted verbatim.

type rationalTime struct {
	Schema string  `json:"OTIO_SCHEMA"` // "RationalTime.1"
	Rate   float64 `json:"rate"`
	Value  float64 `json:"value"` // whole frames
}

func newRationalTime(frames, rate float64) rationalTime {
	return rationalTime{Schema: "RationalTime.1", Rate: rate, Value: frames}
}

type timeRange struct {
	Schema    string       `json:"OTIO_SCHEMA"` // "TimeRange.1"
	StartTime rationalTime `json:"start_time"`
	Duration  rationalTime `json:"duration"`
}

type externalReference struct {
	Schema         string     `json:"OTIO_SCHEMA"` // "ExternalReference.1"
	TargetURL      string     `json:"target_url"`
	AvailableRange *timeRange `json:"available_range"`
}

type clip struct {
	Schema      string            `json:"OTIO_SCHEMA"` // "Clip.1"
	Name        string            `json:"name"`
	SourceRange timeRange         `json:"source_range"`
	MediaRef    externalReference `json:"media_reference"`
	Metadata    map[string]any    `json:"metadata"`
}

type track struct {
	Schema   string `json:"OTIO_SCHEMA"` // "Track.1"
	Name     string `json:"name"`
	Kind     string `json:"kind"` // "Video" | "Audio"
	Children []clip `json:"children"`
}

type stack struct {
	Schema   string  `json:"OTIO_SCHEMA"` // "Stack.1"
	Name     string  `json:"name"`
	Children []track `json:"children"`
}

type timeline struct {
	Schema          string         `json:"OTIO_SCHEMA"` // "Timeline.1"
	Name            string         `json:"name"`
	GlobalStartTime *rationalTime  `json:"global_start_time"`
	Metadata        map[string]any `json:"metadata"`
	Tracks          stack          `json:"tracks"`
}

// Options tunes the conversion. The zero value is the forensic-review default
// (video-only, single track end-to-end).
type Options struct {
	// IncludeAudio adds a parallel Audio track mirroring the video clips. Off by
	// default: most hosts auto-link the source's audio on import.
	IncludeAudio bool
	// FallbackFPS is used for a clip whose Meta.SourceFPS is unset. <=0 falls
	// back to edl.DefaultFPS.
	FallbackFPS float64
}

// fps resolves the frame rate for a clip's timecode math.
func fps(c edl.Clip, opts Options) float64 {
	fb := opts.FallbackFPS
	if fb <= 0 {
		fb = edl.DefaultFPS
	}
	return c.FPS(fb)
}

// buildClip turns one edl.Clip into an OTIO clip. start/dur are in FRAMES at the
// clip's fps: start = in*fps, dur = (out-in)*fps, each rounded to the nearest
// whole frame (rounding, not truncation, so long clips don't drift).
func buildClip(c edl.Clip, opts Options) clip {
	rate := fps(c, opts)
	startFrames := math.Round(c.In * rate)
	durFrames := math.Round(c.Dur() * rate)
	name := strings.TrimSpace(c.Label)
	if name == "" {
		name = pathx.Base(c.Source)
	}
	return clip{
		Schema: "Clip.1",
		Name:   name,
		SourceRange: timeRange{
			Schema:    "TimeRange.1",
			StartTime: newRationalTime(startFrames, rate),
			Duration:  newRationalTime(durFrames, rate),
		},
		MediaRef: externalReference{
			Schema:    "ExternalReference.1",
			TargetURL: FileURL(c.Source),
		},
		Metadata: map[string]any{
			"becky": map[string]any{
				"source":  c.Source,
				"in_sec":  c.In,
				"out_sec": c.Out,
			},
		},
	}
}

// build assembles the full OTIO timeline from a Reel.
func build(r edl.Reel, opts Options) timeline {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "becky-review"
	}
	var vclips, aclips []clip
	for _, c := range r.Clips {
		if c.Dur() <= 0 {
			continue // skip degenerate clips (out <= in)
		}
		cl := buildClip(c, opts)
		vclips = append(vclips, cl)
		if opts.IncludeAudio {
			aclips = append(aclips, cl)
		}
	}
	tracks := []track{{Schema: "Track.1", Name: "V1", Kind: "Video", Children: vclips}}
	if opts.IncludeAudio {
		tracks = append(tracks, track{Schema: "Track.1", Name: "A1", Kind: "Audio", Children: aclips})
	}
	return timeline{
		Schema:          "Timeline.1",
		Name:            name,
		GlobalStartTime: nil,
		Metadata: map[string]any{
			"becky": map[string]any{"generator": "becky-otio", "version": "1"},
		},
		Tracks: stack{Schema: "Stack.1", Name: "tracks", Children: tracks},
	}
}

// WriteOTIO writes the Reel as an OpenTimelineIO JSON timeline (2-space indented,
// trailing newline). Deterministic: same Reel in -> byte-identical file out.
func WriteOTIO(w io.Writer, r edl.Reel, opts Options) error {
	tl := build(r, opts)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(tl)
}

// WriteVegasList writes the plain review-list file the VEGAS Pro 18 script reads:
//
//	<absolute source path> | <in seconds> | <out seconds> | <label>
//
// One line per clip, in Reel order. The '|' delimiter is stripped from labels
// (replaced with '/'). Degenerate clips (out <= in) are skipped. Returns the
// number of clips written.
func WriteVegasList(w io.Writer, r edl.Reel) (int, error) {
	if _, err := io.WriteString(w, "# becky-otio review list  (path | in_seconds | out_seconds | label)\n"); err != nil {
		return 0, err
	}
	n := 0
	for _, c := range r.Clips {
		if c.Dur() <= 0 {
			continue
		}
		label := strings.TrimSpace(c.Label)
		if label == "" {
			label = pathx.Base(c.Source)
		}
		label = strings.ReplaceAll(label, "|", "/")
		label = strings.ReplaceAll(label, "\n", " ")
		line := c.Source + " | " + trimFloat(c.In) + " | " + trimFloat(c.Out) + " | " + label + "\n"
		if _, err := io.WriteString(w, line); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// trimFloat formats seconds without a trailing ".000000" and without scientific
// notation, so the VEGAS script's decimal parser always reads it cleanly.
func trimFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', 3, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}

// FileURL turns an absolute media path into a file:// URL OTIO importers accept.
// It handles Windows ("C:\dir\f.mp4" -> "file:///C:/dir/f.mp4") and POSIX
// ("/dir/f.mp4" -> "file:///dir/f.mp4") paths, using forward slashes. A path
// that already looks like a URL is returned unchanged.
func FileURL(p string) string {
	if strings.Contains(p, "://") {
		return p
	}
	u := filepath.ToSlash(p)
	u = strings.ReplaceAll(u, "\\", "/") // belt-and-suspenders for Windows paths on Linux
	if len(u) >= 2 && u[1] == ':' {      // drive-letter path: C:/...
		return "file:///" + u
	}
	if strings.HasPrefix(u, "/") {
		return "file://" + u
	}
	return "file:///" + u // relative/UNC-ish: keep it a valid-ish URL rather than crash
}
