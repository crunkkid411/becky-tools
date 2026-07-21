package main

// forensic_test.go — H-7's forensic_query verb, exercised offline through the
// runJudge/runHits seams. No test shells the real judge (it costs an LLM call)
// or needs qmd/models — the fakes write the same artifacts the real tools do,
// so the whole orchestration (resolve bins → judge → hits → LoadReel →
// questions → events) runs for real.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"becky-go/internal/edl"
)

// fakeForensicBins points BECKY_JUDGE/BECKY_HITS at files that exist, so
// resolveForensicBin succeeds without real binaries on PATH.
func fakeForensicBins(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	j := filepath.Join(dir, "becky-judge")
	h := filepath.Join(dir, "becky-hits")
	mustWrite(t, j, "#!/bin/sh\n")
	mustWrite(t, h, "#!/bin/sh\n")
	t.Setenv("BECKY_JUDGE", j)
	t.Setenv("BECKY_HITS", h)
}

// swapForensicSeams installs fakes for the two exec seams and restores them
// on cleanup.
func swapForensicSeams(t *testing.T,
	judge func(ctx context.Context, bin, folder, query, outPath string) error,
	hits func(ctx context.Context, bin, folder, hitsPath, outPath string) error) {
	t.Helper()
	origJudge, origHits := runJudge, runHits
	runJudge, runHits = judge, hits
	t.Cleanup(func() { runJudge, runHits = origJudge, origHits })
}

func TestForensicQueryRunsPipelineAndLoadsReel(t *testing.T) {
	app := NewApp()
	app.workDir = t.TempDir()
	dir := fixtureFolder(t)
	if _, err := app.OpenFolder(dir); err != nil {
		t.Fatalf("open folder: %v", err)
	}
	fakeForensicBins(t)

	// The fake judge writes a hit-list; the fake hits tool writes a real reel
	// (one clip on the fixture video) plus the questions sidecar — exactly the
	// artifacts the real tools produce, at the paths forensic_query dictates.
	var gotQuery string
	swapForensicSeams(t,
		func(_ context.Context, _, folder, query, outPath string) error {
			gotQuery = query
			mustWrite(t, outPath, `{"folder":`+jsonStr(folder)+`,"hits":[{"srt":"ring.srt","t":"00:00:02,000"}]}`)
			return nil
		},
		func(_ context.Context, _, folder, hitsPath, outPath string) error {
			if !fileExists(hitsPath) {
				t.Fatalf("hits stage ran before the judge wrote %s", hitsPath)
			}
			reel := edl.Reel{Version: "1", Name: "forensic", Clips: []edl.Clip{
				{ID: "c1", Source: filepath.Join(folder, "ring.mp4"), In: 1, Out: 3, Label: "money for the cat"},
			}}
			if err := edl.Save(outPath, reel); err != nil {
				t.Fatalf("save fake reel: %v", err)
			}
			q := strings.TrimSuffix(outPath, filepath.Ext(outPath)) + ".questions.json"
			mustWrite(t, q, `{"questions":[{"id":"q1","question":"Who offered the reward?","clip_ids":["c1"]}]}`)
			return nil
		})

	// Capture the H-5 activity events the verb narrates with.
	var mu sync.Mutex
	var events []string
	app.emit = func(kind, source, text string) {
		mu.Lock()
		events = append(events, kind+"/"+source)
		mu.Unlock()
	}

	res, err := app.ForensicQuery("  money for the cat  ")
	if err != nil {
		t.Fatalf("ForensicQuery: %v", err)
	}
	if gotQuery != "money for the cat" {
		t.Errorf("judge received query %q, want it trimmed", gotQuery)
	}
	if res.Clips != 1 || len(res.Timeline.Clips) != 1 {
		t.Fatalf("clips = %d / %d on timeline, want 1", res.Clips, len(res.Timeline.Clips))
	}
	if res.Timeline.Clips[0].Label != "money for the cat" {
		t.Errorf("clip label = %q, want the hit's label", res.Timeline.Clips[0].Label)
	}
	if res.Reel != filepath.Join(dir, forensicReelName) {
		t.Errorf("reel path = %q, want %q in the case folder", res.Reel, filepath.Join(dir, forensicReelName))
	}
	if res.Note != "" {
		t.Errorf("note = %q, want clean run", res.Note)
	}
	// The questions sidecar reached the Q&A panel.
	if qs := app.Questions(); len(qs) != 1 || qs[0].Question != "Who offered the reward?" {
		t.Errorf("questions = %+v, want the sidecar's one question", qs)
	}
	// One Ctrl+Z reverses the whole load (LoadReel pushes exactly one snapshot).
	if tl, changed := app.Undo(); !changed || len(tl.Clips) != 0 {
		t.Errorf("undo -> changed=%v clips=%d, want one press back to the empty timeline", changed, len(tl.Clips))
	}
	// Events bracket the run: started → progress → done.
	mu.Lock()
	got := strings.Join(events, ",")
	mu.Unlock()
	want := "started/forensic_query,progress/forensic_query,done/forensic_query"
	if got != want {
		t.Errorf("events = %q, want %q", got, want)
	}
}

