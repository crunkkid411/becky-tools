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
	judge func(ctx context.Context, bin, folder, query, rubric, aliases, outPath string) error,
	hits func(ctx context.Context, bin, folder, hitsPath, outPath string) error) {
	t.Helper()
	origJudge, origHits := runJudge, runHits
	runJudge, runHits = judge, hits
	t.Cleanup(func() { runJudge, runHits = origJudge, origHits })
}

// fakeQmdUpdate stubs the qmd re-index seam to a no-op so a test never shells
// the real qmd binary just because the catch-up sweep correctly found a
// fixture transcript with no .md locator yet and converted it.
func fakeQmdUpdate(t *testing.T) {
	t.Helper()
	orig := runQmdUpdate
	runQmdUpdate = func() error { return nil }
	t.Cleanup(func() { runQmdUpdate = orig })
}

// noGuideAnywhere makes guide resolution come up empty deterministically:
// clears BECKY_JUDGE_GUIDE and points the hardcoded wiki default at a path
// that does not exist (on Jordan's machine the real wiki guide DOES exist,
// which would otherwise leak into every test).
func noGuideAnywhere(t *testing.T) {
	t.Helper()
	t.Setenv("BECKY_JUDGE_GUIDE", "")
	orig := defaultForensicGuide
	defaultForensicGuide = filepath.Join(t.TempDir(), "no-such-guide.md")
	t.Cleanup(func() { defaultForensicGuide = orig })
}

