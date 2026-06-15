package canvas

// Corrections log hook (CLAUDE.md HARD REQUIREMENT: "becky LEARNS his preferences
// from his corrections"). This is the data hook the VISUAL-FIRST surface records
// into when Jordan fixes timing/pitch/gain BY EYE. The headless foundation only
// declares the SHAPE and a deterministic append API — the GUI feeds it real edits,
// and a later learning step reads the log to model Jordan's preferences. No learning
// happens here; this is the durable record the learner will consume.

// CorrectionKind names the sort of manual fix Jordan made on the surface.
type CorrectionKind string

const (
	FixTiming CorrectionKind = "timing" // nudged a note/clip earlier or later
	FixPitch  CorrectionKind = "pitch"  // pulled a pitch contour to a new value
	FixGain   CorrectionKind = "gain"   // changed a clip/lane level
	FixRoute  CorrectionKind = "route"  // re-pointed a routing edge (e.g. a sidechain)
	FixOther  CorrectionKind = "other"  // anything else worth learning from
)

// Correction is one manual edit Jordan made, captured so becky can learn from it.
// Before/After are free-form numeric values interpreted per Kind (ticks for timing,
// MIDI pitch for pitch, dB for gain). TrackID/ClipID locate the edit; At is the tick
// it applied to. Seq is a monotonic, deterministic ordinal (no wall-clock time, to
// keep the scene reproducible — the GUI may add timestamps in its own layer).
type Correction struct {
	Seq     int            `json:"seq"`              // monotonic order of capture
	Kind    CorrectionKind `json:"kind"`             // timing | pitch | gain | route | other
	TrackID string         `json:"trackId"`          // which lane was edited
	ClipID  string         `json:"clipId,omitempty"` // which clip, if clip-scoped
	At      int64          `json:"at"`               // tick the edit applied to
	Before  float64        `json:"before"`           // value before the edit
	After   float64        `json:"after"`            // value after the edit
	Note    string         `json:"note,omitempty"`   // optional human note
}

// CorrectionsLog is the append-only record of Jordan's by-eye fixes. It is carried on
// every Scene so the surface always has somewhere to write a correction, and so the
// emitted scene.json round-trips the history deterministically.
type CorrectionsLog struct {
	Entries []Correction `json:"entries"`
}

// NewCorrectionsLog returns an empty log (never nil Entries, so JSON emits []).
func NewCorrectionsLog() CorrectionsLog {
	return CorrectionsLog{Entries: []Correction{}}
}

// Append records a correction, assigning the next deterministic Seq. It returns a NEW
// log (immutable update, per coding-style) so callers never mutate a shared slice.
func (l CorrectionsLog) Append(c Correction) CorrectionsLog {
	c.Seq = len(l.Entries)
	next := make([]Correction, len(l.Entries), len(l.Entries)+1)
	copy(next, l.Entries)
	next = append(next, c)
	return CorrectionsLog{Entries: next}
}

// Len reports how many corrections have been captured.
func (l CorrectionsLog) Len() int { return len(l.Entries) }
