package main

import "testing"

func TestNameRefersTo(t *testing.T) {
	cases := []struct {
		ident, query string
		want         bool
	}{
		{"John Anthony Clancy", "John", true},
		{"John", "John Anthony Clancy", true},
		{"John Clancy", "john clancy", true},
		{"Shelby", "John", false},
		{"", "John", false},
		{"John", "", false},
	}
	for _, c := range cases {
		if got := nameRefersTo(c.ident, c.query); got != c.want {
			t.Errorf("nameRefersTo(%q,%q) = %v, want %v", c.ident, c.query, got, c.want)
		}
	}
}

// TestNameRefersToTokenSubset covers the real bug: a multi-word query with a gap
// ("John Clancy" vs stored "John Anthony Clancy") that plain substring missed.
func TestNameRefersToTokenSubset(t *testing.T) {
	cases := []struct {
		ident, query string
		want         bool
	}{
		{"John Anthony Clancy", "John Clancy", true},  // the bug — middle name broke substring
		{"John Anthony Clancy", "Clancy", true},       // last name alone
		{"Shelby Alyse Clancy", "John Clancy", false}, // must NOT match across people
		{"John Anthony Clancy", "John Smith", false},  // partial overlap is not enough
		// Descriptive parenthetical must NOT hijack: "John Clancy" is inside
		// "(John Clancy's mother)" but Bettina is not John.
		{"Bettina Burke-Clancy (John Clancy's mother)", "John Clancy", false},
	}
	for _, c := range cases {
		if got := nameRefersTo(c.ident, c.query); got != c.want {
			t.Errorf("nameRefersTo(%q,%q) = %v, want %v", c.ident, c.query, got, c.want)
		}
	}
}

// TestMatchScoreRanking verifies exact > alias > substring > token-subset > none,
// so the strongest candidate wins when several entities partially match.
func TestMatchScoreRanking(t *testing.T) {
	aliases := []string{"JC", "Johnny"}
	if s := matchScore("John Anthony Clancy", aliases, "John Anthony Clancy"); s != 4 {
		t.Errorf("exact name score = %d, want 4", s)
	}
	if s := matchScore("John Anthony Clancy", aliases, "JC"); s != 3 {
		t.Errorf("exact alias score = %d, want 3", s)
	}
	if s := matchScore("John Anthony Clancy", aliases, "John Clancy"); s != 2 {
		t.Errorf("token-subset score = %d, want 2", s)
	}
	if s := matchScore("Shelby Alyse Clancy", aliases, "John Clancy"); s != 0 {
		t.Errorf("no-match score = %d, want 0", s)
	}
	// The Bettina trap: her descriptive name contains "John Clancy's" but she must score 0.
	if s := matchScore("Bettina Burke-Clancy (John Clancy's mother)", nil, "John Clancy"); s != 0 {
		t.Errorf("descriptive-paren score = %d, want 0", s)
	}
	if exact, subset := matchScore("John Clancy", nil, "John Clancy"),
		matchScore("John Anthony Clancy", nil, "John Clancy"); exact <= subset {
		t.Errorf("exact (%d) should outrank token-subset (%d)", exact, subset)
	}
}