func TestForensicQueryRunsPipelineAndLoadsReel(t *testing.T) {
	app := NewApp()
	app.workDir = t.TempDir()
	dir := fixtureFolder(t)
	if _, err := app.OpenFolder(dir); err != nil {
		t.Fatalf("open folder: %v", err)
	}
	fakeQmdUpdate(t)
	fakeForensicBins(t)
	noGuideAnywhere(t)
	// The case folder carries the conventional guide file — the judge MUST
	// receive it as --rubric (the 0-hits regression: two real runs judged
	// everything away because the verb sent no case context at all).
	folderGuide := filepath.Join(dir, forensicGuideNames[0])
	mustWrite(t, folderGuide, "# rubric + alias map")

	// The fake judge writes a hit-list; the fake hits tool writes a real reel
	// (one clip on the fixture video) plus the questions sidecar — exactly the
	// artifacts the real tools produce, at the paths forensic_query dictates.
	var gotQuery, gotRubric, gotAliases string
	swapForensicSeams(t,
		func(_ context.Context, _, folder, query, rubric, aliases, outPath string) error {
			gotQuery, gotRubric, gotAliases = query, rubric, aliases
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

	res, err := app.ForensicQuery("  money for the cat  ", "", "")
	if err != nil {
		t.Fatalf("ForensicQuery: %v", err)
	}
	if gotQuery != "money for the cat" {
		t.Errorf("judge received query %q, want it trimmed", gotQuery)
	}
	if gotRubric != folderGuide {
		t.Errorf("judge received rubric %q, want the case-folder guide %q", gotRubric, folderGuide)
	}
	if gotAliases != "" {
		t.Errorf("judge received aliases %q, want none (the guide file carries the alias map)", gotAliases)
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
	// Events bracket the run: started → progress (catch-up index sweep, the
	// fixture's ring.srt has no .md locator yet) → progress (judged) → done.
	mu.Lock()
	got := strings.Join(events, ",")
	mu.Unlock()
	want := "started/forensic_query,progress/forensic_query,progress/forensic_query,done/forensic_query"
	if got != want {
		t.Errorf("events = %q, want %q", got, want)
	}

	// The sweep actually wrote a real qmd locator for the fixture transcript.
	mdFiles, err := os.ReadDir(filepath.Join(dir, "_md"))
	if err != nil || len(mdFiles) != 1 {
		t.Errorf("catch-up sweep should have written 1 .md locator into _md, got %v (err=%v)", mdFiles, err)
	}
}

func TestForensicQueryGuards(t *testing.T) {
	app := NewApp()
	app.workDir = t.TempDir()

	if _, err := app.ForensicQuery("   ", "", ""); err == nil {
		t.Error("empty query must be a typed error")
	}
	if _, err := app.ForensicQuery("harassment", "", ""); err == nil || !strings.Contains(err.Error(), "folder") {
		t.Errorf("no open folder must say so, got: %v", err)
	}
}

func TestForensicQueryMissingBinaryIsPlainLanguage(t *testing.T) {
	app := NewApp()
	app.workDir = t.TempDir()
	if _, err := app.OpenFolder(fixtureFolder(t)); err != nil {
		t.Fatalf("open folder: %v", err)
	}
	fakeQmdUpdate(t)
	t.Setenv("BECKY_JUDGE", filepath.Join(t.TempDir(), "nope"))
	_, err := app.ForensicQuery("harassment", "", "")
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
	fakeQmdUpdate(t)
	fakeForensicBins(t)
	swapForensicSeams(t,
		func(_ context.Context, _, _, _, _, _, _ string) error {
			return os.ErrDeadlineExceeded
		},
		func(_ context.Context, _, _, _, _ string) error {
			t.Fatal("hits stage must not run after a judge failure")
			return nil
		})

	if _, err := app.ForensicQuery("harassment", "", ""); err == nil {
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

// TestResolveForensicGuide pins the guide resolution order with exact values:
// case-folder conventional file → BECKY_JUDGE_GUIDE → the wiki default → "".
func TestResolveForensicGuide(t *testing.T) {
	folder := t.TempDir()
	noGuideAnywhere(t)

	if got := resolveForensicGuide(folder); got != "" {
		t.Fatalf("nothing configured: guide = %q, want \"\"", got)
	}

	// The wiki default kicks in once it exists.
	mustWrite(t, defaultForensicGuide, "# wiki guide")
	if got := resolveForensicGuide(folder); got != defaultForensicGuide {
		t.Errorf("wiki default: guide = %q, want %q", got, defaultForensicGuide)
	}

	// BECKY_JUDGE_GUIDE beats the wiki default (passed through as-is, same
	// contract as becky-judge's own env fallback).
	envGuide := filepath.Join(folder, "env-guide.md")
	t.Setenv("BECKY_JUDGE_GUIDE", envGuide)
	if got := resolveForensicGuide(folder); got != envGuide {
		t.Errorf("env: guide = %q, want %q", got, envGuide)
	}

	// A conventional file in the case folder beats everything.
	folderGuide := filepath.Join(folder, forensicGuideNames[0])
	mustWrite(t, folderGuide, "# case guide")
	if got := resolveForensicGuide(folder); got != folderGuide {
		t.Errorf("case folder: guide = %q, want %q", got, folderGuide)
	}

	// The short generic name works too.
	folder2 := t.TempDir()
	t.Setenv("BECKY_JUDGE_GUIDE", "")
	short := filepath.Join(folder2, "_forensic_rubric.md")
	mustWrite(t, short, "# case guide")
	if got := resolveForensicGuide(folder2); got != short {
		t.Errorf("short name: guide = %q, want %q", got, short)
	}
}

// TestForensicQueryPassesCallerRubricAndAliases proves the payload fields ride
// the whole way through the bridge to the judge exec — and that an explicit
// rubric beats the case-folder guide.
func TestForensicQueryPassesCallerRubricAndAliases(t *testing.T) {
	app := NewApp()
	app.workDir = t.TempDir()
	dir := fixtureFolder(t)
	if _, err := app.OpenFolder(dir); err != nil {
		t.Fatalf("open folder: %v", err)
	}
	fakeQmdUpdate(t)
	fakeForensicBins(t)
	noGuideAnywhere(t)
	// A folder guide exists but the caller's rubric must win.
	mustWrite(t, filepath.Join(dir, forensicGuideNames[0]), "# ignored")

	var gotRubric, gotAliases string
	swapForensicSeams(t,
		func(_ context.Context, _, folder, _, rubric, aliases, outPath string) error {
			gotRubric, gotAliases = rubric, aliases
			mustWrite(t, outPath, `{"folder":`+jsonStr(folder)+`,"hits":[{"srt":"ring.srt","t":"00:00:02,000"}]}`)
			return nil
		},
		func(_ context.Context, _, folder, _, outPath string) error {
			reel := edl.Reel{Version: "1", Name: "forensic", Clips: []edl.Clip{
				{ID: "c1", Source: filepath.Join(folder, "ring.mp4"), In: 1, Out: 3, Label: "hit"},
			}}
			return edl.Save(outPath, reel)
		})

	r := callEnv(t, app, "forensic_query",
		`{"query":"harassment","rubric":"my-rubric.md","aliases":"green hair -> Hair Jordan"}`)
	if !r.OK {
		t.Fatalf("forensic_query failed: %s", r.Error)
	}
	if gotRubric != "my-rubric.md" {
		t.Errorf("judge received rubric %q, want the caller's %q", gotRubric, "my-rubric.md")
	}
	if gotAliases != "green hair -> Hair Jordan" {
		t.Errorf("judge received aliases %q, want the caller's inline map", gotAliases)
	}
}

// TestForensicQueryNotesWhenNoGuideAnywhere: when NO case context can be
// found, the judge still runs (recall is never lost) but the reply must SAY
// it ran context-free — the silent version of this is exactly the 0-hits bug.
func TestForensicQueryNotesWhenNoGuideAnywhere(t *testing.T) {
	app := NewApp()
	app.workDir = t.TempDir()
	dir := fixtureFolder(t)
	if _, err := app.OpenFolder(dir); err != nil {
		t.Fatalf("open folder: %v", err)
	}
	fakeQmdUpdate(t)
	fakeForensicBins(t)
	noGuideAnywhere(t)

	var gotRubric string
	swapForensicSeams(t,
		func(_ context.Context, _, folder, _, rubric, _, outPath string) error {
			gotRubric = rubric
			mustWrite(t, outPath, `{"folder":`+jsonStr(folder)+`,"hits":[{"srt":"ring.srt","t":"00:00:02,000"}]}`)
			return nil
		},
		func(_ context.Context, _, folder, _, outPath string) error {
			reel := edl.Reel{Version: "1", Name: "forensic", Clips: []edl.Clip{
				{ID: "c1", Source: filepath.Join(folder, "ring.mp4"), In: 1, Out: 3, Label: "hit"},
			}}
			return edl.Save(outPath, reel)
		})

	res, err := app.ForensicQuery("harassment", "", "")
	if err != nil {
		t.Fatalf("ForensicQuery: %v", err)
	}
	if gotRubric != "" {
		t.Errorf("judge received rubric %q, want none (nothing is configured)", gotRubric)
	}
	if !strings.Contains(res.Note, "built-in rubric") {
		t.Errorf("note = %q, want it to say the judge ran on the built-in rubric only", res.Note)
	}
}
