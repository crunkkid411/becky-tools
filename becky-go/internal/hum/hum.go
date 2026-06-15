// Package hum is becky's INPUT side of becky-compose: it turns a hummed or sung
// melody into a {key, tempo, monophonic MIDI} decision sheet with key-aware,
// per-note suggestions (SPEC-BECKY-HUM.md). Everything in this package is pure,
// deterministic Go operating on already-extracted features (an F0 contour and/or
// a note list) — the audio→features boundary (WAV decode, pYIN/basic-pitch f0
// extraction) is a documented stub (see Extractor in features.go) the local agent
// wires to a real model. Same features in => same key/tempo/notes/suggestions out.
//
// Pipeline (SPEC §3): ingest -> KEY (Krumhansl-Schmuckler) -> TEMPO (onset +
// autocorrelation) -> PITCH→notes (segment a contour) -> key-aware suggestions ->
// emit. Stages here are stages 2-6; stage 1 (ffmpeg normalize) and the model call
// in stage 4 live behind the stub.
package hum

// SchemaVersion is the stdout JSON contract version (SPEC §4).
const SchemaVersion = 1

// Frame is one analysis frame of the monophonic F0 contour. T is seconds from the
// start, F0 is the estimated fundamental in Hz (0 when unvoiced), Voiced is the
// voicing probability 0..1. This is exactly the per-frame shape the pitch stub
// emits (features.go Extractor.Frames). The Go segmenter turns frames into notes.
type Frame struct {
	T      float64 `json:"t"`
	F0     float64 `json:"f0"`
	Voiced float64 `json:"voiced"`
}

// Note is one transcribed monophonic note. OnsetSec/DurSec are seconds, Midi is
// the median MIDI pitch over the note, PitchHz the median fundamental, Confidence
// the fused 0..1 score. The key-aware fields (InKey, DistanceCents, Suggestion,
// NeedsReview) are filled by the suggestion engine (suggest.go), not the segmenter.
type Note struct {
	I             int         `json:"i"`
	OnsetSec      float64     `json:"onsetSec"`
	DurSec        float64     `json:"durSec"`
	Midi          int         `json:"midi"`
	PitchHz       float64     `json:"pitchHz"`
	Confidence    float64     `json:"confidence"`
	InKey         bool        `json:"inKey"`
	DistanceCents float64     `json:"distanceCents,omitempty"`
	Suggestion    *Suggestion `json:"suggestion"`
	NeedsReview   bool        `json:"needsReview"`
}

// Suggestion is a key-aware proposal for an off-key/ambiguous note. It is never
// applied silently — Midi/Name is what becky thinks was meant, Reason explains
// WHY (corroborate-then-conclude), Alts are runner-up candidates when signals
// disagree. The canvas renders this as a ghost note the producer clicks to accept.
type Suggestion struct {
	Midi   int    `json:"midi"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
	Alts   []int  `json:"alts,omitempty"`
}

// KeyResult is the stage-2 key decision (Krumhansl-Schmuckler). Compose is the
// exact string becky-compose --key parses (e.g. "F#m"). Confidence is "clear" /
// "good" / "ambiguous" mapped to a 0..1 number; RunnerUp + CorrGap expose the
// margin so a narrow gap (relative major/minor) is reported, not guessed.
type KeyResult struct {
	Root       string  `json:"root"`
	Scale      string  `json:"scale"`
	Compose    string  `json:"compose"`
	Confidence float64 `json:"confidence"`
	Method     string  `json:"method"`
	RunnerUp   string  `json:"runnerUp"`
	CorrGap    float64 `json:"corrGap"`
	Ambiguous  bool    `json:"ambiguous"`
}

// TempoResult is the stage-3 BPM decision. Alt holds the octave alternatives
// (half/double tempo); ResolvedBy says how the octave ambiguity was settled.
type TempoResult struct {
	BPM        int     `json:"bpm"`
	Confidence float64 `json:"confidence"`
	Method     string  `json:"method"`
	Alt        []int   `json:"alt,omitempty"`
	ResolvedBy string  `json:"resolvedBy"`
}

// Correction is one row of the corrections log — the substrate for the
// preference-learning loop (CLAUDE.md HARD REQUIREMENT). Every time becky proposes
// (or applies) a pitch and Jordan overrides it, a Correction records {auto value,
// his corrected value, context}. Field=which decision ("note.midi"), Auto=becky's
// value, Corrected=Jordan's (nil until he edits), Context carries the surrounding
// musical facts so a future model can learn his temperament from his edits.
type Correction struct {
	NoteIndex int                    `json:"noteIndex"`
	Field     string                 `json:"field"`
	Auto      float64                `json:"auto"`
	Corrected *float64               `json:"corrected"`
	Reason    string                 `json:"reason"`
	Context   map[string]interface{} `json:"context"`
}

// PitchLane is the VISUAL-FIRST view substrate (CLAUDE.md HARD REQUIREMENT): a
// downsampled pitch curve (one point per retained frame) plus the note blobs, so a
// GUI can draw the waveform/pitch lane and let Jordan drag points by eye. Peaks is
// reserved for a downsampled waveform-amplitude envelope the audio stub can fill.
type PitchLane struct {
	Curve []LanePoint `json:"curve"`
	Notes []Note      `json:"notes"`
	Peaks []float64   `json:"peaks,omitempty"`
}

// LanePoint is one editable point of the pitch curve (time + pitch in MIDI-with-
// cents). Editable=true means the GUI may move it; a moved point becomes a
// Correction.
type LanePoint struct {
	T        float64 `json:"t"`
	MidiF    float64 `json:"midiF"`
	Voiced   float64 `json:"voiced"`
	Editable bool    `json:"editable"`
}

// Result is the full becky-hum analysis (the stdout JSON object, SPEC §4).
type Result struct {
	Tool          string       `json:"tool"`
	SchemaVersion int          `json:"schemaVersion"`
	Input         InputInfo    `json:"input"`
	Key           KeyResult    `json:"key"`
	Tempo         TempoResult  `json:"tempo"`
	Notes         []Note       `json:"notes"`
	Lane          PitchLane    `json:"lane"`
	Corrections   []Correction `json:"corrections"`
	Compose       string       `json:"compose"`
	Engine        string       `json:"engine"`
	Deterministic bool         `json:"deterministic"`
	Degraded      bool         `json:"degraded"`
	Reason        string       `json:"reason,omitempty"`
}

// InputInfo describes the analyzed audio (echoed for traceability).
type InputInfo struct {
	Wav             string  `json:"wav"`
	DurationSec     float64 `json:"durationSec"`
	NormalizeGainDb float64 `json:"normalizeGainDb"`
}
