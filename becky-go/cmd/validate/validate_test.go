package main

import (
	"context"
	"strings"
	"testing"

	"becky-go/internal/config"
)

func TestParseObservationsCleanArray(t *testing.T) {
	raw := `[{"type":"cross_modal","segment_start":1.0,"segment_end":2.0,"question":"q","visual":"v","audio_tone":"t","content":"c","finding":"f","tone_content_match":false,"confidence":0.8,"significance":"high","rationale":"because","reviewed":false}]`
	obs, ok := parseObservations(raw)
	if !ok || len(obs) != 1 {
		t.Fatalf("parseObservations clean = %+v ok=%v", obs, ok)
	}
	if obs[0].ToneContentMatch == nil || *obs[0].ToneContentMatch {
		t.Errorf("tone_content_match should be false, got %v", obs[0].ToneContentMatch)
	}
	if obs[0].Confidence != 0.8 {
		t.Errorf("confidence = %v", obs[0].Confidence)
	}
}

func TestParseObservationsFenced(t *testing.T) {
	raw := "Sure:\n```json\n[{\"type\":\"cross_modal\",\"finding\":\"x\",\"rationale\":\"r\",\"confidence\":0.5,\"significance\":\"medium\"}]\n```\nDone."
	obs, ok := parseObservations(raw)
	if !ok || len(obs) != 1 {
		t.Fatalf("parseObservations fenced = %+v ok=%v", obs, ok)
	}
	if obs[0].Reviewed {
		t.Error("reviewed should normalize to false")
	}
}

func TestParseObservationsClampsConfidence(t *testing.T) {
	raw := `I looked. [{"type":"cross_modal","finding":"f","rationale":"r","confidence":2.5,"significance":"low"}] ok.`
	obs, ok := parseObservations(raw)
	if !ok || len(obs) != 1 {
		t.Fatalf("parse prose-wrapped = %+v ok=%v", obs, ok)
	}
	if obs[0].Confidence != 1.0 {
		t.Errorf("confidence should clamp to 1.0, got %v", obs[0].Confidence)
	}
}

func TestParseObservationsWrapperObject(t *testing.T) {
	raw := `{"observations":[{"type":"audio","finding":"f","rationale":"r","confidence":0.3,"significance":"low"}]}`
	obs, ok := parseObservations(raw)
	if !ok || len(obs) != 1 {
		t.Fatalf("parse wrapper = %+v ok=%v", obs, ok)
	}
}

func TestParseObservationsEmptyArray(t *testing.T) {
	obs, ok := parseObservations("[]")
	if !ok {
		t.Fatal("empty array should parse ok")
	}
	if len(obs) != 0 {
		t.Errorf("expected 0 observations, got %d", len(obs))
	}
}

func TestParseObservationsGarbage(t *testing.T) {
	if _, ok := parseObservations("I could not analyze the clip."); ok {
		t.Error("garbage prose should not parse as observations")
	}
}

func TestParseObservationsEmptyOutput(t *testing.T) {
	// The NaN-logits failure mode yields empty/whitespace output, which must not
	// parse as a valid (empty) observation array.
	if _, ok := parseObservations("   \n  "); ok {
		t.Error("empty/whitespace model output must not parse as observations")
	}
}

func TestNormalizeFillsRequiredFields(t *testing.T) {
	in := []Observation{{Type: "", Significance: "", Rationale: "", Finding: "", Confidence: -5, Reviewed: true}}
	out := normalize(in)
	if out[0].Type != "cross_modal" || out[0].Significance != "low" {
		t.Errorf("normalize did not fill defaults: %+v", out[0])
	}
	if out[0].Rationale == "" || out[0].Finding == "" {
		t.Errorf("normalize did not fill finding/rationale: %+v", out[0])
	}
	if out[0].Confidence != 0 || out[0].Reviewed {
		t.Errorf("normalize did not clamp/force: %+v", out[0])
	}
}

