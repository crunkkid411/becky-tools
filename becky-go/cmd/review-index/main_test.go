package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"becky-go/internal/edl"
)

// TestSearchTimelineDualSystem is Jordan's real timeline shape: the picture is a
// camera file, the grouped sound a separate recorder file. Both have transcripts
// (the camera's is its scratch mic). The search must answer from what the edit
// PLAYS - the recorder - once, at the recorder event's ruler position, and must
// not list the camera file as searchable or untranscribed. A cut-out line in the
// recorder file comes back with no ruler position.
func TestSearchTimelineDualSystem(t *testing.T) {
	dir := t.TempDir()
	cam := filepath.Join(dir, "IURJ0280.MP4")
	rec := filepath.Join(dir, "take1.wav")
	srt := "1\n00:00:05,000 --> 00:00:07,000\nthe scissors are right here\n\n2\n00:00:40,000 --> 00:00:42,000\nscissors again later\n"
	for path, body := range map[string]string{
		cam: "", rec: "",
		filepath.Join(dir, "IURJ0280.srt"): srt, // scratch-mic transcript, same words
		filepath.Join(dir, "take1.srt"):    srt,
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tl := edl.VegasTimeline{Events: []edl.VegasEvent{
		{Source: cam, In: 0, Out: 20, Timeline: 100, Track: 0, Kind: "video"},
		{Source: rec, In: 0.5, Out: 20.5, Timeline: 100, Track: 1, Kind: "audio"},
	}}

	out := searchTimeline(tl, []string{"scissors"}, 0)

	if len(out.Videos) != 1 || out.Videos[0].Path != rec {
		t.Fatalf("searched files = %+v, want only the recorder %s", out.Videos, rec)
	}
	if len(out.TimelineCandidates) != 2 {
		t.Fatalf("got %d hits, want 2 (one on the timeline, one cut out): %+v", len(out.TimelineCandidates), out.TimelineCandidates)
	}
	first := out.TimelineCandidates[0]
	if len(first.OnTimeline) != 1 || first.OnTimeline[0].Timeline != 104.5 || first.OnTimeline[0].Track != 1 {
		t.Errorf("on-timeline hit = %+v, want one place at ruler 104.5 on the audio track", first.OnTimeline)
	}
	if last := out.TimelineCandidates[1]; len(last.OnTimeline) != 0 || last.Timestamp != 40 {
		t.Errorf("cut-out hit = %+v, want the 40s line with no ruler position", last)
	}

	// Without Kind (BeckyCaptions' writer) nothing is filtered.
	tl.Events[0].Kind, tl.Events[1].Kind = "", ""
	if got := searchTimeline(tl, []string{"scissors"}, 0); len(got.Videos) != 2 {
		t.Errorf("unmarked timeline searched %d files, want both", len(got.Videos))
	}
}

func TestSplitTerms(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", []string{}},
		{"blanks only", "   \t  ", []string{}},
		{"single", "cat", []string{"cat"}},
		{"multi collapses spaces", "  cat   near  camera ", []string{"cat", "near", "camera"}},
		{"tabs and newlines", "threat\tto\nhost", []string{"threat", "to", "host"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitTerms(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("splitTerms(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
