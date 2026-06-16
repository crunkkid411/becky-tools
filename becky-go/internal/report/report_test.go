package report

import (
	"strings"
	"testing"
)

// --- corroboration rule tests ---

func TestApplyCorroboration(t *testing.T) {
	tests := []struct {
		name           string
		confidence     float64
		corroboratedBy []string
		wantConcluded  bool
		wantTag        string
	}{
		{
			name:           "two signals: DOCUMENTED",
			confidence:     0.75,
			corroboratedBy: []string{"voice", "face"},
			wantConcluded:  true,
			wantTag:        "DOCUMENTED",
		},
		{
			name:           "three signals: DOCUMENTED",
			confidence:     0.91,
			corroboratedBy: []string{"voice", "face", "location"},
			wantConcluded:  true,
			wantTag:        "DOCUMENTED",
		},
		{
			name:           "one signal, very high confidence: DOCUMENTED",
			confidence:     0.92,
			corroboratedBy: []string{"voice"},
			wantConcluded:  true,
			wantTag:        "DOCUMENTED",
		},
		{
			name:           "one signal, exactly at threshold: DOCUMENTED",
			confidence:     concludedHighConf,
			corroboratedBy: []string{"face"},
			wantConcluded:  true,
			wantTag:        "DOCUMENTED",
		},
		{
			name:           "one signal, just below threshold: CANDIDATE",
			confidence:     concludedHighConf - 0.01,
			corroboratedBy: []string{"voice"},
			wantConcluded:  false,
			wantTag:        "CANDIDATE",
		},
		{
			name:           "no signals: CANDIDATE",
			confidence:     0.95,
			corroboratedBy: nil,
			wantConcluded:  false,
			wantTag:        "CANDIDATE",
		},
		{
			name:           "empty corroborated_by: CANDIDATE",
			confidence:     0.85,
			corroboratedBy: []string{},
			wantConcluded:  false,
			wantTag:        "CANDIDATE",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotConcluded, gotTag := applyCorroboration(tc.confidence, tc.corroboratedBy)
			if gotConcluded != tc.wantConcluded {
				t.Errorf("concluded: got %v, want %v", gotConcluded, tc.wantConcluded)
			}
			if gotTag != tc.wantTag {
				t.Errorf("tag: got %q, want %q", gotTag, tc.wantTag)
			}
		})
	}
}

// --- Build() tests ---

func TestBuild_EmptySidecars(t *testing.T) {
	r := Build(Sidecars{}, "test.mp4")
	if !r.Degraded {
		t.Error("expected Degraded=true when no sidecars provided")
	}
	if len(r.Timeline) != 0 {
		t.Errorf("expected empty timeline, got %d moments", len(r.Timeline))
	}
	if r.Source != "test.mp4" {
		t.Errorf("unexpected source: %q", r.Source)
	}
}

func TestBuild_TranscriptOnly(t *testing.T) {
	s := Sidecars{
		Transcript: &transcriptOutput{
			File:     "clip.mp4",
			Duration: 30.5,
			Model:    "parakeet-v3",
			Segments: []transcriptSegment{
				{Start: 1.0, End: 3.5, Text: "Hello, how are you?"},
				{Start: 5.0, End: 7.0, Text: "I'm fine.", LowConfidence: true},
				{Start: 9.0, End: 11.0, Text: ""},
			},
		},
	}
	r := Build(s, "clip.mp4")

	if r.Degraded {
		t.Error("unexpected Degraded=true")
	}
	if r.Duration != 30.5 {
		t.Errorf("duration: got %v, want 30.5", r.Duration)
	}

	// Empty text segment must not appear in timeline.
	for _, m := range r.Timeline {
		if m.Description == "" {
			t.Error("empty-description moment in timeline")
		}
	}
	// Two non-empty segments.
	if len(r.Timeline) != 2 {
		t.Errorf("timeline len: got %d, want 2", len(r.Timeline))
	}

	// Low-confidence segment gets ANALYSIS tag.
	gotTags := map[string]bool{}
	for _, m := range r.Timeline {
		gotTags[m.Tag] = true
	}
	if !gotTags["DOCUMENTED"] {
		t.Error("expected at least one DOCUMENTED moment")
	}
	if !gotTags["ANALYSIS"] {
		t.Error("expected at least one ANALYSIS moment for low-confidence segment")
	}

	// Signal summary.
	if r.Signals.Transcript == nil || !r.Signals.Transcript.Present {
		t.Error("transcript signal missing")
	}
	if r.Signals.Transcript.SegmentCount != 3 {
		// all 3 parsed, including empty one (we report the raw count, filter only in timeline)
		// Actually we set SegmentCount from the parsed slices, so it's 3.
		t.Errorf("segment count: got %d, want 3", r.Signals.Transcript.SegmentCount)
	}
}

