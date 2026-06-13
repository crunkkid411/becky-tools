package main

import (
	"encoding/json"
	"fmt"
)

// Output is the becky-motion JSON document written to stdout. It is a deterministic
// timeline of sub-second motion bursts so a descriptive model (becky-validate) can
// be aimed at the EXACT window instead of blind 1-fps sampling.
//
// Honesty contract (FORENSIC-OUTPUT-PHILOSOPHY.md): motion detection finds WHEN
// something moved, never WHAT moved or who did it. Every burst is a [CANDIDATE]
// window for review, carrying its own measured basis (motion score, frames) — it is
// not a conclusion about an action.
type Output struct {
	Tool           string        `json:"tool"`
	SourceFile     string        `json:"source_file"`
	SourceSHA256   string        `json:"source_sha256"`
	SourceFPS      float64       `json:"source_fps"` // true source frame rate (motion is measured at this rate)
	SampleFPS      float64       `json:"sample_fps"` // fps actually decoded for the diff (= source unless capped)
	DurationSec    float64       `json:"duration_sec"`
	AnalyzedWindow [2]float64    `json:"analyzed_window"` // [start, end] seconds actually scanned
	Method         string        `json:"method"`          // measurement method, stated plainly
	Threshold      ThresholdInfo `json:"threshold"`       // adaptive threshold + clip baseline (auditable)
	MotionBursts   []Burst       `json:"motion_bursts"`   // [] (never null) when calm
	BurstCount     int           `json:"burst_count"`
	AnalyzedAt     string        `json:"analyzed_at"` // RFC3339
	Notes          Notes         `json:"notes"`
}

// Burst is one localized sub-second (or longer) interval of elevated motion energy,
// at frame precision. The load-bearing value is window_start/window_end: the tight
// window a slow descriptive model should be pointed at.
type Burst struct {
	WindowStart     float64 `json:"window_start"` // seconds, frame-precise (true fps)
	WindowEnd       float64 `json:"window_end"`   // seconds, frame-precise (true fps)
	PeakTime        float64 `json:"peak_time"`    // seconds at maximum motion energy
	DurationSec     float64 `json:"duration_sec"`
	MotionScore     float64 `json:"motion_score"`      // normalized peak energy 0..1 within this clip
	MeanScore       float64 `json:"mean_score"`        // normalized mean energy over the burst
	FrameIndexStart int     `json:"frame_index_start"` // source-frame index (true fps)
	FrameIndexEnd   int     `json:"frame_index_end"`
	FrameIndexPeak  int     `json:"frame_index_peak"`
	SubSecond       bool    `json:"sub_second"`           // true if shorter than 1s (the kind 1-fps sampling misses)
	BetweenSamples  bool    `json:"between_1fps_samples"` // true if the burst falls entirely between two 1-fps grid points
	RecommendReview bool    `json:"recommend_review"`
	RouteTo         string  `json:"route_to"`      // hand-off hint for Tier 2 (becky-validate)
	ValidateArgs    string  `json:"validate_args"` // ready-to-use becky-validate --window/--fps hint
}

// ThresholdInfo records exactly how the burst threshold was derived from this clip's
// own motion baseline, so the determination is auditable and reproducible.
type ThresholdInfo struct {
	Mode        string  `json:"mode"`  // "adaptive" or "fixed"
	Value       float64 `json:"value"` // normalized threshold applied
	BaselineMed float64 `json:"baseline_median"`
	BaselineMAD float64 `json:"baseline_mad"` // median absolute deviation (robust spread)
	K           float64 `json:"k"`            // sensitivity multiplier (median + k*MAD)
}

// Notes carries the honesty disclaimer + any graceful-degradation messages.
type Notes struct {
	Honesty string `json:"honesty"`
	Skipped string `json:"skipped,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Warning string `json:"warning,omitempty"`
}

const honestyNote = "Motion bursts mark WHEN fast movement occurred (frame-precise, at true source fps) - " +
	"they do NOT identify WHAT moved or who did it. Each burst is a [CANDIDATE] window for human or " +
	"becky-validate review, with its measured motion score as the basis. Not a conclusion about any action."

// marshalIndent renders the output document as indented JSON with a trailing newline,
// matching the house convention used by the other becky tools' --output writers.
func marshalIndent(o Output) ([]byte, error) {
	b, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal output: %w", err)
	}
	return append(b, '\n'), nil
}
