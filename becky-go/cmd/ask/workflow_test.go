// workflow_test.go — proves the transcribe-workflow MERGE headless: a finished
// transcript labels speakers AND surfaces the on-screen (burned-in) text that raw
// speech-to-text misses. No tools run; the merge is pure.
package main

import (
	"strings"
	"testing"
)

func TestMergeTranscript_SpeakersAndBurnedInCaptions(t *testing.T) {
	tf := wfTranscript{Segments: []wfTSeg{
		{Start: 0, End: 2, Text: "hello there"},
		{Start: 3, End: 5, Text: "checking the mic"},
	}}
	df := wfDiarize{Speakers: []wfDSpk{
		{ID: "SPEAKER_00", Segments: []wfDSeg{{Start: 0, End: 2}}},
		{ID: "SPEAKER_01", Segments: []wfDSeg{{Start: 2, End: 6}}},
	}}
	of := wfOcr{Results: []wfOcrRes{
		{Timestamp: 0, Lines: []wfOcrLine{{Text: "oh my god", Confidence: 1.0}}},
		{Timestamp: 3, Lines: []wfOcrLine{{Text: "mic check", Confidence: 1.0}}},
		{Timestamp: 4, Lines: []wfOcrLine{{Text: "*CHOKES ON NOTHING*", Confidence: 0.99}}},
		{Timestamp: 5, Lines: []wfOcrLine{{Text: "@somehandle", Confidence: 1.0}}},
	}}

	md := mergeTranscript("clip.mp4", tf, df, of)

	if !strings.Contains(md, "SPEAKER_00") || !strings.Contains(md, "SPEAKER_01") {
		t.Errorf("transcript should label both speakers:\n%s", md)
	}
	for _, want := range []string{"oh my god", "mic check", "CHOKES"} {
		if !strings.Contains(md, want) {
			t.Errorf("transcript should surface on-screen text %q:\n%s", want, md)
		}
	}
	// 3 distinct non-@ on-screen lines => burned-in warning present.
	if !strings.Contains(md, "burned into it") {
		t.Errorf("expected a burned-in-captions warning:\n%s", md)
	}
}

func TestWfOcrCaptions_DedupAndHandleExclusion(t *testing.T) {
	of := wfOcr{Results: []wfOcrRes{
		{Timestamp: 1, Lines: []wfOcrLine{{Text: "hi", Confidence: 1}}},
		{Timestamp: 2, Lines: []wfOcrLine{{Text: "hi", Confidence: 1}}}, // dup
		{Timestamp: 3, Lines: []wfOcrLine{{Text: "@handle", Confidence: 1}}},
		{Timestamp: 4, Lines: []wfOcrLine{{Text: "low", Confidence: 0.3}}}, // below 0.7
	}}
	caps, burned := wfOcrCaptions(of)
	if len(caps) != 2 { // "hi" once + "@handle"; "low" dropped by confidence
		t.Errorf("expected 2 deduped caps, got %d: %v", len(caps), caps)
	}
	if burned {
		t.Errorf("one real line + a handle is NOT enough to call it burned-in")
	}
}

func TestWfSpeakerAt(t *testing.T) {
	df := wfDiarize{Speakers: []wfDSpk{
		{ID: "SPEAKER_00", Segments: []wfDSeg{{Start: 0, End: 2}}},
		{ID: "SPEAKER_01", Segments: []wfDSeg{{Start: 2, End: 6}}},
	}}
	if got := wfSpeakerAt(df, 1); got != "SPEAKER_00" {
		t.Errorf("t=1 -> %q, want SPEAKER_00", got)
	}
	if got := wfSpeakerAt(df, 4); got != "SPEAKER_01" {
		t.Errorf("t=4 -> %q, want SPEAKER_01", got)
	}
	if got := wfSpeakerAt(df, 99); got != "" {
		t.Errorf("t=99 (far outside) -> %q, want empty", got)
	}
}

func TestCensorToken(t *testing.T) {
	cases := map[string]string{
		"fuck": "f**k", "Fuck": "F**k", "fucking": "f***ing",
		"shit,": "sh*t,", "\"shit\"": "\"sh*t\"",
		"hello": "hello", "Class": "Class", // not profanity, untouched
	}
	for in, want := range cases {
		if got := censorToken(in); got != want {
			t.Errorf("censorToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReflowCaptions_ShortCensoredChunks(t *testing.T) {
	words := []wfWord{
		{"I", 0, 0.1}, {"don't", 0.2, 0.4}, {"need", 0.5, 0.7}, {"a", 0.8, 0.85},
		{"man", 0.9, 1.1}, {"fucking", 1.2, 1.6}, {"period", 3.0, 3.4}, // big gap before "period"
	}
	lines := reflowCaptions(words, 4, 0.5)
	if len(lines) < 2 {
		t.Fatalf("expected multiple short lines, got %d: %+v", len(lines), lines)
	}
	for _, l := range lines {
		if n := len(strings.Fields(l.Text)); n > 4 {
			t.Errorf("caption chunk too long (%d words): %q", n, l.Text)
		}
	}
	joined := ""
	for _, l := range lines {
		joined += " " + l.Text
	}
	if !strings.Contains(joined, "f***ing") || strings.Contains(joined, "fucking") {
		t.Errorf("profanity should be censored in captions: %q", joined)
	}
}
