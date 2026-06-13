// types.go — input schemas (becky-transcribe / becky-events JSON) and the
// becky-review output contract for the per-media context review tool.
package main

// ---- Upstream input schemas (subset of fields we consume) ----

// transcriptSegment mirrors one caption-sized segment in becky-transcribe JSON.
type transcriptSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// transcript mirrors the becky-transcribe JSON contract (we read the fields we
// need; unknown fields are ignored).
type transcript struct {
	File     string              `json:"file"`
	Duration float64             `json:"duration"`
	Model    string              `json:"model"`
	Language string              `json:"language"`
	Text     string              `json:"text"`
	Segments []transcriptSegment `json:"segments"`
}

// eventItem mirrors one event in becky-events JSON.
type eventItem struct {
	Type        string  `json:"type"`
	Start       float64 `json:"start"`
	End         float64 `json:"end"`
	Duration    float64 `json:"duration"`
	SpeakerID   string  `json:"speaker_id"`
	Timestamp   float64 `json:"timestamp"`
	Confidence  float64 `json:"confidence"`
	Description string  `json:"description"`
}

// eventsDoc mirrors the becky-events JSON contract.
type eventsDoc struct {
	File     string            `json:"file"`
	Duration float64           `json:"duration"`
	Events   []eventItem       `json:"events"`
	Notes    map[string]string `json:"notes"`
}

// ---- Output contract (exact schema from the task spec) ----

// Annotation is one context observation produced by the review backend. Every
// annotation carries a rationale, a confidence (0-1), and a significance level;
// these are required by the spec (no annotation without reasoning).
type Annotation struct {
	Type         string  `json:"type"`          // reference_resolution | notable_moment | ...
	SegmentStart float64 `json:"segment_start"` // seconds (clip-relative)
	SegmentEnd   float64 `json:"segment_end"`   // seconds (clip-relative)
	Text         string  `json:"text"`          // the transcript/event text the note is about
	Resolution   string  `json:"resolution"`    // who/what it resolves to, or the finding
	Rationale    string  `json:"rationale"`     // why — required, never empty
	Confidence   float64 `json:"confidence"`    // 0.0 - 1.0
	Significance string  `json:"significance"`  // low | medium | high
	Reviewed     bool    `json:"reviewed"`      // human-review flag (always false on emit)
}

// Output is the becky-review JSON contract emitted per media file.
type Output struct {
	File        string       `json:"file"`
	ReviewedAt  string       `json:"reviewed_at"` // RFC3339 UTC
	Backend     string       `json:"backend"`
	Model       string       `json:"model"`
	Annotations []Annotation `json:"annotations"`
	// Note carries a graceful-degradation marker (e.g. backend failed / skipped)
	// without breaking the schema. Omitted on a clean run.
	Note string `json:"note,omitempty"`
}