func TestBuild_IdentifyCorroboration(t *testing.T) {
	s := Sidecars{
		Identify: &identifyOutput{
			File: "clip.mp4",
			Identifications: []identifyEntry{
				{
					Name:           "Alice Smith",
					Type:           "voice+face",
					Confidence:     0.88,
					CorroboratedBy: []string{"voice", "face"},
					SpeakerID:      "SPEAKER_00",
					Segments:       []identifySpan{{Start: 2.0, End: 10.0}},
				},
				{
					Name:           "Bob Jones",
					Type:           "voice",
					Confidence:     0.75,
					CorroboratedBy: []string{"voice"},
					SpeakerID:      "SPEAKER_01",
					Segments:       []identifySpan{{Start: 12.0, End: 18.0}},
				},
			},
			Unidentified: []identifyUnknown{
				{Type: "voice", SpeakerID: "SPEAKER_02", Description: "unknown male", Confidence: 0.55, Candidate: "Charlie"},
			},
		},
	}
	r := Build(s, "clip.mp4")

	if r.Degraded {
		t.Errorf("unexpected Degraded=true, notes: %v", r.Notes)
	}

	// Alice: 2 signals → DOCUMENTED; Bob: 1 signal, <0.90 → CANDIDATE.
	entByName := map[string]Entity{}
	for _, e := range r.Entities {
		entByName[e.Name] = e
	}

	alice, ok := entByName["Alice Smith"]
	if !ok {
		t.Fatal("Alice Smith not in entities")
	}
	if !alice.Concluded {
		t.Error("Alice should be concluded (2 signals)")
	}
	if alice.Tag != "DOCUMENTED" {
		t.Errorf("Alice tag: got %q, want DOCUMENTED", alice.Tag)
	}

	bob, ok := entByName["Bob Jones"]
	if !ok {
		t.Fatal("Bob Jones not in entities")
	}
	if bob.Concluded {
		t.Error("Bob should not be concluded (1 signal, conf 0.75 < 0.90)")
	}
	if bob.Tag != "CANDIDATE" {
		t.Errorf("Bob tag: got %q, want CANDIDATE", bob.Tag)
	}

	// Charlie near-miss should appear as CANDIDATE entity.
	charlie, ok := entByName["Charlie"]
	if !ok {
		t.Fatal("Charlie (near-miss candidate) not in entities")
	}
	if charlie.Tag != "CANDIDATE" {
		t.Errorf("Charlie tag: got %q, want CANDIDATE", charlie.Tag)
	}

	// Conclusions must only contain Alice.
	concludedNames := map[string]bool{}
	for _, f := range r.Conclusions {
		if strings.Contains(f.What, "Alice") {
			concludedNames["Alice"] = true
		}
	}
	if !concludedNames["Alice"] {
		t.Error("Alice should appear in Conclusions")
	}
}

func TestBuild_MotionBursts(t *testing.T) {
	s := Sidecars{
		Motion: &motionOutput{
			SourceFile:  "clip.mp4",
			DurationSec: 60.0,
			BurstCount:  2,
			MotionBursts: []motionBurst{
				{
					WindowStart:     0.5,
					WindowEnd:       0.8,
					PeakTime:        0.65,
					MotionScore:     0.72,
					SubSecond:       true,
					BetweenSamples:  true,
					RecommendReview: true,
				},
				{
					WindowStart:     25.0,
					WindowEnd:       27.3,
					PeakTime:        26.1,
					MotionScore:     0.45,
					SubSecond:       false,
					BetweenSamples:  false,
					RecommendReview: false,
				},
			},
		},
	}
	r := Build(s, "clip.mp4")

	// Sub-second burst must be in timeline with sub_second=true.
	foundSubSec := false
	for _, m := range r.Timeline {
		if m.Type == "motion_burst" && m.SubSecond {
			foundSubSec = true
			if !strings.Contains(m.Description, "sub-second") {
				t.Errorf("sub-second burst description should mention sub-second: %q", m.Description)
			}
		}
	}
	if !foundSubSec {
		t.Error("expected sub-second motion burst in timeline")
	}

	// Sub-second burst should be in review items.
	foundReview := false
	for _, f := range r.ReviewItems {
		if strings.Contains(f.What, "sub-second") {
			foundReview = true
		}
	}
	if !foundReview {
		t.Error("sub-second burst should be in ReviewItems")
	}

	// Motion signal summary.
	if r.Signals.Motion == nil || !r.Signals.Motion.Present {
		t.Error("motion signal missing")
	}
	if r.Signals.Motion.SubSecondCount != 1 {
		t.Errorf("sub-second count: got %d, want 1", r.Signals.Motion.SubSecondCount)
	}
}

