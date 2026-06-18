// Package edl is the clip-list / EDL data model for becky-clip — a multi-source
// forensic video compilation ("reel"). It owns the canonical Reel/Clip JSON
// (SPEC-BECKY-CLIP.md §4) plus two deterministic exporters:
//
//   - WriteEDL — a CMX3600 EDL (8-char reel-name table, "* FROM CLIP NAME:"
//     comments, source in/out as timecode at each clip's source fps, record
//     in/out as the running compilation timecode).
//   - WriteSRT — a re-based SRT: each clip's Label becomes a cue timed to its
//     position on the COMPILATION timeline (not the source), so the exported
//     .srt matches the exported .mp4.
//
// Pure Go: no exec, no ffmpeg, no models. Fully table-tested. Times are seconds
// (float64) into each source. Originals are never touched here — this package
// only reads/writes the small JSON timeline and emits text.
package edl

// Reel is a multi-source compilation. It is the source of truth the GUI, the
// assistant, and becky-reel all serialize. Field names/types are a frozen
// contract (SPEC-BECKY-CLIP.md §4) — do not rename them.
type Reel struct {
	Version string  `json:"version"` // "1"
	Name    string  `json:"name"`
	Clips   []Clip  `json:"clips"`
	Overlay Overlay `json:"overlay"` // defaults; per-clip Meta fills the values
	Created string  `json:"created,omitempty"`
}

// Clip is one [in,out] span of a single source video, placed in order on the
// compilation timeline. In/Out are seconds into Source (frame-accurate at
// export). Source is an ABSOLUTE path and is only ever READ.
type Clip struct {
	ID     string   `json:"id"`              // stable, e.g. "c1"
	Source string   `json:"source"`          // ABSOLUTE path to the source video (read-only)
	In     float64  `json:"in"`              // seconds into source (frame-accurate at export)
	Out    float64  `json:"out"`             // seconds into source
	Label  string   `json:"label,omitempty"` // e.g. the quote text
	Meta   ClipMeta `json:"meta"`
}

// ClipMeta is the per-clip provenance burned into the forensic lower-third. It
// is sourced from the read-only "<video>.beckymeta.json" sidecar (the original
// video is never modified). A missing sidecar yields an empty ClipMeta, never
// an error.
type ClipMeta struct {
	Date      string  `json:"date,omitempty"` // recording date if known (ISO YYYY-MM-DD)
	Link      string  `json:"link,omitempty"` // source URL if known
	Person    string  `json:"person,omitempty"`
	Location  string  `json:"location,omitempty"`
	SourceFPS float64 `json:"source_fps,omitempty"` // for the original-timecode burn
}

// Overlay holds the reel-wide defaults for the forensic lower-third. Each line
// is independently toggleable; per-clip values come from Clip.Meta.
type Overlay struct {
	Enabled      bool   `json:"enabled"`
	ShowFilename bool   `json:"show_filename"`
	ShowTimecode bool   `json:"show_timecode"` // RUNNING ORIGINAL-FILE timecode (see SPEC §5)
	ShowDate     bool   `json:"show_date"`
	ShowLink     bool   `json:"show_link"`
	ShowPerson   bool   `json:"show_person"`
	ShowLocation bool   `json:"show_location"`
	Position     string `json:"position"` // "bottom" (default) | "top"
}

// Dur returns the clip's duration in seconds (Out-In), clamped to >= 0 so a
// malformed clip can never produce a negative timeline span.
func (c Clip) Dur() float64 {
	d := c.Out - c.In
	if d < 0 {
		return 0
	}
	return d
}

// Duration returns the total compilation length in seconds: the sum of every
// clip's duration. This is the length of the rendered MP4 and the time base for
// the re-based SRT.
func (r Reel) Duration() float64 {
	var total float64
	for _, c := range r.Clips {
		total += c.Dur()
	}
	return total
}

// FPS returns the frame rate to use for a clip's timecode math: its Meta's
// SourceFPS when set, otherwise the supplied fallback (e.g. probed from the
// source, or a sane default). It never returns <= 0.
func (c Clip) FPS(fallback float64) float64 {
	if c.Meta.SourceFPS > 0 {
		return c.Meta.SourceFPS
	}
	if fallback > 0 {
		return fallback
	}
	return DefaultFPS
}

// DefaultFPS is the last-resort frame rate when neither the clip meta nor a
// probe supplies one. 30 matches the R-CUT test rig and is a safe NTSC-free
// default for deterministic timecode emission.
const DefaultFPS = 30.0
