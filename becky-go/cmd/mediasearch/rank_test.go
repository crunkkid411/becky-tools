package main

import (
	"testing"

	"becky-go/internal/beckydb"
	"becky-go/internal/mediainfo"
)

func row(file, kind string, start, end, sim float64) beckydb.MediaHit {
	return beckydb.MediaHit{SourceFile: file, Kind: kind, Start: start, End: end, Similarity: sim}
}

// TestRankMergesOneMoment: touching strong windows become one hit keeping the best score and
// frame; a weak window is its own hit; best first.
func TestRankMergesOneMoment(t *testing.T) {
	hits := rank([]beckydb.MediaHit{
		row("a.mp4", "picture", 10, 11, 0.70),
		row("a.mp4", "picture", 11, 12, 0.72),
		row("a.mp4", "picture", 12, 13, 0.71),
		row("a.mp4", "picture", 40, 41, 0.50),
	})
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2: %+v", len(hits), hits)
	}
	h := hits[0]
	if h.Start != 10 || h.End != 13 || h.Score != 0.72 || h.Frame != 11 {
		t.Fatalf("merged hit = %+v, want 10-13 score 0.72 frame 11", h)
	}
	if hits[1].Start != 40 {
		t.Fatalf("second hit = %+v", hits[1])
	}
}

// TestTopDropsRepeatedMoment: the same seconds found as picture and sound count once.
func TestTopDropsRepeatedMoment(t *testing.T) {
	got := top([]Hit{
		{File: "a.mp4", Kind: "picture", Start: 10, End: 13, Score: 0.8},
		{File: "a.mp4", Kind: "sound", Start: 12, End: 17, Score: 0.7},
		{File: "b.mp4", Kind: "picture", Start: 10, End: 13, Score: 0.6},
		{File: "a.mp4", Kind: "picture", Start: 30, End: 31, Score: 0.5},
	}, 2)
	if len(got) != 2 || got[0].File != "a.mp4" || got[1].File != "b.mp4" {
		t.Fatalf("top = %+v", got)
	}
}

func TestVerdict(t *testing.T) {
	for in, want := range map[string]string{"Yes, a dog barks.": "yes", `**No** - a cat.`: "no", "Maybe.": ""} {
		if got, _ := verdict(in); got != want {
			t.Errorf("verdict(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestReelOf: video hits Gemma did not reject become clips with half a second of handle, clamped
// to the video; stills and rejected hits are left out.
func TestReelOf(t *testing.T) {
	infos := map[string]mediainfo.Info{`C:\v\a.mp4`: {Duration: 10, FPS: 30}}
	r := reelOf([]Hit{
		{File: `C:\v\a.mp4`, Kind: "shot", Start: 0.2, End: 9.8, What: "a wide shot", Gemma: "yes"},
		{File: `C:\v\a.mp4`, Kind: "picture", Start: 3, End: 4, Gemma: "no"},
		{File: `C:\v\b.jpg`, Kind: "picture"},
	}, "dog", infos)
	if len(r.Clips) != 1 {
		t.Fatalf("got %d clips, want 1: %+v", len(r.Clips), r.Clips)
	}
	c := r.Clips[0]
	if c.ID != "c1" || c.In != 0 || c.Out != 10 || c.Label != "a wide shot" || c.Meta.SourceFPS != 30 {
		t.Fatalf("clip = %+v", c)
	}
}

// TestWindowCapped: a long hit is shown to Gemma as checkSec around its best frame (a 27 s shot
// overflowed the model's context); a short one keeps its second either side.
func TestWindowCapped(t *testing.T) {
	if a, b := window(Hit{Kind: "shot", Start: 13, End: 40, Frame: 32}, 40); a != 29 || b != 35 {
		t.Fatalf("long shot window = %v-%v, want 29-35", a, b)
	}
	if a, b := window(Hit{Kind: "picture", Start: 3, End: 4, Frame: 3}, 40); a != 2 || b != 5 {
		t.Fatalf("short window = %v-%v, want 2-5", a, b)
	}
}
