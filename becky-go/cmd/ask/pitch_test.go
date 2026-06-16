// pitch_test.go — table-driven tests for the deterministic pitch extraction
// and routing logic in pitch.go. No model, no FFmpeg, no temp-file mocking needed
// because savePitchFile writes to the OS temp dir (which works on any CI).
package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// --- pitchDeriveSlug ---

func TestPitchDeriveSlug(t *testing.T) {
	// The slug keeps the first 3 meaningful non-stop-word tokens, mirrors
	// cmd/new-tool/util.go's deriveSlug so both tools agree.
	cases := []struct {
		core string
		want string
	}{
		{"remove watermarks from videos", "becky-remove-watermarks-videos"},
		{"export faces to a spreadsheet", "becky-export-faces-spreadsheet"},
		{"analyze body language in clips", "becky-analyze-body-language"},
		{"auto-transcribe new clips in a folder", "becky-autotranscribe-new-clips"},
		{"redact names from transcripts", "becky-redact-names-transcripts"},
		{"", "becky-new-capability"},
		{"   ", "becky-new-capability"},
	}
	for _, c := range cases {
		got := pitchDeriveSlug(c.core)
		if got != c.want {
			t.Errorf("pitchDeriveSlug(%q) = %q, want %q", c.core, got, c.want)
		}
	}
}

// --- pitchCapability ---

