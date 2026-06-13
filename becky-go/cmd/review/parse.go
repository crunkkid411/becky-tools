// parse.go — defensive extraction of the annotation JSON array from raw LLM
// output. Models often wrap JSON in prose or ```json fences; this tolerates that
// and normalizes the parsed annotations.
package main

import (
	"encoding/json"
	"strings"
)

// parseAnnotations pulls a JSON array of annotations out of arbitrary model text.
// It strips code fences, then tries to unmarshal progressively smaller candidate
// substrings until one decodes into the annotation schema. Returns the (possibly
// empty) annotations and whether a JSON array was found at all.
func parseAnnotations(raw string) ([]Annotation, bool) {
	s := stripFences(strings.TrimSpace(raw))

	// Fast path: the whole thing is a clean array.
	if anns, ok := tryArray(s); ok {
		return normalize(anns), true
	}

	// Otherwise locate the outermost [...] span and try that.
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start >= 0 && end > start {
		if anns, ok := tryArray(s[start : end+1]); ok {
			return normalize(anns), true
		}
	}

	// Some models return a single object or {"annotations":[...]}; tolerate both.
	if anns, ok := tryObjectWrapper(s); ok {
		return normalize(anns), true
	}
	return nil, false
}

// tryArray attempts to decode s as a JSON array of annotations.
func tryArray(s string) ([]Annotation, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") {
		return nil, false
	}
	var anns []Annotation
	if err := json.Unmarshal([]byte(s), &anns); err != nil {
		return nil, false
	}
	return anns, true
}

// tryObjectWrapper handles {"annotations":[...]} and a bare single object.
func tryObjectWrapper(s string) ([]Annotation, bool) {
	objStart := strings.Index(s, "{")
	objEnd := strings.LastIndex(s, "}")
	if objStart < 0 || objEnd <= objStart {
		return nil, false
	}
	candidate := s[objStart : objEnd+1]

	var wrapper struct {
		Annotations []Annotation `json:"annotations"`
	}
	if err := json.Unmarshal([]byte(candidate), &wrapper); err == nil && wrapper.Annotations != nil {
		return wrapper.Annotations, true
	}

	var single Annotation
	if err := json.Unmarshal([]byte(candidate), &single); err == nil && single.Type != "" {
		return []Annotation{single}, true
	}
	return nil, false
}

// stripFences removes a leading/trailing markdown code fence (```json ... ```)
// if present, returning the inner content.
func stripFences(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// Drop the opening fence line (``` or ```json).
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		s = s[nl+1:]
	}
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// normalize clamps confidence to [0,1], defaults blank fields to safe values,
// and forces reviewed=false. It guarantees every emitted annotation satisfies
// the "rationale + confidence + significance present" requirement.
func normalize(anns []Annotation) []Annotation {
	out := make([]Annotation, 0, len(anns))
	for _, a := range anns {
		if a.Confidence < 0 {
			a.Confidence = 0
		}
		if a.Confidence > 1 {
			a.Confidence = 1
		}
		a.Confidence = round3(a.Confidence)
		a.SegmentStart = round3(a.SegmentStart)
		a.SegmentEnd = round3(a.SegmentEnd)
		if a.Type == "" {
			a.Type = "other"
		}
		if a.Significance == "" {
			a.Significance = "low"
		}
		if a.Rationale == "" {
			a.Rationale = "Model returned no rationale; flagged for human review."
		}
		a.Reviewed = false
		out = append(out, a)
	}
	return out
}

func round3(f float64) float64 { return float64(int(f*1000+0.5)) / 1000 }
