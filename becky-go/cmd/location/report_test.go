package main

import (
	"encoding/json"
	"strings"
	"testing"

	"becky-go/internal/location"
)

func hist(bin int) []float64 {
	h := make([]float64, 64)
	h[bin%64] = 1.0
	return h
}

// buildReport must produce a valid schema with the expected room count, verdict
// level, and non-nil slices (so the JSON is stable).
func TestBuildReport_SchemaAndVerdict(t *testing.T) {
	clips := []location.Clip{
		{Index: 0, Path: "a.mp4", Duration: 10, KeyframeN: 2, Print: location.Fingerprint{DecorHash: 0x0, ColorHist: hist(0)}},
		{Index: 1, Path: "b.mp4", Duration: 10, KeyframeN: 2, Print: location.Fingerprint{DecorHash: 0x3, ColorHist: hist(0)}},
	}
	thr := location.DefaultThresholds()
	cr := location.Cluster(clips, thr)
	dw, v := location.GroupDwellings(clips, cr, thr, location.DefaultDwellingParams())

	r := buildReport(clips, cr, dw, v, "phash", "talking-head", nil, nil)
	if r.ClipCount != 2 {
		t.Fatalf("clip_count = %d, want 2", r.ClipCount)
	}
	if r.RoomCount != 1 {
		t.Fatalf("room_count = %d, want 1 (same room)", r.RoomCount)
	}
	if r.Verdict.Level != string(location.SameRoom) {
		t.Fatalf("verdict = %s, want SAME_ROOM", r.Verdict.Level)
	}
	// Must round-trip through JSON and carry the provenance note + a same-room pair.
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, "not a geolocation conclusion") {
		t.Fatalf("report must carry the provenance note")
	}
	if len(r.PairVerdicts) == 0 || r.PairVerdicts[0].Level != "SAME_ROOM" {
		t.Fatalf("expected a SAME_ROOM pair verdict, got %+v", r.PairVerdicts)
	}
	if r.PairVerdicts[0].ExhibitHint == "" {
		t.Fatalf("same-room pair should carry a framematch exhibit hint")
	}
}

// A degraded clip must land in degraded[] and not be clustered.
func TestBuildReport_Degraded(t *testing.T) {
	clips := []location.Clip{
		{Index: 0, Path: "a.mp4", Print: location.Fingerprint{DecorHash: 0x0, ColorHist: hist(0)}},
		{Index: 1, Path: "bad.mp4", Degraded: "no upright keyframes"},
		{Index: 2, Path: "c.mp4", Print: location.Fingerprint{DecorHash: 0x1, ColorHist: hist(0)}},
	}
	thr := location.DefaultThresholds()
	cr := location.Cluster(clips, thr)
	dw, v := location.GroupDwellings(clips, cr, thr, location.DefaultDwellingParams())
	r := buildReport(clips, cr, dw, v, "phash", "talking-head", nil, nil)

	if len(r.Degraded) != 1 || r.Degraded[0].Index != 1 {
		t.Fatalf("expected clip 1 in degraded[], got %+v", r.Degraded)
	}
	if len(r.Clips) != 2 {
		t.Fatalf("expected 2 non-degraded clips reported, got %d", len(r.Clips))
	}
}

// renderSummary leads with the verdict and lists rooms.
func TestRenderSummary_LeadsWithVerdict(t *testing.T) {
	clips := []location.Clip{
		{Index: 0, Path: "a.mp4", Print: location.Fingerprint{DecorHash: 0x0, ColorHist: hist(0)}},
		{Index: 1, Path: "b.mp4", Print: location.Fingerprint{DecorHash: 0x1, ColorHist: hist(0)}},
	}
	thr := location.DefaultThresholds()
	cr := location.Cluster(clips, thr)
	dw, v := location.GroupDwellings(clips, cr, thr, location.DefaultDwellingParams())
	r := buildReport(clips, cr, dw, v, "phash", "talking-head", nil, nil)

	out := renderSummary(r)
	if !strings.HasPrefix(out, "VERDICT:") {
		t.Fatalf("summary must lead with VERDICT, got:\n%s", out)
	}
	if !strings.Contains(out, "Room 1") {
		t.Fatalf("summary must list rooms, got:\n%s", out)
	}
}

func TestParsePair(t *testing.T) {
	p, ok := parsePair("0,2")
	if !ok || p != [2]int{0, 2} {
		t.Fatalf("parsePair(0,2) = %v,%v", p, ok)
	}
	if _, ok := parsePair("bad"); ok {
		t.Fatalf("parsePair should reject malformed input")
	}
}

func TestIsVideo(t *testing.T) {
	if !isVideo("x.MP4") || !isVideo("y.mov") {
		t.Fatalf("video extensions should be recognized (case-insensitive)")
	}
	if isVideo("z.txt") {
		t.Fatalf(".txt is not a video")
	}
}
