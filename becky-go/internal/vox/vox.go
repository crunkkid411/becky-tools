// Package vox is becky's multi-take vocal-alignment layer (SPEC-BECKY-VOX.md):
// match N recorded takes to a guide in TIMING (DTW warp of onsets) and PITCH
// (note-segmented, formant-preserving correction — stubbed), and COMP the best bits
// across takes. The differentiator is the TRUST MODEL, not the DSP: becky ANALYZES
// (warp map + notes + per-phrase confidence) and only CONCLUDES where corroborated,
// FLAGGING the rest for per-phrase human approval — never a black-box global knob.
//
// Everything in this package is pure, deterministic Go operating on already-extracted
// feature sequences. The audio→features boundary (WAV decode, F0 tracking) and the
// audio render boundary (formant-preserving pitch/time shift) are documented stubs
// (see Aligner / Renderer in features.go) the local agent wires to real DSP. Same
// features in => same warp map / notes / comp out.
package vox

// SchemaVersion is the stdout JSON contract version (SPEC §4.2).
const SchemaVersion = 1

// FeatureFrame is one frame of a take's fused feature vector for DTW (SPEC §2.2):
// an onset-strength value, an MFCC-summary scalar, and a chroma-summary scalar, all
// normalized. Keeping the fused frame as named scalars makes the cost function
// explainable and the tests synthetic-array-driven (no audio).
type FeatureFrame struct {
	T      float64 `json:"t"`      // seconds from take start
	Onset  float64 `json:"onset"`  // onset/energy strength (consonant attacks)
	MFCC   float64 `json:"mfcc"`   // phoneme/spectral-shape summary
	Chroma float64 `json:"chroma"` // pitched-vowel content
}

// WarpStep is one index pair on the DTW warp path: guide frame g aligned to alt
// frame a (SPEC §2.1). The path is monotonic with the first/last frames pinned.
type WarpStep struct {
	G int `json:"g"`
	A int `json:"a"`
}

// WarpEntry is the human-facing, per-syllable warp record (SPEC §2.3): how far a
// syllable moved and how confident becky is. The canvas draws these as tie-lines
// (green = high confidence, amber = flagged).
type WarpEntry struct {
	GuideOnsetMs float64 `json:"guideOnsetMs"`
	AltOnsetMs   float64 `json:"altOnsetMs"`
	ShiftMs      float64 `json:"shiftMs"`
	LocalStretch float64 `json:"localStretch"`
	Confidence   float64 `json:"confidence"`
	Syllable     int     `json:"syllable"`
	Flagged      bool    `json:"flagged"`
}

// VoxNote is one detected/aligned note (the Melodyne "blob"), with the proposed
// pitch move (SPEC §3). DetectedHz is the take's pitch; TargetHz is the guide's
// note (stack mode) or the nearest scale tone (tune mode); MoveCents is the
// proposed shift. EngineUsed records which formant-preserving engine WOULD render
// it (world | psola | rubberband) so the result is auditable. Flagged = becky is
// not confident enough to apply silently.
type VoxNote struct {
	StartMs    float64 `json:"startMs"`
	EndMs      float64 `json:"endMs"`
	DetectedHz float64 `json:"detectedHz"`
	TargetHz   float64 `json:"targetHz"`
	MoveCents  float64 `json:"moveCents"`
	EngineUsed string  `json:"engineUsed"`
	Confidence float64 `json:"confidence"`
	Flagged    bool    `json:"flagged"`
}

// Phrase is a section spanning several syllables/notes, with the per-phrase metrics
// comp + approval are built on (SPEC §4.2, §6). TimingTightness and PitchStability
// are 0..1; Confidence is the fused per-phrase number.
type Phrase struct {
	StartMs         float64 `json:"startMs"`
	EndMs           float64 `json:"endMs"`
	TimingTightness float64 `json:"timingTightness"`
	PitchStability  float64 `json:"pitchStability"`
	Confidence      float64 `json:"confidence"`
}

// Correction is one row of the corrections log — the preference-learning substrate
// (CLAUDE.md HARD REQUIREMENT). When becky proposes a move (warp shift or pitch
// move) and Jordan overrides it by eye, a Correction records {auto value, his
// corrected value, context}. Kind is "warp.shiftMs" or "note.moveCents"; Auto is
// becky's value; Corrected is Jordan's (nil until he edits); Context carries the
// surrounding facts a future model learns his preferences from.
type Correction struct {
	Kind      string                 `json:"kind"`
	Index     int                    `json:"index"`
	Auto      float64                `json:"auto"`
	Corrected *float64               `json:"corrected"`
	Reason    string                 `json:"reason"`
	Context   map[string]interface{} `json:"context"`
}

// CompChoice is one phrase's comp decision: which take won and why, plus the
// runner-up — a declared, repeatable artifact, not a mouse-history (SPEC §6).
type CompChoice struct {
	Phrase      int     `json:"phrase"`
	StartMs     float64 `json:"startMs"`
	EndMs       float64 `json:"endMs"`
	ChosenTake  int     `json:"chosenTake"`
	Score       float64 `json:"score"`
	RunnerUp    int     `json:"runnerUp"`
	RunnerScore float64 `json:"runnerScore"`
}

// AnalysisResult is the becky-vox align/analyze output (SPEC §4.2). Analysis is
// SEPARATE from any rendered audio: a human (or canvas) approves per phrase BEFORE
// anything is baked. AlignedWav/AlignedMid are paths the renderer (stub) fills.
type AnalysisResult struct {
	Tool          string       `json:"tool"`
	SchemaVersion int          `json:"schemaVersion"`
	Mode          string       `json:"mode"`
	Guide         string       `json:"guide"`
	Alt           string       `json:"alt"`
	WarpMap       []WarpEntry  `json:"warpMap"`
	Notes         []VoxNote    `json:"notes"`
	Phrases       []Phrase     `json:"phrases"`
	Corrections   []Correction `json:"corrections"`
	AlignedWav    string       `json:"alignedWav,omitempty"`
	AlignedMid    string       `json:"alignedMid,omitempty"`
	Deterministic bool         `json:"deterministic"`
	Degraded      bool         `json:"degraded"`
	Reason        string       `json:"reason,omitempty"`
}

// CompResult is the becky-vox comp output (SPEC §6): the per-phrase decision list.
type CompResult struct {
	Tool          string       `json:"tool"`
	SchemaVersion int          `json:"schemaVersion"`
	Metric        string       `json:"metric"`
	Takes         []string     `json:"takes"`
	Choices       []CompChoice `json:"choices"`
	Out           string       `json:"out,omitempty"`
	Deterministic bool         `json:"deterministic"`
	Degraded      bool         `json:"degraded"`
	Reason        string       `json:"reason,omitempty"`
}