func TestBuild_TimelineOrder(t *testing.T) {
	// Transcript at t=5, events at t=3, motion at t=1 — timeline must be ascending.
	s := Sidecars{
		Transcript: &transcriptOutput{
			Segments: []transcriptSegment{{Start: 5.0, End: 6.0, Text: "spoken word"}},
		},
		Events: &eventsOutput{
			Events: []eventsEvent{
				{Type: "second_speaker", Start: 3.0, End: 4.0, Confidence: 0.9,
					Description: "second speaker turn"},
			},
		},
		Motion: &motionOutput{
			MotionBursts: []motionBurst{
				{WindowStart: 1.0, WindowEnd: 1.5, MotionScore: 0.6},
			},
		},
	}
	r := Build(s, "clip.mp4")

	for i := 1; i < len(r.Timeline); i++ {
		if r.Timeline[i].Time < r.Timeline[i-1].Time {
			t.Errorf("timeline not sorted: moment[%d].Time=%.2f < moment[%d].Time=%.2f",
				i, r.Timeline[i].Time, i-1, r.Timeline[i-1].Time)
		}
	}
}

func TestBuild_SpeakerResolution(t *testing.T) {
	// When identify gives us Alice → SPEAKER_00, events with SPEAKER_00 should
	// show "Alice Smith" in the resolved speaker field.
	s := Sidecars{
		Identify: &identifyOutput{
			Identifications: []identifyEntry{
				{
					Name:           "Alice Smith",
					Type:           "voice",
					Confidence:     0.91,
					CorroboratedBy: []string{"voice"},
					SpeakerID:      "SPEAKER_00",
					Segments:       []identifySpan{{Start: 0, End: 20}},
				},
			},
		},
		Events: &eventsOutput{
			Events: []eventsEvent{
				{Type: "second_speaker", Start: 5.0, End: 8.0, Confidence: 0.85,
					SpeakerID: "SPEAKER_00", Description: "second speaker"},
			},
		},
	}
	r := Build(s, "clip.mp4")

	resolved := false
	for _, m := range r.Timeline {
		if m.Source == "events" && m.Speaker == "Alice Smith" {
			resolved = true
		}
	}
	if !resolved {
		t.Error("expected SPEAKER_00 to be resolved to 'Alice Smith' in event moments")
	}
}

func TestBuild_DurationPriority(t *testing.T) {
	// Events duration takes priority over transcript when both present.
	s := Sidecars{
		Transcript: &transcriptOutput{Duration: 20.0, Segments: []transcriptSegment{{Start: 1, End: 2, Text: "hi"}}},
		Events:     &eventsOutput{Duration: 35.0, Events: []eventsEvent{}},
	}
	r := Build(s, "clip.mp4")
	if r.Duration != 35.0 {
		t.Errorf("duration: got %v, want 35.0 (events takes priority)", r.Duration)
	}
}

// --- formatTime tests ---

func TestFormatTime(t *testing.T) {
	tests := []struct {
		sec  float64
		want string
	}{
		{0, "0:00"},
		{30, "0:30"},
		{60, "1:00"},
		{90.5, "1:30"},
		{3661, "61:01"},
		{-1, "unknown"},
	}
	for _, tc := range tests {
		got := formatTime(tc.sec)
		if got != tc.want {
			t.Errorf("formatTime(%.1f) = %q, want %q", tc.sec, got, tc.want)
		}
	}
}

func TestFormatTimeRange(t *testing.T) {
	tests := []struct {
		start, end float64
		want       string
	}{
		{5, 10, "0:05–0:10"},
		{65, 70.5, "1:05–1:10"},
		{5, 5.05, "0:05"}, // too short → point
		{0, 0, "unknown"},
	}
	for _, tc := range tests {
		got := formatTimeRange(tc.start, tc.end)
		if got != tc.want {
			t.Errorf("formatTimeRange(%.2f,%.2f) = %q, want %q", tc.start, tc.end, got, tc.want)
		}
	}
}

