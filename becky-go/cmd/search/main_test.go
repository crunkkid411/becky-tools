package main

import (
	"strings"
	"testing"

	"becky-go/internal/beckydb"
)

func TestSpeakerLabel(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty -> unidentified", "", unidentifiedSpeaker},
		{"whitespace -> unidentified", "   ", unidentifiedSpeaker},
		{"named passes through", "Defendant", "Defendant"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := speakerLabel(tt.in); got != tt.want {
				t.Errorf("speakerLabel(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNilIfEmpty(t *testing.T) {
	if got := nilIfEmpty(""); got != nil {
		t.Errorf("nilIfEmpty(\"\") = %v, want nil", got)
	}
	if got := nilIfEmpty("  "); got != nil {
		t.Errorf("nilIfEmpty(whitespace) = %v, want nil", got)
	}
	got := nilIfEmpty("becky-identify")
	if got == nil || *got != "becky-identify" {
		t.Errorf("nilIfEmpty(name) = %v, want pointer to \"becky-identify\"", got)
	}
}

// TestBuildResults verifies rank assignment, duration math, speaker labeling,
// needs_review coercion, and verified_by nulling — the core transcript mapping
// contract — now expressed over the unified rankedItem input.
func TestBuildResults(t *testing.T) {
	items := []rankedItem{
		{
			kind: kindSegment,
			seg: beckydb.Neighbor{
				Segment: beckydb.Segment{
					SourceFile: "a.mp4", SourceSHA256: "sha", StartTime: 10, EndTime: 15.5,
					Text: "named", SpeakerName: "Defendant", SpeakerConfidence: 0.9,
					NeedsReview: 0, VerifiedBy: "becky-identify",
				},
				Similarity: 0.88,
			},
			matched: "vector",
			fused:   0.5,
		},
		{
			kind: kindSegment,
			seg: beckydb.Neighbor{
				Segment: beckydb.Segment{
					SourceFile: "b.mp4", StartTime: 20, EndTime: 21,
					Text: "anon", SpeakerName: "", NeedsReview: 1, VerifiedBy: "",
				},
				Similarity: 0.71,
			},
			matched: "vector",
			fused:   0.4,
		},
	}
	got := buildResults(nil, items, false, false)
	if len(got) != 2 {
		t.Fatalf("want 2 results, got %d", len(got))
	}
	if got[0].Rank != 1 || got[1].Rank != 2 {
		t.Errorf("ranks = %d,%d want 1,2", got[0].Rank, got[1].Rank)
	}
	if got[0].Kind != "transcript" || got[1].Kind != "transcript" {
		t.Errorf("kind = %q,%q want transcript,transcript", got[0].Kind, got[1].Kind)
	}
	if got[0].Matched != "vector" || got[1].Matched != "vector" {
		t.Errorf("matched = %q,%q want vector,vector", got[0].Matched, got[1].Matched)
	}
	if got[0].Duration != 5.5 {
		t.Errorf("duration[0] = %v want 5.5", got[0].Duration)
	}
	if got[0].Speaker != "Defendant" || got[1].Speaker != unidentifiedSpeaker {
		t.Errorf("speakers = %q,%q", got[0].Speaker, got[1].Speaker)
	}
	if got[0].NeedsReview != false || got[1].NeedsReview != true {
		t.Errorf("needs_review = %v,%v want false,true", got[0].NeedsReview, got[1].NeedsReview)
	}
	if got[0].VerifiedBy == nil || *got[0].VerifiedBy != "becky-identify" {
		t.Errorf("verified_by[0] = %v want becky-identify", got[0].VerifiedBy)
	}
	if got[1].VerifiedBy != nil {
		t.Errorf("verified_by[1] = %v want nil", got[1].VerifiedBy)
	}
}

// TestBuildResultsOCR verifies an OCR rankedItem maps to a kind="ocr" result row
// carrying frame + timestamp + recognition confidence + category + matched="ocr".
func TestBuildResultsOCR(t *testing.T) {
	items := []rankedItem{
		{
			kind: kindOCR,
			ocr: beckydb.OCRHit{
				OCRLine: beckydb.OCRLine{
					SourceFile: "clip.mp4", SourceSHA256: "sha", FramePath: "osint/frame_000012.jpg",
					Timestamp: 13.0, FrameIndex: 12, Text: "Greenwood IN 46143",
					Confidence: 0.97, Category: "candidate_address", BBoxJSON: "[10,20,300,60]",
				},
				Rank: 1,
			},
			matched: "ocr",
			fused:   0.5,
		},
	}
	got := buildResults(nil, items, false, false)
	if len(got) != 1 {
		t.Fatalf("want 1 result, got %d", len(got))
	}
	r := got[0]
	if r.Kind != "ocr" || r.Matched != "ocr" {
		t.Errorf("kind/matched = %q/%q want ocr/ocr", r.Kind, r.Matched)
	}
	if r.Text != "Greenwood IN 46143" {
		t.Errorf("text = %q", r.Text)
	}
	if r.Timestamp != 13.0 || r.FrameIndex != 12 {
		t.Errorf("timestamp/frame_index = %v/%d want 13/12", r.Timestamp, r.FrameIndex)
	}
	if r.OCRConfidence != 0.97 {
		t.Errorf("ocr_confidence = %v want 0.97", r.OCRConfidence)
	}
	if r.Category != "candidate_address" {
		t.Errorf("category = %q want candidate_address", r.Category)
	}
	if r.FramePath != "osint/frame_000012.jpg" {
		t.Errorf("frame_path = %q", r.FramePath)
	}
	if r.Similarity != 0 {
		t.Errorf("similarity = %v want 0 (no cosine for OCR)", r.Similarity)
	}
}

func TestBuildStats(t *testing.T) {
	t.Run("empty -> zero avg", func(t *testing.T) {
		st := buildStats(nil)
		if st.TotalResults != 0 || st.AvgConfidence != 0 {
			t.Errorf("empty stats = %+v", st)
		}
	})
	t.Run("counts and average", func(t *testing.T) {
		results := []result{
			{Kind: "transcript", Speaker: "Defendant", Similarity: 0.8},
			{Kind: "transcript", Speaker: unidentifiedSpeaker, Similarity: 0.6},
			{Kind: "transcript", Speaker: unidentifiedSpeaker, Similarity: 0.4},
		}
		st := buildStats(results)
		if st.TotalResults != 3 {
			t.Errorf("total = %d want 3", st.TotalResults)
		}
		if st.TranscriptHits != 3 || st.OCRHits != 0 {
			t.Errorf("transcript/ocr = %d/%d want 3/0", st.TranscriptHits, st.OCRHits)
		}
		if st.NamedSpeakers != 1 || st.Unidentified != 2 {
			t.Errorf("named/unid = %d/%d want 1/2", st.NamedSpeakers, st.Unidentified)
		}
		if st.AvgConfidence != 0.6 { // (0.8+0.6+0.4)/3
			t.Errorf("avg = %v want 0.6", st.AvgConfidence)
		}
	})
	t.Run("mixed transcript + ocr", func(t *testing.T) {
		// Two transcript hits (sim 0.8, 0.4) and one OCR hit. The OCR hit must NOT
		// count toward speaker tallies and must NOT dilute the similarity average
		// (which is over transcript hits only): avg = (0.8+0.4)/2 = 0.6.
		results := []result{
			{Kind: "transcript", Speaker: "Defendant", Similarity: 0.8},
			{Kind: "ocr", Text: "Greenwood IN 46143", OCRConfidence: 0.97},
			{Kind: "transcript", Speaker: unidentifiedSpeaker, Similarity: 0.4},
		}
		st := buildStats(results)
		if st.TotalResults != 3 {
			t.Errorf("total = %d want 3", st.TotalResults)
		}
		if st.TranscriptHits != 2 || st.OCRHits != 1 {
			t.Errorf("transcript/ocr = %d/%d want 2/1", st.TranscriptHits, st.OCRHits)
		}
		if st.NamedSpeakers != 1 || st.Unidentified != 1 {
			t.Errorf("named/unid = %d/%d want 1/1 (OCR not counted)", st.NamedSpeakers, st.Unidentified)
		}
		if st.AvgConfidence != 0.6 { // (0.8+0.4)/2, OCR excluded
			t.Errorf("avg = %v want 0.6 (transcript only)", st.AvgConfidence)
		}
	})
}

func TestVecJSON(t *testing.T) {
	got := vecJSON([]float64{0.1, -0.2, 0})
	want := "[0.1,-0.2,0]"
	if got != want {
		t.Errorf("vecJSON = %q want %q", got, want)
	}
}

func TestParseEmbedJSON(t *testing.T) {
	t.Run("clean json", func(t *testing.T) {
		r, ok := parseEmbedJSON(`{"model":"m","dim":1024,"vectors":[[0.1]]}`)
		if !ok || r.Dim != 1024 || len(r.Vectors) != 1 {
			t.Errorf("parse failed: ok=%v r=%+v", ok, r)
		}
	})
	t.Run("json after log noise", func(t *testing.T) {
		s := "Loading weights: 100%\nsome warning\n{\"dim\":1024,\"vectors\":[[0.2]]}"
		r, ok := parseEmbedJSON(s)
		if !ok || r.Dim != 1024 {
			t.Errorf("noisy parse failed: ok=%v r=%+v", ok, r)
		}
	})
	t.Run("skipped payload", func(t *testing.T) {
		r, ok := parseEmbedJSON(`{"skipped":true,"reason":"boom"}`)
		if !ok || !r.Skipped {
			t.Errorf("skipped parse failed: ok=%v r=%+v", ok, r)
		}
	})
	t.Run("no json", func(t *testing.T) {
		if _, ok := parseEmbedJSON("just logs, no json here"); ok {
			t.Error("expected ok=false for non-json input")
		}
	})
}

func TestClock(t *testing.T) {
	tests := map[float64]string{0: "0:00", 5: "0:05", 83.2: "1:23", 605: "10:05", -3: "0:00"}
	for in, want := range tests {
		if got := clock(in); got != want {
			t.Errorf("clock(%v) = %q want %q", in, got, want)
		}
	}
}

// TestRenderTxt covers the human-readable format for both a transcript hit and an
// on-screen OCR hit, including the empty result set.
func TestRenderTxt(t *testing.T) {
	out := output{
		Query: "q",
		Results: []result{
			{
				Rank: 1, Kind: "transcript", SourceFile: "x/y/video.mp4", Timestamp: 83.2, Duration: 5.1,
				Speaker: "Defendant", Similarity: 0.92, Matched: "hybrid", NeedsReview: false, Text: "hello",
			},
			{
				Rank: 2, Kind: "ocr", SourceFile: "x/y/clip.mp4", Timestamp: 13.0, Matched: "ocr",
				Text: "Greenwood IN 46143", OCRConfidence: 0.97, Category: "candidate_address",
				FramePath: "osint/frame_000012.jpg",
			},
		},
		Stats: stats{TotalResults: 2, TranscriptHits: 1, OCRHits: 1, NamedSpeakers: 1, AvgConfidence: 0.92},
	}
	txt := renderTxt(out)
	// transcript row substrings + OCR row substrings must both appear.
	for _, want := range []string{
		"Defendant", "92% match", "video.mp4", "1:23", "hello",
		"on-screen text", "Greenwood IN 46143", "candidate_address", "frame_000012.jpg", "on-screen(ocr)",
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("renderTxt missing %q in:\n%s", want, txt)
		}
	}
	empty := renderTxt(output{Query: "none"})
	if !strings.Contains(empty, "no results") {
		t.Errorf("empty renderTxt should say 'no results':\n%s", empty)
	}
}