func TestAppearanceReportIngest(t *testing.T) {
	r := newAppearanceReport("John Anthony Clancy", "kb", "corpus")
	idJSON := []byte(`{
      "file":"v1.mp4",
      "identifications":[
        {"type":"voice","name":"John Anthony Clancy","confidence":0.91,"speaker_id":"SPEAKER_00"},
        {"type":"face","name":"John Anthony Clancy","confidence":0.87,"frames":[{"timestamp":7.5}]},
        {"type":"voice","name":"Shelby","confidence":0.6,"speaker_id":"SPEAKER_01"}
      ]}`)
	r.ingest("v1.mp4", idJSON)
	// A second video with no match for John.
	r.ingest("v2.mp4", []byte(`{"file":"v2.mp4","identifications":[{"type":"voice","name":"Shelby","confidence":0.8}]}`))
	r.finalize()

	if r.TotalVideos != 2 {
		t.Errorf("TotalVideos = %d, want 2", r.TotalVideos)
	}
	if r.MatchedVideos != 1 {
		t.Errorf("MatchedVideos = %d, want 1 (only v1 has John)", r.MatchedVideos)
	}
	if r.VoiceMatches != 1 || r.FaceMatches != 1 {
		t.Errorf("voice=%d face=%d, want 1/1 (Shelby's voice excluded)", r.VoiceMatches, r.FaceMatches)
	}
	if len(r.Appearances) != 2 {
		t.Fatalf("appearances = %d, want 2", len(r.Appearances))
	}
	if r.Appearances[1].Timestamp != 7.5 {
		t.Errorf("face timestamp = %v, want 7.5", r.Appearances[1].Timestamp)
	}
}

func TestAppearanceReportError(t *testing.T) {
	r := newAppearanceReport("X", "kb", "corpus")
	r.ingest("bad.mp4", []byte("not json"))
	r.finalize()
	if len(r.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(r.Errors))
	}
	if r.MatchedVideos != 0 {
		t.Errorf("MatchedVideos = %d, want 0", r.MatchedVideos)
	}
}

func TestParseSearchResults(t *testing.T) {
	wrapped := []byte(`{"results":[{"rank":1,"source_file":"a.mp4","timestamp":12.3,"text":"hi","similarity":0.8,"matched":"hybrid"}]}`)
	r := parseSearchResults(wrapped)
	if len(r.results) != 1 || r.results[0].Rank != 1 || r.results[0].Similarity != 0.8 {
		t.Errorf("wrapped parse = %+v", r.results)
	}
	bare := []byte(`[{"rank":2,"source_file":"b.mp4","timestamp":1,"text":"x","similarity":0.5}]`)
	r2 := parseSearchResults(bare)
	if len(r2.results) != 1 || r2.results[0].Rank != 2 {
		t.Errorf("bare parse = %+v", r2.results)
	}
	if got := parseSearchResults([]byte("garbage")); len(got.results) != 0 {
		t.Errorf("garbage parse should be empty, got %+v", got.results)
	}
}

func TestParsePipelineManifest(t *testing.T) {
	man := []byte(`{"videos":[
      {"status":"ok","steps":[{"name":"transcribe","status":"ok"},{"name":"embed","status":"ok"}]},
      {"status":"partial","steps":[{"name":"transcribe","status":"ok"},{"name":"embed","status":"failed"}]}
    ]}`)
	s := parsePipelineManifest(man)
	if s.totalVideos != 2 || s.okVideos != 1 || s.partialVideos != 1 {
		t.Errorf("summary = %+v", s)
	}
	if !s.embedFailed {
		t.Error("expected embedFailed = true")
	}
}

func TestExtractCommonAndFlags(t *testing.T) {
	cf, rest := extractCommon([]string{"John Clancy", "--bin", "/b", "--verbose", "--kb", "kb"})
	if cf.bin != "/b" || !cf.verbose {
		t.Errorf("commonFlags = %+v", cf)
	}
	kb, rest2 := flagValue(rest, "kb", "default")
	if kb != "kb" {
		t.Errorf("kb = %q", kb)
	}
	if firstPositional(rest2) != "John Clancy" {
		t.Errorf("positional = %q", firstPositional(rest2))
	}
}

func TestFlagValues(t *testing.T) {
	vals, rest := flagValues([]string{"--wiki", "a", "--wiki", "b", "--kb", "x"}, "wiki")
	if len(vals) != 2 || vals[0] != "a" || vals[1] != "b" {
		t.Errorf("flagValues = %v", vals)
	}
	if len(rest) != 2 { // --kb x remains
		t.Errorf("rest = %v", rest)
	}
}
