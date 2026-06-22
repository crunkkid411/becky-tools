package main

import "testing"

// TestRemedyLine_ExactString asserts the inline teach-me remedy is EXACTLY the
// specified literal for a given clip path — <clip> filled in, <name> left literal.
func TestRemedyLine_ExactString(t *testing.T) {
	clip := `C:\cases\stream-07.mp4`
	got := remedyLine(clip)
	want := `not enrolled — teach me: becky "this is <name>" C:\cases\stream-07.mp4`
	if got != want {
		t.Fatalf("remedyLine mismatch:\n got  %q\n want %q", got, want)
	}
}

// TestRemedyLine_NamePlaceholderKept asserts <name> stays a literal placeholder
// (the human supplies it) and the clip is the only thing substituted.
func TestRemedyLine_NamePlaceholderKept(t *testing.T) {
	got := remedyLine("clip.mp4")
	if want := `not enrolled — teach me: becky "this is <name>" clip.mp4`; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

// TestAttachRemedies_Unidentified_HasRemedy asserts a below-threshold face and a
// demoted voice candidate both end up with the exact remedy string after attach.
func TestAttachRemedies_Unidentified_HasRemedy(t *testing.T) {
	clip := "evidence/clip.mp4"
	report := Output{
		File: clip,
		Unidentified: []Unidentified{
			{Type: "face", Description: "unidentified face", Confidence: 0.41},
			{Type: "voice", SpeakerID: "SPEAKER_00", Description: "possible Braxton (voice match 0.70) — below the naming threshold (0.75), not confirmed", Candidate: "Braxton", WhyUnnamed: whyBelowNameThresh},
		},
	}
	attachRemedies(&report)
	want := `not enrolled — teach me: becky "this is <name>" evidence/clip.mp4`
	for i, u := range report.Unidentified {
		if u.Remedy != want {
			t.Fatalf("unidentified[%d] remedy = %q, want %q", i, u.Remedy, want)
		}
	}
}

// TestAttachRemedies_PreservesWhyUnnamed asserts the remedy is purely ADDITIVE — the
// wave-1 hardening audit trail (why_unnamed, candidate, description) is untouched.
func TestAttachRemedies_PreservesWhyUnnamed(t *testing.T) {
	report := Output{
		File: "clip.mp4",
		Unidentified: []Unidentified{
			{Type: "voice", SpeakerID: "SPEAKER_01", Description: "ambiguous: 0.78 for A vs 0.76 for B", Candidate: "A", RunnerUp: "B", VoiceMargin: 0.02, WhyUnnamed: whyAmbiguousMargin},
		},
	}
	attachRemedies(&report)
	u := report.Unidentified[0]
	if u.WhyUnnamed != whyAmbiguousMargin {
		t.Errorf("why_unnamed clobbered: got %q want %q", u.WhyUnnamed, whyAmbiguousMargin)
	}
	if u.Candidate != "A" || u.RunnerUp != "B" || u.VoiceMargin != 0.02 {
		t.Errorf("audit trail clobbered: candidate=%q runnerUp=%q margin=%v", u.Candidate, u.RunnerUp, u.VoiceMargin)
	}
	if u.Description == "" {
		t.Errorf("description clobbered to empty")
	}
	if u.Remedy == "" {
		t.Errorf("remedy not attached")
	}
}

// TestAttachRemedies_Named_NoRemedy asserts named identifications carry no remedy
// (the Identification struct has no remedy field; only unidentified entries get one),
// and that an empty Unidentified list is a no-op (no panic).
func TestAttachRemedies_Named_NoRemedy(t *testing.T) {
	report := Output{
		File:            "clip.mp4",
		Identifications: []Identification{{Type: "voice", Name: "Braxton", Confidence: 0.88}},
		Unidentified:    []Unidentified{},
	}
	attachRemedies(&report)
	if len(report.Unidentified) != 0 {
		t.Fatalf("expected no unidentified entries, got %d", len(report.Unidentified))
	}
	// Named identification is unaffected (no remedy concept on it).
	if report.Identifications[0].Name != "Braxton" {
		t.Fatalf("identification mutated")
	}
}
