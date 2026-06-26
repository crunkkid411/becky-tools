package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"becky-go/internal/forensicrun"
)

// --forensic must reuse the transcript we already produced (no second ASR pass) and thread the
// subject/speakers through to the resolver, then embed the corroborated report.
func TestBuildForensic_ReusesTranscript_AndEmbeds(t *testing.T) {
	var gotSubject, gotKB string
	var gotSpeakers int
	var gotTranscribe []byte
	orig := forensicResolve
	defer func() { forensicResolve = orig }()
	forensicResolve = func(ctx context.Context, file, subject, kb string, speakers int, transcribeJSON []byte) forensicrun.ForensicReport {
		gotSubject, gotKB, gotSpeakers, gotTranscribe = subject, kb, speakers, transcribeJSON
		return forensicrun.ForensicReport{File: file, Subject: subject, Plan: []string{"becky-transcribe"}}
	}

	out := Output{File: "clip.mp4", Segments: []Segment{{Start: 10, End: 12, Text: "the cat is here"}}}
	rep := buildForensic("clip.mp4", "cat", "kb-final", 2, out)

	if rep == nil || rep.File != "clip.mp4" || rep.Subject != "cat" {
		t.Fatalf("buildForensic did not embed the resolution: %+v", rep)
	}
	if gotSubject != "cat" || gotSpeakers != 2 || gotKB != "kb-final" {
		t.Errorf("subject/kb/speakers not threaded through: %q/%q/%d", gotSubject, gotKB, gotSpeakers)
	}
	if !strings.Contains(string(gotTranscribe), "the cat is here") {
		t.Errorf("our transcript must be reused (no second ASR pass); got %s", gotTranscribe)
	}
}

// The forensic block is OPT-IN: a plain transcribe output must not contain it (existing consumers
// unchanged), and a --forensic output must.
func TestOutput_ForensicOmittedByDefault(t *testing.T) {
	b, _ := json.Marshal(Output{File: "clip.mp4"})
	if strings.Contains(string(b), "forensic") {
		t.Errorf("default transcribe output must NOT contain a forensic field: %s", b)
	}
	b2, _ := json.Marshal(Output{File: "clip.mp4", Forensic: &forensicrun.ForensicReport{File: "clip.mp4"}})
	if !strings.Contains(string(b2), `"forensic"`) {
		t.Errorf("--forensic output must contain the forensic block: %s", b2)
	}
}
