package report

import (
	"encoding/json"
	"fmt"
	"os"
)

// Sidecars holds the parsed JSON sidecar outputs from each pipeline tool for one
// clip. Fields are nil when a sidecar was not provided or not found.
type Sidecars struct {
	Transcript *transcriptOutput
	Events     *eventsOutput
	Identify   *identifyOutput
	Motion     *motionOutput
}

// LoadSidecars reads each non-empty path and parses it. Missing files are
// silently skipped (they just leave the corresponding field nil); a file that
// exists but cannot be parsed is reported as an error.
func LoadSidecars(transcriptPath, eventsPath, identifyPath, motionPath string) (Sidecars, []string, error) {
	var s Sidecars
	var notes []string

	if transcriptPath != "" {
		t, err := loadJSON[transcriptOutput](transcriptPath)
		if err != nil {
			return s, notes, fmt.Errorf("transcript: %w", err)
		}
		s.Transcript = &t
	}
	if eventsPath != "" {
		e, err := loadJSON[eventsOutput](eventsPath)
		if err != nil {
			return s, notes, fmt.Errorf("events: %w", err)
		}
		s.Events = &e
	}
	if identifyPath != "" {
		i, err := loadJSON[identifyOutput](identifyPath)
		if err != nil {
			return s, notes, fmt.Errorf("identify: %w", err)
		}
		s.Identify = &i
	}
	if motionPath != "" {
		m, err := loadJSON[motionOutput](motionPath)
		if err != nil {
			return s, notes, fmt.Errorf("motion: %w", err)
		}
		s.Motion = &m
	}

	if s.Transcript == nil && s.Events == nil && s.Identify == nil && s.Motion == nil {
		notes = append(notes, "no sidecar files found — pass at least one of --transcript/--events/--identify/--motion")
	}
	return s, notes, nil
}

// loadJSON reads a file and unmarshals it into T.
func loadJSON[T any](path string) (T, error) {
	var zero T
	b, err := os.ReadFile(path)
	if err != nil {
		return zero, fmt.Errorf("read %s: %w", path, err)
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return zero, fmt.Errorf("parse %s: %w", path, err)
	}
	return v, nil
}

// --- JSON shapes that mirror the actual sidecar files ---

// transcriptOutput mirrors becky-transcribe's JSON output contract.
type transcriptOutput struct {
	File     string              `json:"file"`
	Duration float64             `json:"duration"`
	Model    string              `json:"model"`
	Segments []transcriptSegment `json:"segments"`
}

type transcriptSegment struct {
	Start         float64 `json:"start"`
	End           float64 `json:"end"`
	Text          string  `json:"text"`
	LowConfidence bool    `json:"low_confidence,omitempty"`
}

// eventsOutput mirrors becky-events' JSON output contract.
type eventsOutput struct {
	File     string        `json:"file"`
	Duration float64       `json:"duration"`
	Events   []eventsEvent `json:"events"`
}

type eventsEvent struct {
	Type        string  `json:"type"`
	Start       float64 `json:"start"`
	End         float64 `json:"end"`
	Duration    float64 `json:"duration,omitempty"`
	SpeakerID   string  `json:"speaker_id,omitempty"`
	Confidence  float64 `json:"confidence"`
	Description string  `json:"description"`
}

// identifyOutput mirrors becky-identify's JSON output contract.
type identifyOutput struct {
	File            string            `json:"file"`
	Identifications []identifyEntry   `json:"identifications"`
	Unidentified    []identifyUnknown `json:"unidentified"`
}

type identifyEntry struct {
	Type           string          `json:"type"`
	SpeakerID      string          `json:"speaker_id,omitempty"`
	Name           string          `json:"name"`
	Confidence     float64         `json:"confidence"`
	Match          string          `json:"match"`
	CorroboratedBy []string        `json:"corroborated_by,omitempty"`
	Segments       []identifySpan  `json:"segments,omitempty"`
	Frames         []identifyFrame `json:"frames,omitempty"`
}

type identifySpan struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

type identifyFrame struct {
	Timestamp float64 `json:"timestamp"`
}

type identifyUnknown struct {
	Type        string  `json:"type"`
	SpeakerID   string  `json:"speaker_id,omitempty"`
	Description string  `json:"description"`
	Confidence  float64 `json:"confidence"`
	Candidate   string  `json:"candidate,omitempty"`
}

// motionOutput mirrors becky-motion's JSON output contract.
type motionOutput struct {
	SourceFile   string        `json:"source_file"`
	DurationSec  float64       `json:"duration_sec"`
	MotionBursts []motionBurst `json:"motion_bursts"`
	BurstCount   int           `json:"burst_count"`
}

type motionBurst struct {
	WindowStart     float64 `json:"window_start"`
	WindowEnd       float64 `json:"window_end"`
	PeakTime        float64 `json:"peak_time"`
	MotionScore     float64 `json:"motion_score"`
	SubSecond       bool    `json:"sub_second"`
	BetweenSamples  bool    `json:"between_1fps_samples"`
	RecommendReview bool    `json:"recommend_review"`
}
