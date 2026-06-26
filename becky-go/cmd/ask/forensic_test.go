package main

import (
	"context"
	"strings"
	"testing"

	"becky-go/internal/forensicrun"
	"becky-go/internal/orchestrate"
)

func TestForensicIntent(t *testing.T) {
	cases := []struct {
		q       string
		want    forensicKind
		subject string
	}{
		{"who is in this video", fNaming, ""},
		{"who's in this clip?", fNaming, ""},
		{"identify the speakers", fNaming, ""},
		{"is Shelby on screen?", fPresence, "Shelby"},
		{"does Jordan appear in this video", fPresence, "Jordan"},
		{"is the dog visible", fPresence, "dog"},
		{"transcribe this", fNone, ""},
		{"what is said in this clip", fNone, ""},
		{"cut the silence", fNone, ""},
	}
	for _, c := range cases {
		k, subj := forensicIntent(c.q)
		if k != c.want || subj != c.subject {
			t.Errorf("forensicIntent(%q) = (%d,%q), want (%d,%q)", c.q, k, subj, c.want, c.subject)
		}
	}
}

func swapResolver(t *testing.T, fn func(ctx context.Context, file, subject, kb string, speakers int, tr []byte) forensicrun.ForensicReport) {
	t.Helper()
	orig := forensicResolveAsk
	t.Cleanup(func() { forensicResolveAsk = orig })
	forensicResolveAsk = fn
}

func mediaTarget(path string) Target { return Target{Paths: []string{path}, Kind: targetFile} }

// A naming question on a dropped file returns the corroborated name as the answer (not a staged
// command), and threads the file through with no subject.
func TestForensicSingleShot_Naming(t *testing.T) {
	var gotFile, gotSubject string
	swapResolver(t, func(ctx context.Context, file, subject, kb string, speakers int, tr []byte) forensicrun.ForensicReport {
		gotFile, gotSubject = file, subject
		return forensicrun.ForensicReport{File: file, Names: []orchestrate.Verdict{{Claim: "person=Shelby", Status: orchestrate.Concluded}}}
	})

	res, ok := forensicSingleShot(context.Background(), "who is in this video", mediaTarget("clip.mp4"))
	if !ok {
		t.Fatal("a naming question on a file must be handled by the forensic path")
	}
	if res.Kind != "forensic" || res.Source != "becky-orchestrate" {
		t.Errorf("kind/source = %q/%q", res.Kind, res.Source)
	}
	if !strings.Contains(res.Answer, "Shelby") {
		t.Errorf("answer must state the corroborated name, got %q", res.Answer)
	}
	if gotFile != "clip.mp4" || gotSubject != "" {
		t.Errorf("resolver got file=%q subject=%q, want clip.mp4/empty", gotFile, gotSubject)
	}
}

// A presence question threads the subject and states the watched window.
func TestForensicSingleShot_Presence(t *testing.T) {
	var gotSubject string
	swapResolver(t, func(ctx context.Context, file, subject, kb string, speakers int, tr []byte) forensicrun.ForensicReport {
		gotSubject = subject
		return forensicrun.ForensicReport{File: file, Subject: subject,
			OnScreen: []orchestrate.Verdict{{Claim: "onscreen=Shelby@[12.0-18.0]", Status: orchestrate.Concluded}}}
	})

	res, ok := forensicSingleShot(context.Background(), "is Shelby on screen?", mediaTarget("clip.mp4"))
	if !ok {
		t.Fatal("a presence question on a file must be handled by the forensic path")
	}
	if gotSubject != "Shelby" {
		t.Errorf("subject not threaded, got %q", gotSubject)
	}
	if !strings.Contains(res.Answer, "Shelby") || !strings.Contains(res.Answer, "12.0-18.0") {
		t.Errorf("presence answer must state the watched window, got %q", res.Answer)
	}
}

// A non-forensic question is NOT intercepted (normal routing proceeds).
func TestForensicSingleShot_NotForensic_FallsThrough(t *testing.T) {
	if _, ok := forensicSingleShot(context.Background(), "transcribe this", mediaTarget("clip.mp4")); ok {
		t.Error("a non-forensic question must fall through to normal routing")
	}
}

// A forensic question with NO target file is not intercepted (nothing to resolve on).
func TestForensicSingleShot_NoTarget_FallsThrough(t *testing.T) {
	if _, ok := forensicSingleShot(context.Background(), "who is in this", Target{}); ok {
		t.Error("a forensic question with no target must fall through")
	}
}

// The answer is honest when nothing corroborates, and never dumps held maybes as conclusions.
func TestFormatForensicAnswer_HonestWhenEmpty(t *testing.T) {
	naming := formatForensicAnswer(forensicrun.ForensicReport{
		Held: []orchestrate.Verdict{{Claim: "person=Maybe", Status: orchestrate.Candidate}}}, fNaming)
	if !strings.Contains(naming, "No one could be named") {
		t.Errorf("empty naming must be honest, got %q", naming)
	}
	if strings.Contains(naming, "Maybe") {
		t.Errorf("held candidates must NOT be stated as names, got %q", naming)
	}
	if !strings.Contains(naming, "1 candidate(s) held") {
		t.Errorf("must note that a candidate is held, got %q", naming)
	}

	presence := formatForensicAnswer(forensicrun.ForensicReport{Subject: "Shelby"}, fPresence)
	if !strings.Contains(presence, "can't confirm Shelby on screen") {
		t.Errorf("empty presence must be honest, got %q", presence)
	}
}
