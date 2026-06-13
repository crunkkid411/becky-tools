package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSegmentizeEmptyIsNonNil verifies the zero-word case yields a non-nil,
// empty slice so the JSON "segments" field marshals as [] rather than null.
func TestSegmentizeEmptyIsNonNil(t *testing.T) {
	segs := segmentize(nil)
	if segs == nil {
		t.Fatal("segmentize(nil) returned a nil slice; want non-nil empty slice")
	}
	if len(segs) != 0 {
		t.Fatalf("segmentize(nil) len = %d; want 0", len(segs))
	}
}

// TestEmptyOutputMarshalsArrays verifies a zero-word transcript marshals
// "words" and "segments" as [] (not null) — the bug class this fix addresses.
func TestEmptyOutputMarshalsArrays(t *testing.T) {
	var helperWords []Word // nil, as the helper emits when there is no speech
	words := helperWords
	if words == nil {
		words = []Word{}
	}
	o := Output{
		File:     "x.mp4",
		Words:    words,
		Segments: segmentize(helperWords),
	}
	b, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(b)
	if !strings.Contains(js, `"words":[]`) {
		t.Errorf("words not emitted as []: %s", js)
	}
	if !strings.Contains(js, `"segments":[]`) {
		t.Errorf("segments not emitted as []: %s", js)
	}
	if strings.Contains(js, "null") {
		t.Errorf("output contains null array(s): %s", js)
	}
}
