// Manifest types for becky-pipeline. The manifest is the tool's stdout JSON
// contract: per-video, per-step status with output paths and durations, plus
// run-level totals. All slices are initialised to [] (never null) so consumers
// can iterate without nil checks.
package main

// Manifest is the top-level becky-pipeline JSON document.
type Manifest struct {
	Tool       string        `json:"tool"`
	StartedAt  string        `json:"started_at"`  // RFC3339 UTC
	FinishedAt string        `json:"finished_at"` // RFC3339 UTC
	OutRoot    string        `json:"out_root"`
	BinDir     string        `json:"bin_dir"`
	Steps      []string      `json:"steps"` // the requested step set, in chain order
	KB         string        `json:"kb,omitempty"`
	Videos     []VideoResult `json:"videos"`
	Totals     Totals        `json:"totals"`
}

// VideoResult is the per-input row: where its outputs went and how each step did.
//
//	status: "ok"      — every step ok or skipped
//	        "partial" — at least one step failed (others still ran)
//	        "failed"  — could not even set up the video (e.g. mkdir failed)
type VideoResult struct {
	Input  string       `json:"input"`
	Stem   string       `json:"stem"`
	OutDir string       `json:"out_dir"`
	Status string       `json:"status"`
	Error  string       `json:"error,omitempty"`
	Steps  []StepResult `json:"steps"`
}

// StepResult is one tool invocation's outcome.
//
//	status: "ok"      — tool exited 0
//	        "skipped" — output already existed (resume) or dependency unmet
//	        "failed"  — tool exited non-zero / could not be launched
type StepResult struct {
	Name             string `json:"name"`
	Status           string `json:"status"`
	Output           string `json:"output,omitempty"` // primary output path produced/expected
	DurationMS       int64  `json:"duration_ms"`
	SkipReason       string `json:"skip_reason,omitempty"`       // why it was skipped
	Error            string `json:"error,omitempty"`             // failure detail (stderr tail)
	ExitCode         *int   `json:"exit_code,omitempty"`         // process exit code on failure
	TranscriptSource string `json:"transcript_source,omitempty"` // transcribe step: youtube-srt | parakeet
	Note             string `json:"note,omitempty"`              // human-readable extra detail (sidecar reuse, DB ingest)
}

// Totals summarises the whole run for quick triage.
type Totals struct {
	Videos       int `json:"videos"`
	StepsOK      int `json:"steps_ok"`
	StepsSkipped int `json:"steps_skipped"`
	StepsFailed  int `json:"steps_failed"`
}

// computeTotals aggregates step outcomes across all videos.
func computeTotals(videos []VideoResult) Totals {
	t := Totals{Videos: len(videos)}
	for _, v := range videos {
		for _, s := range v.Steps {
			switch s.Status {
			case "ok":
				t.StepsOK++
			case "skipped":
				t.StepsSkipped++
			case "failed":
				t.StepsFailed++
			}
		}
	}
	return t
}
