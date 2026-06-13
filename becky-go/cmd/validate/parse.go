// parse.go — defensive extraction of the observation JSON array from raw model
// output. Gemma 4 (like any LLM) may wrap JSON in prose or ```json fences; this
// tolerates that and normalizes the parsed observations.
package main

import (
	"encoding/json"
	"strings"
)

// parseObservations pulls a JSON array of observations out of arbitrary model
// text. It strips code fences, then tries progressively smaller candidate spans
// until one decodes into the observation schema. Returns the (possibly empty)
// observations and whether a JSON array was found at all.
func parseObservations(raw string) ([]Observation, bool) {
	s := stripFences(strings.TrimSpace(raw))

	if obs, ok := tryArray(s); ok {
		return normalize(obs), true
	}
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start >= 0 && end > start {
		if obs, ok := tryArray(s[start : end+1]); ok {
			return normalize(obs), true
		}
	}
	if obs, ok := tryObjectWrapper(s); ok {
		return normalize(obs), true
	}
	return nil, false
}

// tryArray attempts to decode s as a JSON array of observations.
func tryArray(s string) ([]Observation, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") {
		return nil, false
	}
	var obs []Observation
	if err := json.Unmarshal([]byte(s), &obs); err != nil {
		return nil, false
	}
	return obs, true
}

// tryObjectWrapper handles {"observations":[...]} and a bare single object.
func tryObjectWrapper(s string) ([]Observation, bool) {
	objStart := strings.Index(s, "{")
	objEnd := strings.LastIndex(s, "}")
	if objStart < 0 || objEnd <= objStart {
		return nil, false
	}
	candidate := s[objStart : objEnd+1]

	var wrapper struct {
		Observations []Observation `json:"observations"`
	}
	if err := json.Unmarshal([]byte(candidate), &wrapper); err == nil && wrapper.Observations != nil {
		return wrapper.Observations, true
	}

	var single Observation
	if err := json.Unmarshal([]byte(candidate), &single); err == nil && hasContent(single) {
		return []Observation{single}, true
	}
	return nil, false
}

// hasContent reports whether a single parsed observation carries any signal.
func hasContent(o Observation) bool {
	return o.Type != "" || o.Finding != "" || o.Visual != "" || o.AudioTone != "" || o.Content != ""
}

// stripFences removes a leading/trailing markdown code fence if present.
func stripFences(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		s = s[nl+1:]
	}
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// normalize clamps confidence to [0,1], defaults blank fields to safe values,
// rounds timestamps, and forces reviewed=false. It guarantees every emitted
// observation satisfies the "finding + rationale + confidence + significance
// present" requirement.
func normalize(obs []Observation) []Observation {
	out := make([]Observation, 0, len(obs))
	for _, o := range obs {
		if o.Confidence < 0 {
			o.Confidence = 0
		}
		if o.Confidence > 1 {
			o.Confidence = 1
		}
		o.Confidence = round3(o.Confidence)
		o.SegmentStart = round3(o.SegmentStart)
		o.SegmentEnd = round3(o.SegmentEnd)
		if o.Type == "" {
			o.Type = "cross_modal"
		}
		if o.Significance == "" {
			o.Significance = "low"
		}
		if o.Rationale == "" {
			o.Rationale = "Model returned no rationale; flagged for human review."
		}
		if o.Finding == "" {
			o.Finding = "Model returned no explicit finding; flagged for human review."
		}
		if o.Frames == nil {
			o.Frames = []string{} // emit [] not null
		}
		o.Reviewed = false
		out = append(out, o)
	}
	return out
}

// anyMismatch reports whether any observation explicitly flags a tone-content
// mismatch (tone_content_match == false). It drives the top-level
// tone_vs_content_flag.
func anyMismatch(obs []Observation) bool {
	for _, o := range obs {
		if o.ToneContentMatch != nil && !*o.ToneContentMatch {
			return true
		}
	}
	return false
}

func round3(f float64) float64 { return float64(int(f*1000+0.5)) / 1000 }

func boolPtr(b bool) *bool { return &b }
