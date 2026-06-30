package main

// qmd_test.go covers the pure parsing/resolution helpers of the qmd integration —
// no qmd binary, no GPU. The live hybrid/keyword shell-out is exercised by hand.

import "testing"

// TestParseQmdJSON: tolerate leading progress text + trailing bytes around the array.
func TestParseQmdJSON(t *testing.T) {
	raw := "Expanding query... (5s)\n" +
		`[{"docid":"#a","score":0.7,"file":"qmd://transcripts/x.md","title":"ring","snippet":"hello"}]` +
		"\ndone\n"
	hits, err := parseQmdJSON([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hits) != 1 || hits[0].Title != "ring" || hits[0].Score != 0.7 {
		t.Fatalf("bad parse: %+v", hits)
	}
	if _, err := parseQmdJSON([]byte("no json here")); err == nil {
		t.Error("non-JSON output should error so the caller can fall back")
	}
}

// TestQmdSourceName: frontmatter source wins; else title + ".srt".
func TestQmdSourceName(t *testing.T) {
	fm := qmdHit{Title: "ring", Snippet: "@@ -1,4 @@\nsource: \"real_name.srt\"\nvideo_id: \"\""}
	if got := qmdSourceName(fm); got != "real_name.srt" {
		t.Fatalf("frontmatter should win, got %q", got)
	}
	noFm := qmdHit{Title: "18_2026-05-19-penguin_parakeet_transcription", Snippet: "**[00:01:02]** words"}
	if got := qmdSourceName(noFm); got != "18_2026-05-19-penguin_parakeet_transcription.srt" {
		t.Fatalf("title fallback wrong, got %q", got)
	}
	if got := qmdSourceName(qmdHit{}); got != "" {
		t.Fatalf("empty hit should yield empty source, got %q", got)
	}
}

// TestFirstQmdTimecode: parse **[H:MM:SS]** to seconds; -1 when absent.
func TestFirstQmdTimecode(t *testing.T) {
	if got := firstQmdTimecode("**[01:02:03]** hi"); got != 3723 {
		t.Fatalf("01:02:03 -> %v want 3723", got)
	}
	if got := firstQmdTimecode("blah [0:00:07] blah"); got != 7 {
		t.Fatalf("0:00:07 -> %v want 7", got)
	}
	if got := firstQmdTimecode("no timecode"); got != -1 {
		t.Fatalf("absent -> %v want -1", got)
	}
}

// TestCleanQmdSnippet: drop diff header + frontmatter + markers, keep readable text.
func TestCleanQmdSnippet(t *testing.T) {
	s := "@@ -1,4 @@ (0 before, 17 after)\n---\nsource: \"x.srt\"\ndate: \"2026-05-19\"\n**[00:00:00]** do I run out of time. My name is Penguin."
	got := cleanQmdSnippet(s)
	if got != "do I run out of time. My name is Penguin." {
		t.Fatalf("clean snippet = %q", got)
	}
}

// TestResolveCueSnapsToNearest: a coarse timecode snaps to the nearest REAL .srt cue
// (the fixture has cues at 1s/4s/7s), proving timecodes come from the transcript.
func TestResolveCueSnapsToNearest(t *testing.T) {
	app, _ := openFixture(t)
	v, ok := app.videoByTranscript("ring.srt")
	if !ok {
		t.Fatal("videoByTranscript(ring.srt) should resolve the fixture video")
	}
	// coarse 4.6s -> the 4s cue ("bring Penguin back ...")
	start, _, text := app.resolveCue(v.TranscriptPath, 4.6)
	if start != 4 {
		t.Fatalf("snap to nearest cue start = %v, want 4", start)
	}
	if text == "" {
		t.Fatal("resolved cue should carry the .srt text")
	}
	// t<0 (no timecode in snippet) -> the first cue (1s).
	if s, _, _ := app.resolveCue(v.TranscriptPath, -1); s != 1 {
		t.Fatalf("no-timecode should use first cue (1s), got %v", s)
	}
}

// TestResolveQmdHitPrecise: a hit titled "ring" with a coarse 7s marker resolves to the
// fixture video + the precise 7s cue (start FROM the .srt, not the .md).
func TestResolveQmdHitPrecise(t *testing.T) {
	app, _ := openFixture(t)
	h := qmdHit{Title: "ring", Score: 0.8, Snippet: "**[00:00:07]** nothing else happened that night"}
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
	h := qmdHit{Title: "some_absent_video", Snippet: "**[00:00:03]** words"}
	r, ok := app.resolveQmdHit(h)
	if !ok || !r.TranscriptOnly || r.Source != "" {
		t.Fatalf("absent-source hit should be transcript-only, got ok=%v %+v", ok, r)
	}
}
