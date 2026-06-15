package canvas

// Track + clip + lane: the VISUAL-FIRST surface (CLAUDE.md HARD REQUIREMENT). Each
// track is a horizontal LANE the GUI draws; clips sit on the lane positioned in
// ticks; and every lane carries a waveform-lane / pitch-lane DATA PLACEHOLDER the
// native renderer fills with peaks and a pitch contour. Jordan fixes timing/pitch
// BY EYE on these lanes; becky LEARNS from the corrections he makes (see scene.go's
// Corrections hook). Nothing here renders — it is the model the DrawList consumes.

// LaneKind is the medium a track lane carries. It drives which surface the GUI draws
// (a MIDI piano-strip, an audio waveform, or a video filmstrip).
type LaneKind string

const (
	LaneMIDI  LaneKind = "midi"  // notes -> pitch lane / piano roll
	LaneAudio LaneKind = "audio" // samples -> waveform lane
	LaneVideo LaneKind = "video" // frames -> filmstrip lane
)

// Track is one lane on the canvas: a strip with a name, a medium, the clips placed
// on it, and the visual lane data the GUI fills. Tracks are ordered deterministically
// (see Scene assembly) so the lane stack never reshuffles between runs.
type Track struct {
	ID      string   `json:"id"`               // stable identifier (matches the source track id)
	Name    string   `json:"name"`             // display name in the lane header
	Kind    LaneKind `json:"kind"`             // midi | audio | video
	Channel int      `json:"channel"`          // MIDI channel (0 for non-MIDI)
	Bus     string   `json:"bus,omitempty"`    // routing target (from project.json `out`)
	Muted   bool     `json:"muted"`            // lane mute state
	Soloed  bool     `json:"soloed"`           // lane solo state
	Clips   []Clip   `json:"clips"`            // clips on this lane, sorted by Start
	Lane    Lane     `json:"lane"`             // the visual lane data placeholder
	Source  string   `json:"source,omitempty"` // origin file (e.g. "bass.mid")
}

// Clip is a positioned region on a lane, in ticks. The GUI draws it as a block on the
// timeline; for an audio clip the Peaks placeholder gives it a waveform body.
type Clip struct {
	ID    string `json:"id"`              // deterministic clip id
	Name  string `json:"name"`            // label drawn on the clip
	Start int64  `json:"start"`           // clip start, in ticks
	Len   int64  `json:"len"`             // clip length, in ticks
	Peaks *Peaks `json:"peaks,omitempty"` // waveform peaks placeholder (audio clips)
}

// End returns the clip's end position in ticks.
func (c Clip) End() int64 { return c.Start + c.Len }

// Lane is the per-track visual data placeholder. Exactly the data the GUI needs to
// paint the surface, left empty for the renderer/decoder to fill (this headless
// foundation never decodes audio or scans MIDI — it only declares the SHAPE).
type Lane struct {
	Wave  *WaveLane  `json:"wave,omitempty"`  // present on audio lanes
	Pitch *PitchLane `json:"pitch,omitempty"` // present on midi/audio pitch surfaces
}

// WaveLane is the waveform surface for an audio lane: min/max peak pairs the GUI
// draws as the waveform body. PeaksPerTick records the intended resolution; Peaks is
// the placeholder slice (nil until the audio decoder fills it). This keeps the model
// honest — the surface is declared, the heavy decode is deferred to the GUI/helper.
type WaveLane struct {
	PeaksPerTick float64 `json:"peaksPerTick"`    // sampling density of the peak data
	Peaks        []Peak  `json:"peaks,omitempty"` // nil placeholder; GUI/decoder fills
}

// Peaks is the waveform data for a single audio clip (same shape as WaveLane.Peaks).
type Peaks struct {
	PeaksPerTick float64 `json:"peaksPerTick"`
	Data         []Peak  `json:"data,omitempty"` // nil placeholder
}

// Peak is one min/max sample pair (the standard waveform-overview primitive),
// normalized to [-1,1]. Left empty here; the decoder fills it in the GUI.
type Peak struct {
	Min float32 `json:"min"`
	Max float32 `json:"max"`
}

// PitchLane is the pitch surface Jordan edits BY EYE: a per-track contour the GUI
// draws as the pitch ribbon (MIDI note line, or audio pitch detection). The Points
// placeholder is nil until notes/pitch are filled; Lo/Hi bound the visible pitch
// range so the GUI can scale the lane vertically.
type PitchLane struct {
	Lo     int          `json:"lo"`               // lowest MIDI note shown
	Hi     int          `json:"hi"`               // highest MIDI note shown
	Points []PitchPoint `json:"points,omitempty"` // nil placeholder; filled later
}

// PitchPoint is one sample on the pitch contour: a tick position and a (possibly
// fractional) MIDI pitch. Fractional pitch lets the audio-pitch surface show drift
// off the nearest semitone — the exact thing Jordan nudges by eye.
type PitchPoint struct {
	Tick  int64   `json:"tick"`
	Pitch float64 `json:"pitch"`
}

// newPitchLane returns an empty pitch lane spanning a sane default MIDI range so the
// GUI has a vertical scale before any notes are loaded.
func newPitchLane(lo, hi int) *PitchLane {
	if hi <= lo {
		lo, hi = 36, 84 // C2..C6, a sensible default span
	}
	return &PitchLane{Lo: lo, Hi: hi}
}
