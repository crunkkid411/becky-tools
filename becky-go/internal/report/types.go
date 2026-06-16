// Package report builds deterministic forensic case reports from the JSON
// sidecar outputs of the becky pipeline tools (transcript, events, identify,
// motion). It implements the "corroborate, then CONCLUDE" rule from
// FORENSIC-OUTPUT-PHILOSOPHY.md in code: identifications backed by ≥2
// independent signals are tagged DOCUMENTED; single-signal candidates are tagged
// CANDIDATE. No LLM, no network — same sidecars always produce the same report.
package report

// Report is the top-level forensic case report. It is the single JSON document
// produced by becky-report for one video or pipeline run.
type Report struct {
	Source      string        `json:"source"`          // video path or pipeline dir
	GeneratedAt string        `json:"generated_at"`    // RFC3339
	Duration    float64       `json:"duration"`        // clip length in seconds (0 = unknown)
	Entities    []Entity      `json:"entities"`        // all identified people/voices/faces
	Timeline    []Moment      `json:"timeline"`        // merged chronological view
	Conclusions []Finding     `json:"conclusions"`     // DOCUMENTED: ≥2-signal or high-confidence
	ReviewItems []Finding     `json:"review_required"` // CANDIDATE/ANALYSIS: needs human check
	Signals     SignalSummary `json:"signals"`         // per-tool summary
	Degraded    bool          `json:"degraded,omitempty"`
	Notes       []string      `json:"notes,omitempty"`
}

// Entity is one identified person with all their appearances and the corroboration
// evidence behind the identification.
type Entity struct {
	Name              string   `json:"name"`
	Type              string   `json:"type"` // voice | face | voice+face | location
	Confidence        float64  `json:"confidence"`
	CorroboratedBy    []string `json:"corroborated_by"`    // signals that agreed: voice, face, location
	CorroboratedCount int      `json:"corroborated_count"` // len(corroborated_by)
	Concluded         bool     `json:"concluded"`          // true when the corroboration rule is met
	Tag               string   `json:"tag"`                // DOCUMENTED | CANDIDATE
	SpeakerID         string   `json:"speaker_id,omitempty"`
	Appearances       []Span   `json:"appearances"` // time spans where this entity was observed
}

// Span is an inclusive time range in seconds.
type Span struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Moment is one entry in the merged chronological timeline. It captures a single
// observation from any tool at a specific time.
type Moment struct {
	Time        float64 `json:"time"`          // seconds, start of the moment
	End         float64 `json:"end,omitempty"` // seconds (for ranged moments like speech)
	Type        string  `json:"type"`          // speech | event | motion_burst | identification
	Source      string  `json:"source"`        // originating tool: transcript | events | motion | identify
	Description string  `json:"description"`   // human-readable, plain language
	Confidence  float64 `json:"confidence,omitempty"`
	Tag         string  `json:"tag"`                  // DOCUMENTED | ANALYSIS | CANDIDATE
	Speaker     string  `json:"speaker,omitempty"`    // resolved name or raw speaker_id
	SubSecond   bool    `json:"sub_second,omitempty"` // for motion_burst: <1s, missed by 1-fps sampling
}

// Finding is one forensic conclusion or review item assembled from multiple signals.
type Finding struct {
	What       string   `json:"what"`       // plain-language description
	When       string   `json:"when"`       // human-readable time: "0:13–0:22" or "0:15"
	WhenSec    float64  `json:"when_sec"`   // start time in seconds (for sorting/linking)
	Confidence float64  `json:"confidence"` // highest confidence among contributing signals
	Sources    []string `json:"sources"`    // which tool(s) produced the evidence
	Tag        string   `json:"tag"`        // DOCUMENTED | ANALYSIS | CANDIDATE
}

// SignalSummary captures the contribution from each tool for this report.
type SignalSummary struct {
	Transcript *TranscriptSig `json:"transcript,omitempty"`
	Events     *EventsSig     `json:"events,omitempty"`
	Identify   *IdentifySig   `json:"identify,omitempty"`
	Motion     *MotionSig     `json:"motion,omitempty"`
}

// TranscriptSig is the contribution summary from becky-transcribe.
type TranscriptSig struct {
	Present      bool    `json:"present"`
	SegmentCount int     `json:"segment_count"`
	Duration     float64 `json:"duration"`
	Model        string  `json:"model,omitempty"`
}

// EventsSig is the contribution summary from becky-events.
type EventsSig struct {
	Present    bool `json:"present"`
	EventCount int  `json:"event_count"`
}

// IdentifySig is the contribution summary from becky-identify.
type IdentifySig struct {
	Present           bool `json:"present"`
	IdentifiedCount   int  `json:"identified_count"`
	UnidentifiedCount int  `json:"unidentified_count"`
}

// MotionSig is the contribution summary from becky-motion.
type MotionSig struct {
	Present        bool `json:"present"`
	BurstCount     int  `json:"burst_count"`
	SubSecondCount int  `json:"sub_second_count"` // bursts that a 1-fps sampler would miss
}