func TestForensicQueryGuards(t *testing.T) {
	app := NewApp()
	app.workDir = t.TempDir()

	if _, err := app.ForensicQuery("   "); err == nil {
		t.Error("empty query must be a typed error")
	}
	if _, err := app.ForensicQuery("harassment"); err == nil || !strings.Contains(err.Error(), "folder") {
		t.Errorf("no open folder must say so, got: %v", err)
	}
}

func TestForensicQueryMissingBinaryIsPlainLanguage(t *testing.T) {
	app := NewApp()
	app.workDir = t.TempDir()
	if _, err := app.OpenFolder(fixtureFolder(t)); err != nil {
		t.Fatalf("open folder: %v", err)
	}
	t.Setenv("BECKY_JUDGE", filepath.Join(t.TempDir(), "nope"))
	_, err := app.ForensicQuery("harassment")
	if err == nil || !strings.Contains(err.Error(), "becky-judge") {
		t.Errorf("missing judge binary must name the tool, got: %v", err)
	}
}

func TestForensicQueryJudgeFailureDoesNotTouchTimeline(t *testing.T) {
	app := NewApp()
	app.workDir = t.TempDir()
	dir := fixtureFolder(t)
	if _, err := app.OpenFolder(dir); err != nil {
		t.Fatalf("open folder: %v", err)
	}
	fakeForensicBins(t)
	swapForensicSeams(t,
		func(_ context.Context, _, _, _, _ string) error {
			return os.ErrDeadlineExceeded
		},
		func(_ context.Context, _, _, _, _ string) error {
			t.Fatal("hits stage must not run after a judge failure")
			return nil
		})

	if _, err := app.ForensicQuery("harassment"); err == nil {
		t.Fatal("judge failure must propagate as an error")
	}
	if tl := app.Timeline(); len(tl.Clips) != 0 {
		t.Errorf("timeline gained %d clips from a failed run, want 0", len(tl.Clips))
	}
	if tl, changed := app.Undo(); changed {
		t.Errorf("failed run left an undo entry (timeline now %d clips)", len(tl.Clips))
	}
}

func TestCallForensicQueryViaBridge(t *testing.T) {
	app := NewApp()
	app.workDir = t.TempDir()
	// No folder open: the verb must exist in the dispatch table (not "unknown
	// command") and answer with its own plain-language guard.
	r := callEnv(t, app, "forensic_query", `{"query":"harassment"}`)
	if r.OK {
		t.Fatal("forensic_query with no folder open must fail")
	}
	if strings.Contains(r.Error, "unknown command") {
		t.Fatal("forensic_query is not wired into the dispatch table")
	}
	if !strings.Contains(r.Error, "folder") {
		t.Errorf("error = %q, want the open-a-folder guard", r.Error)
	}
}