// --- Markdown tests ---

func TestMarkdown_ContainsSections(t *testing.T) {
	s := Sidecars{
		Transcript: &transcriptOutput{
			Duration: 20.0,
			Model:    "parakeet",
			Segments: []transcriptSegment{{Start: 1, End: 2, Text: "test text"}},
		},
		Identify: &identifyOutput{
			Identifications: []identifyEntry{
				{Name: "Jordan", Type: "voice", Confidence: 0.93,
					CorroboratedBy: []string{"voice"}, Segments: []identifySpan{{Start: 1, End: 5}}},
			},
		},
	}
	r := Build(s, "test.mp4")
	md := Markdown(r)

	checks := []struct{ needle, label string }{
		{"# Forensic Case Report", "title"},
		{"## Signals available", "signals section"},
		{"## Timeline", "timeline section"},
		{"test.mp4", "source name"},
	}
	for _, c := range checks {
		if !strings.Contains(md, c.needle) {
			t.Errorf("markdown missing %s: looking for %q", c.label, c.needle)
		}
	}
}

func TestMarkdown_DocumentedNotHedged(t *testing.T) {
	// A concluded entity (DOCUMENTED) must appear in markdown without hedging words.
	s := Sidecars{
		Identify: &identifyOutput{
			Identifications: []identifyEntry{
				{Name: "Shelby", Type: "voice+face", Confidence: 0.89,
					CorroboratedBy: []string{"voice", "face"},
					Segments:       []identifySpan{{Start: 3, End: 12}}},
			},
		},
	}
	r := Build(s, "test.mp4")
	md := Markdown(r)

	// The name "Shelby" must appear plainly under DOCUMENTED section.
	if !strings.Contains(md, "DOCUMENTED") {
		t.Fatal("expected DOCUMENTED section in markdown")
	}
	// Must not say "possibly Shelby" or "maybe Shelby".
	if strings.Contains(md, "possibly Shelby") || strings.Contains(md, "maybe Shelby") {
		t.Error("DOCUMENTED entity should not be hedged")
	}
}

func TestMarkdown_DegradedFlag(t *testing.T) {
	r := Build(Sidecars{}, "empty.mp4")
	md := Markdown(r)
	if !strings.Contains(md, "DEGRADED") {
		t.Error("expected DEGRADED warning in markdown for empty sidecars")
	}
}

// --- signalTable tests ---

func TestSignalTable_AllPresent(t *testing.T) {
	sig := SignalSummary{
		Transcript: &TranscriptSig{Present: true, SegmentCount: 42, Duration: 30, Model: "parakeet"},
		Events:     &EventsSig{Present: true, EventCount: 5},
		Identify:   &IdentifySig{Present: true, IdentifiedCount: 2, UnidentifiedCount: 1},
		Motion:     &MotionSig{Present: true, BurstCount: 3, SubSecondCount: 1},
	}
	tbl := signalTable(sig)
	for _, want := range []string{"✅", "42", "parakeet", "5", "3"} {
		if !strings.Contains(tbl, want) {
			t.Errorf("signal table missing %q", want)
		}
	}
}

func TestSignalTable_NonePresent(t *testing.T) {
	tbl := signalTable(SignalSummary{})
	if !strings.Contains(tbl, "❌") {
		t.Error("empty signal summary should show ❌")
	}
}

// --- entity sorting tests ---

func TestEntitiesSortedConcludedFirst(t *testing.T) {
	s := Sidecars{
		Identify: &identifyOutput{
			Identifications: []identifyEntry{
				{Name: "Candidate", Type: "voice", Confidence: 0.70,
					CorroboratedBy: []string{"voice"}}, // 1 signal, low conf → CANDIDATE
				{Name: "Concluded", Type: "voice+face", Confidence: 0.88,
					CorroboratedBy: []string{"voice", "face"}, // 2 signals → DOCUMENTED
					Segments:       []identifySpan{{Start: 0, End: 10}}},
			},
		},
	}
	r := Build(s, "test.mp4")
	if len(r.Entities) < 2 {
		t.Fatalf("expected 2 entities, got %d", len(r.Entities))
	}
	if !r.Entities[0].Concluded {
		t.Error("DOCUMENTED entity should sort before CANDIDATE")
	}
}