func TestPitchCapability(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"remove watermarks from videos", "Remove watermarks from videos."},
		{"Export faces to a spreadsheet.", "Export faces to a spreadsheet."},
		{"already capitalized", "Already capitalized."},
		{"ends with question?", "Ends with question?"},
		{"", "Capability not yet described."},
		{"   ", "Capability not yet described."},
	}
	for _, c := range cases {
		got := pitchCapability(c.in)
		if got != c.want {
			t.Errorf("pitchCapability(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- pitchGuessInputKind ---

func TestPitchGuessInputKind(t *testing.T) {
	cases := []struct {
		q    string
		want string
	}{
		{"remove watermarks from videos", "video"},
		{"blur faces in mp4 clips", "video"},
		{"transcribe audio speech", "audio"},
		{"read wav file for voice", "audio"},
		{"analyze image/photo", "image"},
		{"read a jpg file", "image"},
		{"fetch from a url", "url"},
		{"parse json transcript", "json"},
		{"monitor a folder", "text"},
	}
	for _, c := range cases {
		got := pitchGuessInputKind(c.q)
		if got != c.want {
			t.Errorf("pitchGuessInputKind(%q) = %q, want %q", c.q, got, c.want)
		}
	}
}

// --- pitchGuessOutputKind ---

func TestPitchGuessOutputKind(t *testing.T) {
	cases := []struct {
		q    string
		want string
	}{
		{"export to a spreadsheet or csv", "csv"},
		{"generate a markdown report", "text"},
		{"identify faces in clips", "json"},
	}
	for _, c := range cases {
		got := pitchGuessOutputKind(c.q)
		if got != c.want {
			t.Errorf("pitchGuessOutputKind(%q) = %q, want %q", c.q, got, c.want)
		}
	}
}

// --- extractPitchDeterministic ---

func TestExtractPitchDeterministic(t *testing.T) {
	cases := []struct {
		question          string
		wantSlug          string
		wantInputKind     string
		wantHasConstraint string
	}{
		{
			"I wish becky could remove watermarks from videos",
			"becky-remove-watermarks-videos",
			"video",
			"offline",
		},
		{
			"build a tool that exports transcripts to a spreadsheet",
			"becky-exports-transcripts-spreadsheet",
			"json",
			"offline",
		},
		{
			"a tool to analyze body language in clips",
			"becky-analyze-body-language",
			"video",
			"offline",
		},
		{
			"it would be nice if becky could auto-redact faces in mp4s",
			"becky-autoredact-faces-mp4s",
			"video",
			"offline",
		},
		{
			"becky should be able to monitor a folder for new audio and auto-transcribe",
			"becky-monitor-folder-new",
			"audio",
			"offline",
		},
	}
	for _, c := range cases {
		p := extractPitchDeterministic(c.question)
		if p.Slug != c.wantSlug {
			t.Errorf("slug(%q) = %q, want %q", c.question, p.Slug, c.wantSlug)
		}
		if p.InputKind != c.wantInputKind {
			t.Errorf("input_kind(%q) = %q, want %q", c.question, p.InputKind, c.wantInputKind)
		}
		found := false
		for _, con := range p.Constraints {
			if con == c.wantHasConstraint {
				found = true
			}
		}
		if !found {
			t.Errorf("constraints(%q) = %v, want to contain %q", c.question, p.Constraints, c.wantHasConstraint)
		}
		// Capability should be non-empty and sentence-cased.
		if p.Capability == "" {
			t.Errorf("capability(%q) is empty", c.question)
		}
		if len(p.Capability) > 0 && p.Capability[0] >= 'a' && p.Capability[0] <= 'z' {
			t.Errorf("capability(%q) = %q starts lowercase", c.question, p.Capability)
		}
		// DefinitionOfDone should include the bare slug in the first entry.
		bare := strings.TrimPrefix(p.Slug, "becky-")
		if len(p.DefinitionOfDone) == 0 || !strings.Contains(p.DefinitionOfDone[0], bare) {
			t.Errorf("definition_of_done(%q) missing bare slug %q in first entry: %v",
				c.question, bare, p.DefinitionOfDone)
		}
		if p.CapturedAt == "" {
			t.Errorf("captured_at(%q) is empty", c.question)
		}
		if p.NormalizedBy != "deterministic" {
			t.Errorf("normalized_by(%q) = %q, want %q", c.question, p.NormalizedBy, "deterministic")
		}
	}
}

// --- pitchCommand ---

func TestPitchCommand(t *testing.T) {
	cmd := pitchCommand("/tmp/becky-pitch-42.json")
	if len(cmd) != 3 {
		t.Fatalf("pitchCommand len = %d, want 3; got %v", len(cmd), cmd)
	}
	if cmd[0] != "becky-new-tool" {
		t.Errorf("cmd[0] = %q, want becky-new-tool", cmd[0])
	}
	if cmd[1] != "--intake-file" {
		t.Errorf("cmd[1] = %q, want --intake-file", cmd[1])
	}
	if cmd[2] != "/tmp/becky-pitch-42.json" {
		t.Errorf("cmd[2] = %q, want /tmp/becky-pitch-42.json", cmd[2])
	}
}

// --- savePitchFile ---

func TestSavePitchFile_WritesValidJSON(t *testing.T) {
	p := extractPitchDeterministic("I wish becky could auto-redact faces from clips")
	f, err := savePitchFile(p)
	if err != nil {
		t.Fatalf("savePitchFile: %v", err)
	}
	defer os.Remove(f)

	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatalf("read pitch file: %v", err)
	}
	var round PitchRecord
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("pitch file is not valid JSON: %v\n%s", err, data)
	}
	if round.Slug != p.Slug {
		t.Errorf("round-trip slug = %q, want %q", round.Slug, p.Slug)
	}
}

// --- buildNewToolRouted ---

func TestBuildNewToolRouted_CatalogHit_NoPending(t *testing.T) {
	// "can becky transcribe" hits the offline catalog — should return a catalog
	// answer, NOT a new-tool pitch, and should have no pending command.
	r := buildNewToolRouted("can becky transcribe a video?")
	if len(r.Pending) != 0 {
		t.Errorf("expected no pending command for a catalog hit, got %v", r.Pending)
	}
	if r.Reply == "" {
		t.Error("Reply should be non-empty for a catalog hit")
	}
}

func TestBuildNewToolRouted_NewIdea_HasPending(t *testing.T) {
	// A genuinely new idea should produce a pitch with a pending becky-new-tool command.
	r := buildNewToolRouted("I wish becky could remove license-plate blur from footage")
	if len(r.Pending) == 0 {
		t.Fatal("expected a pending command for a new-tool idea, got none")
	}
	if r.Pending[0] != "becky-new-tool" {
		t.Errorf("pending[0] = %q, want becky-new-tool", r.Pending[0])
	}
	if r.Pending[1] != "--intake-file" {
		t.Errorf("pending[1] = %q, want --intake-file", r.Pending[1])
	}
	// The pitch file should exist and be valid JSON.
	pitchFile := r.Pending[2]
	defer os.Remove(pitchFile)
	data, err := os.ReadFile(pitchFile)
	if err != nil {
		t.Fatalf("pitch file missing: %v", err)
	}
	var p PitchRecord
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("pitch file invalid JSON: %v\n%s", err, data)
	}
	if p.Slug == "" {
		t.Error("pitch slug is empty")
	}
}

func TestBuildNewToolRouted_Reply_ContainsSlug(t *testing.T) {
	r := buildNewToolRouted("a tool that logs GPS from video metadata")
	if len(r.Pending) == 0 {
		t.Skip("no pitch generated (catalog may have matched); skipping slug-in-reply check")
	}
	defer os.Remove(r.Pending[2])

	pitchFile := r.Pending[2]
	data, _ := os.ReadFile(pitchFile)
	var p PitchRecord
	_ = json.Unmarshal(data, &p)
	if p.Slug != "" && !strings.Contains(r.Reply, p.Slug) {
		t.Errorf("Reply does not contain slug %q\n%s", p.Slug, r.Reply)
	}
}
