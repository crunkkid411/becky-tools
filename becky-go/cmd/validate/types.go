// types.go — input schemas (becky-transcribe / becky-events / becky-identify
// JSON, all optional context) and the becky-validate output contract for the
// local audio-visual validation tool.
package main

// ---- Upstream input schemas (subset of fields we consume; all optional) ----

// transcriptSegment mirrors one caption-sized segment in becky-transcribe JSON.
type transcriptSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// transcript mirrors the becky-transcribe JSON contract (read fields we need).
type transcript struct {
	File     string              `json:"file"`
	Duration float64             `json:"duration"`
	Language string              `json:"language"`
	Text     string              `json:"text"`
	Segments []transcriptSegment `json:"segments"`
}

// eventItem mirrors one event in becky-events JSON.
type eventItem struct {
	Type        string  `json:"type"`
	Start       float64 `json:"start"`
	End         float64 `json:"end"`
	SpeakerID   string  `json:"speaker_id"`
	Timestamp   float64 `json:"timestamp"`
	Confidence  float64 `json:"confidence"`
	Description string  `json:"description"`
}

// eventsDoc mirrors the becky-events JSON contract.
type eventsDoc struct {
	File   string      `json:"file"`
	Events []eventItem `json:"events"`
}

// identifyName mirrors one resolved speaker/face in becky-identify JSON. The
// upstream schema varies; we read the common name + label fields and ignore the
// rest.
type identifyName struct {
	SpeakerID string  `json:"speaker_id"`
	Name      string  `json:"name"`
	Label     string  `json:"label"`
	Conf      float64 `json:"confidence"`
}

// identifyDoc mirrors the becky-identify JSON contract (subset).
type identifyDoc struct {
	File     string         `json:"file"`
	Speakers []identifyName `json:"speakers"`
	Names    []identifyName `json:"names"`
}

// ---- Output contract ----

// Observation is one cross-modal finding. Every observation pairs what was
// SEEN, HEARD (tone), and SAID (content) with a finding + the headline
// tone_content_match flag, a calibrated confidence, and a rationale. These are
// candidates for a human, never conclusions (reviewed is always false).
type Observation struct {
	Type             string  `json:"type"`               // cross_modal | visual | audio | ...
	SegmentStart     float64 `json:"segment_start"`      // clip-absolute seconds
	SegmentEnd       float64 `json:"segment_end"`        // clip-absolute seconds
	Question         string  `json:"question"`           // the question this answers
	Visual           string  `json:"visual"`             // what is visible
	AudioTone        string  `json:"audio_tone"`         // the speaker's tone / prosody
	Content          string  `json:"content"`            // what is said (the words)
	Finding          string  `json:"finding"`            // the cross-modal finding
	ToneContentMatch *bool   `json:"tone_content_match"` // headline flag (nil = undetermined)
	Confidence       float64 `json:"confidence"`         // 0.0 - 1.0
	Significance     string  `json:"significance"`       // low | medium | high
	// Frames lists the extracted frame image(s) that support this observation.
	// It is REQUIRED for physical_contact / possible_contact so a human can open
	// the exact frame and verify the contact; an unlinkable contact claim is
	// downgraded (see gateContactFrames). Always emitted as [] (never null).
	Frames    []string `json:"frames"`
	Rationale string   `json:"rationale"` // WHY — never empty
	Reviewed  bool     `json:"reviewed"`  // human-review flag (always false on emit)
}

// Output is the becky-validate JSON contract emitted per clip.
type Output struct {
	File        string  `json:"file"`
	ValidatedAt string  `json:"validated_at"` // RFC3339 UTC
	Backend     string  `json:"backend"`
	Model       string  `json:"model"`
	Disclaimer  string  `json:"disclaimer"`   // load-bearing: candidate, not conclusion
	WindowStart float64 `json:"window_start"` // seconds into clip where analysis began (0 = beginning)
	WindowSec   float64 `json:"window_sec"`
	FPS         float64 `json:"fps"`
	// MotionTargeted is set when --motion targeted this window at a detected burst.
	// Omitted (false) when running with the default whole-clip window.
	MotionTargeted    bool          `json:"motion_targeted,omitempty"`
	Observations      []Observation `json:"observations"`
	ToneVsContentFlag bool          `json:"tone_vs_content_flag"` // true if any observation flags a mismatch
	// Note carries a graceful-degradation marker (model missing/failed, NaN
	// output, etc.) without breaking the schema. Omitted on a clean run.
	Note string `json:"note,omitempty"`
}

// Disclaimer is the exact, required text on every output. It is load-bearing:
// the tool produces triage candidates for a detective, never evidence.
const Disclaimer = "AI ANALYSIS — candidate, not conclusion. Requires human verification."
