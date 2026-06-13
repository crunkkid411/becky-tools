package main

import (
	"context"
	"strings"
	"testing"
)

func TestParseAnnotationsCleanArray(t *testing.T) {
	raw := `[{"type":"notable_moment","segment_start":1.0,"segment_end":2.0,"text":"hi","resolution":"x","rationale":"because","confidence":0.8,"significance":"high","reviewed":false}]`
	anns, ok := parseAnnotations(raw)
	if !ok || len(anns) != 1 {
		t.Fatalf("parseAnnotations clean = %+v ok=%v", anns, ok)
	}
	if anns[0].Type != "notable_moment" || anns[0].Confidence != 0.8 {
		t.Errorf("unexpected annotation: %+v", anns[0])
	}
}

func TestParseAnnotationsFenced(t *testing.T) {
	raw := "Here you go:\n```json\n[{\"type\":\"reference_resolution\",\"text\":\"my ex\",\"rationale\":\"r\",\"confidence\":0.5,\"significance\":\"medium\"}]\n```\nHope that helps!"
	anns, ok := parseAnnotations(raw)
	if !ok || len(anns) != 1 {
		t.Fatalf("parseAnnotations fenced = %+v ok=%v", anns, ok)
	}
	if anns[0].Reviewed {
		t.Error("reviewed should be normalized to false")
	}
}

func TestParseAnnotationsProseAroundArray(t *testing.T) {
	raw := `I reviewed the file. [{"type":"other","rationale":"r","confidence":2.0,"significance":"low"}] Done.`
	anns, ok := parseAnnotations(raw)
	if !ok || len(anns) != 1 {
		t.Fatalf("parseAnnotations prose = %+v ok=%v", anns, ok)
	}
	if anns[0].Confidence != 1.0 {
		t.Errorf("confidence should clamp to 1.0, got %v", anns[0].Confidence)
	}
}

func TestParseAnnotationsWrapperObject(t *testing.T) {
	raw := `{"annotations":[{"type":"notable_moment","rationale":"r","confidence":0.3,"significance":"low"}]}`
	anns, ok := parseAnnotations(raw)
	if !ok || len(anns) != 1 {
		t.Fatalf("parseAnnotations wrapper = %+v ok=%v", anns, ok)
	}
}

func TestParseAnnotationsEmptyArray(t *testing.T) {
	anns, ok := parseAnnotations("[]")
	if !ok {
		t.Fatal("empty array should parse ok")
	}
	if len(anns) != 0 {
		t.Errorf("expected 0 annotations, got %d", len(anns))
	}
}

func TestParseAnnotationsGarbage(t *testing.T) {
	if _, ok := parseAnnotations("I could not analyze this."); ok {
		t.Error("garbage prose should not parse as annotations")
	}
}

func TestNormalizeFillsRequiredFields(t *testing.T) {
	in := []Annotation{{Type: "", Significance: "", Rationale: "", Confidence: -5, Reviewed: true}}
	out := normalize(in)
	if out[0].Type != "other" || out[0].Significance != "low" || out[0].Rationale == "" {
		t.Errorf("normalize did not fill required fields: %+v", out[0])
	}
	if out[0].Confidence != 0 || out[0].Reviewed {
		t.Errorf("normalize did not clamp/force fields: %+v", out[0])
	}
}

func TestFirstCueWordBoundary(t *testing.T) {
	// "he" must NOT match inside "the".
	if _, ok := firstCue("like the the platforms encourage it"); ok {
		t.Error("firstCue matched a substring inside another word (the->he)")
	}
	// A genuine reference must match.
	if cue, ok := firstCue("my ex was harassing me"); !ok || cue != "my ex" {
		t.Errorf("firstCue(my ex) = %q ok=%v", cue, ok)
	}
	if _, ok := firstCue("she came to the house"); !ok {
		t.Error("firstCue should match 'she'")
	}
}

func TestStripFences(t *testing.T) {
	if got := stripFences("```json\n[1,2]\n```"); got != "[1,2]" {
		t.Errorf("stripFences = %q", got)
	}
	if got := stripFences("[1,2]"); got != "[1,2]" {
		t.Errorf("stripFences passthrough = %q", got)
	}
}

func TestMockBackendDeterministic(t *testing.T) {
	in := reviewInput{
		File: "v.mp4",
		Transcript: transcript{
			Segments: []transcriptSegment{
				{Start: 1, End: 2, Text: "my ex was here"},
				{Start: 3, End: 4, Text: "the platforms encourage it"}, // no real cue
			},
		},
		Events: eventsDoc{
			Events: []eventItem{
				{Type: "phone_call", Start: 6.038, End: 8.03, SpeakerID: "SPEAKER_01", Confidence: 0.75},
			},
		},
	}
	a, _ := mockBackend{}.Review(context.Background(), in)
	b, _ := mockBackend{}.Review(context.Background(), in)
	if len(a.Annotations) != len(b.Annotations) {
		t.Fatalf("mock not deterministic: %d vs %d", len(a.Annotations), len(b.Annotations))
	}
	// 1 event -> 1 notable_moment; 1 segment with a real cue -> 1 reference_resolution.
	if len(a.Annotations) != 2 {
		t.Fatalf("expected 2 annotations, got %d: %+v", len(a.Annotations), a.Annotations)
	}
	for _, an := range a.Annotations {
		if an.Rationale == "" || an.Significance == "" {
			t.Errorf("annotation missing required field: %+v", an)
		}
	}
	// The notable_moment must carry the event's real timestamps.
	var found bool
	for _, an := range a.Annotations {
		if an.Type == "notable_moment" && an.SegmentStart == 6.038 && an.SegmentEnd == 8.03 {
			found = true
		}
	}
	if !found {
		t.Error("mock did not preserve real event timestamps")
	}
}

func TestNewBackend(t *testing.T) {
	for _, name := range []string{"mock", "claude-code", "openrouter"} {
		if _, err := newBackend(name); err != nil {
			t.Errorf("newBackend(%q) error: %v", name, err)
		}
	}
	if _, err := newBackend("nope"); err == nil {
		t.Error("newBackend(nope) should error")
	}
}

func TestBuildUserPromptContainsContent(t *testing.T) {
	tr := transcript{Duration: 45.2, Language: "en", Segments: []transcriptSegment{{Start: 1, End: 2, Text: "hello world"}}}
	ev := eventsDoc{Events: []eventItem{{Type: "phone_call", Start: 6, End: 8, Description: "short turn"}}}
	p := buildUserPrompt("CASE-CTX-MARKER", tr, ev, "v.mp4")
	for _, want := range []string{"CASE-CTX-MARKER", "hello world", "phone_call", "v.mp4"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}
