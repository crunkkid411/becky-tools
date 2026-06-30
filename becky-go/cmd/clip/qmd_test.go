package main

// qmd_test.go covers the App-level resolution of qmd hits to precise .srt cues. The
// pure qmd client (parse/env/source-mapping/timecode) is tested in internal/qmd.

import (
	"testing"

	"becky-go/internal/qmd"
)

// TestResolveCueSnapsToNearest: a coarse timecode snaps to the nearest REAL .srt cue
// (the fixture has cues at 1s/4s/7s), proving timecodes come from the transcript.
func TestResolveCueSnapsToNearest(t *testing.T) {
	app, _ := openFixture(t)
	v, ok := app.videoByTranscript("ring.srt")
	if !ok {
		t.Fatal("videoByTranscript(ring.srt) should resolve the fixture video")
	}
	start, _, text := app.resolveCue(v.TranscriptPath, 4.6) // -> the 4s cue
	if start != 4 {
		t.Fatalf("snap to nearest cue start = %v, want 4", start)
	}
	if text == "" {
		t.Fatal("resolved cue should carry the .srt text")
	}
	if s, _, _ := app.resolveCue(v.TranscriptPath, -1); s != 1 {
		t.Fatalf("no-timecode should use first cue (1s), got %v", s)
	}
}

// TestResolveQmdHitPrecise: a hit titled "ring" with a coarse 7s marker resolves to the
// fixture video + the precise 7s cue (start FROM the .srt, not the .md).
func TestResolveQmdHitPrecise(t *testing.T) {
	app, _ := openFixture(t)
	h := qmd.Hit{Title: "ring", Score: 0.8, Snippet: "**[00:00:07]** nothing else happened that night"}
	r, ok := app.resolveQmdHit(h)
	if !ok {
		t.Fatal("hit should resolve")
	}
	if r.TranscriptOnly {
		t.Fatal("ring.mp4 is in the folder -> should be playable, not transcript-only")
	}
	if r.Start != 7 {
		t.Fatalf("precise start = %v, want 7 (from the .srt)", r.Start)
	}
	if r.Source == "" || baseName(r.Source) != "ring.mp4" {
		t.Fatalf("source should be ring.mp4, got %q", r.Source)
	}
}

// TestResolveQmdHitTranscriptOnly: a hit whose video isn't in the folder is shown as
// transcript-only (not dropped).
func TestResolveQmdHitTranscriptOnly(t *testing.T) {
	app, _ := openFixture(t)
	h := qmd.Hit{Title: "some_absent_video", Snippet: "**[00:00:03]** words"}
	r, ok := app.resolveQmdHit(h)
	if !ok || !r.TranscriptOnly || r.Source != "" {
		t.Fatalf("absent-source hit should be transcript-only, got ok=%v %+v", ok, r)
	}
}
