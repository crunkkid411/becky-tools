package main

import "testing"

// resolveNumClusters maps the --min/--max-speakers knobs onto sherpa's num_clusters.
// These rules are load-bearing: getting them wrong silently pins the wrong count.
func TestResolveNumClusters(t *testing.T) {
	cases := []struct {
		name           string
		minSpk, maxSpk int
		want           int
	}{
		{"auto by default", 1, 0, -1},
		{"pinned when min==max", 2, 2, 2},
		{"min>1 alone pins min", 3, 0, 3},
		{"a wide min/max range stays auto", 1, 5, -1},
		{"min 2 max 4 stays auto (range)", 2, 4, -1},
		{"single-speaker floor stays auto", 1, 0, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveNumClusters(c.minSpk, c.maxSpk); got != c.want {
				t.Errorf("resolveNumClusters(%d,%d) = %d, want %d", c.minSpk, c.maxSpk, got, c.want)
			}
		})
	}
}

// groupBySpeaker turns the helper's flat (start,end,speaker) list into the schema's
// per-speaker grouping, ordered by speaker id, each speaker's segments by start.
func TestGroupBySpeakerOrdersAndGroups(t *testing.T) {
	flat := []flatSegment{
		{Start: 5.0, End: 6.0, Speaker: "SPEAKER_01"},
		{Start: 0.0, End: 2.0, Speaker: "SPEAKER_00"},
		{Start: 2.0, End: 3.0, Speaker: "SPEAKER_01"},
		{Start: 3.0, End: 4.0, Speaker: "SPEAKER_00"},
	}
	got := groupBySpeaker(flat)
	if len(got) != 2 {
		t.Fatalf("expected 2 speakers, got %d", len(got))
	}
	if got[0].ID != "SPEAKER_00" || got[1].ID != "SPEAKER_01" {
		t.Fatalf("speakers not ordered by id: %s, %s", got[0].ID, got[1].ID)
	}
	// SPEAKER_01's segments must be start-sorted (2.0 before 5.0).
	if got[1].Segments[0].Start != 2.0 || got[1].Segments[1].Start != 5.0 {
		t.Errorf("SPEAKER_01 segments not start-sorted: %+v", got[1].Segments)
	}
	// Every segment carries the documented fixed confidence.
	for _, sp := range got {
		for _, s := range sp.Segments {
			if s.Confidence != segmentConfidence {
				t.Errorf("segment confidence = %v, want %v", s.Confidence, segmentConfidence)
			}
		}
	}
}

// A single-speaker flat list groups to exactly ONE speaker — the single-speaker
// guarantee the hardening must preserve (no phantom split in the grouping layer).
func TestGroupBySpeakerSingleSpeaker(t *testing.T) {
	flat := []flatSegment{
		{Start: 0.0, End: 10.0, Speaker: "SPEAKER_00"},
		{Start: 11.0, End: 20.0, Speaker: "SPEAKER_00"},
	}
	got := groupBySpeaker(flat)
	if len(got) != 1 {
		t.Fatalf("single-speaker input must group to 1 speaker, got %d", len(got))
	}
	if len(got[0].Segments) != 2 {
		t.Errorf("expected both segments under the one speaker, got %d", len(got[0].Segments))
	}
}

// parseHelperJSON must tolerate leading C++/ONNX log noise and still find the JSON
// line — the real failure mode on this machine (sherpa prints banners to stdout).
func TestParseHelperJSONToleratesLogNoise(t *testing.T) {
	noisy := "I0608 sherpa-onnx loading model...\n" +
		"some banner line\n" +
		`{"duration":50.0,"sample_rate":16000,"num_speakers":2,"segments":[{"start":0,"end":1,"speaker":"SPEAKER_00"}]}` + "\n"
	res, ok := parseHelperJSON(noisy)
	if !ok {
		t.Fatalf("parseHelperJSON failed to find JSON amid log noise")
	}
	if res.NumSpeakers != 2 || len(res.Segments) != 1 {
		t.Errorf("parsed wrong payload: %+v", res)
	}
}

// A skipped helper result is recognized (so the Go caller surfaces the reason rather
// than crashing).
func TestParseHelperJSONSkipped(t *testing.T) {
	res, ok := parseHelperJSON(`{"skipped":true,"reason":"model not found"}`)
	if !ok || !res.Skipped || res.Reason != "model not found" {
		t.Fatalf("skipped result not parsed: ok=%v res=%+v", ok, res)
	}
}