func TestAnyMismatch(t *testing.T) {
	yes := []Observation{{ToneContentMatch: boolPtr(false)}}
	no := []Observation{{ToneContentMatch: boolPtr(true)}, {ToneContentMatch: nil}}
	if !anyMismatch(yes) {
		t.Error("anyMismatch should be true when an observation flags a mismatch")
	}
	if anyMismatch(no) {
		t.Error("anyMismatch should be false when nothing flags a mismatch")
	}
}

func TestStripFences(t *testing.T) {
	if got := stripFences("```json\n[1]\n```"); got != "[1]" {
		t.Errorf("stripFences = %q", got)
	}
	if got := stripFences("[1]"); got != "[1]" {
		t.Errorf("stripFences passthrough = %q", got)
	}
}

func TestNewBackend(t *testing.T) {
	for _, name := range []string{"mock", "gemma4-local", "fusion"} {
		if _, err := newBackend(name); err != nil {
			t.Errorf("newBackend(%q) error: %v", name, err)
		}
	}
	if _, err := newBackend("openrouter"); err == nil {
		t.Error("newBackend(openrouter) should error (validate is offline)")
	}
}

func TestMockBackendDeterministic(t *testing.T) {
	in := validateInput{
		File:      "clip.mp4",
		Questions: defaultQuestions,
		Transcript: &transcript{Segments: []transcriptSegment{
			{Start: 142, End: 146, Text: "everything was completely normal"},
		}},
		Events: &eventsDoc{Events: []eventItem{
			{Type: "second_speaker", Start: 153, End: 158, Confidence: 0.7},
		}},
	}
	a, _ := mockBackend{}.Validate(context.Background(), config.Config{}, in)
	b, _ := mockBackend{}.Validate(context.Background(), config.Config{}, in)
	if len(a.Observations) != len(b.Observations) {
		t.Fatalf("mock not deterministic: %d vs %d", len(a.Observations), len(b.Observations))
	}
	// One observation per default question + one per event.
	want := len(defaultQuestions) + 1
	if len(a.Observations) != want {
		t.Fatalf("expected %d observations (%d questions + 1 event), got %d",
			want, len(defaultQuestions), len(a.Observations))
	}
	for _, o := range a.Observations {
		if o.Finding == "" || o.Rationale == "" || o.Significance == "" {
			t.Errorf("observation missing required field: %+v", o)
		}
	}
	// The event observation must carry the event's real timestamps.
	var found bool
	for _, o := range a.Observations {
		if o.Type == "visual" && o.SegmentStart == 153 && o.SegmentEnd == 158 {
			found = true
		}
	}
	if !found {
		t.Error("mock did not preserve real event timestamps")
	}
}

func TestBuildUserPromptContainsQuestions(t *testing.T) {
	p := buildUserPrompt("CTX-MARKER", []string{"Does tone match content?"})
	for _, want := range []string{"CTX-MARKER", "Does tone match content?", "JSON array"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestBuildContextPreamble(t *testing.T) {
	tr := &transcript{Segments: []transcriptSegment{{Start: 1, End: 2, Text: "hello"}}}
	ev := &eventsDoc{Events: []eventItem{{Type: "phone_call", Start: 6, End: 8, Description: "short turn"}}}
	id := &identifyDoc{Speakers: []identifyName{{SpeakerID: "SPEAKER_01", Name: "Alice"}}}
	p := buildContextPreamble(tr, ev, id)
	for _, want := range []string{"hello", "phone_call", "SPEAKER_01 = Alice"} {
		if !strings.Contains(p, want) {
			t.Errorf("preamble missing %q", want)
		}
	}
	// nil inputs must not panic and yield empty.
	if got := buildContextPreamble(nil, nil, nil); got != "" {
		t.Errorf("empty preamble = %q", got)
	}
}

func TestDisclaimerPresent(t *testing.T) {
	if !strings.Contains(Disclaimer, "candidate, not conclusion") {
		t.Errorf("disclaimer must say candidate, not conclusion: %q", Disclaimer)
	}
}
