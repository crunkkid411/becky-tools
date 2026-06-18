package main

// app_test.go covers the non-GUI backend logic the brief calls out: the
// path-security resolver, the Reel mutation handlers (add/remove/reorder/trim/
// overlay), and the media-path resolver. Fixtures are synthetic files made in
// t.TempDir() (empty .mp4s + a tiny .srt + a beckymeta sidecar) — no models, no
// ffmpeg, no production data. These run under `go test ./cmd/clip/...` everywhere.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// fixtureFolder writes a synthetic case folder: two videos, one with an .srt
// transcript and a beckymeta sidecar. Returns the folder path.
func fixtureFolder(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// video A with a transcript + meta sidecar.
	mustWrite(t, filepath.Join(dir, "ring.mp4"), "not-real-video-bytes")
	mustWrite(t, filepath.Join(dir, "ring.srt"), sampleSRT)
	mustWrite(t, filepath.Join(dir, "ring.mp4.beckymeta.json"),
		`{"date":"2026-06-14","person":"Test Person","location":"Front porch","source_fps":30}`)

	// video B without a transcript.
	mustWrite(t, filepath.Join(dir, "kitchen.mov"), "not-real-video-bytes")

	return dir
}

const sampleSRT = "1\r\n00:00:01,000 --> 00:00:03,000\r\nI will give you money for the cat\r\n\r\n" +
	"2\r\n00:00:04,000 --> 00:00:06,000\r\nbring Penguin back and there is a reward\r\n\r\n" +
	"3\r\n00:00:07,000 --> 00:00:09,000\r\nnothing else happened that night\r\n\r\n"

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", path, err)
	}
}

func openFixture(t *testing.T) (*App, string) {
	t.Helper()
	dir := fixtureFolder(t)
	app := NewApp()
	app.workDir = t.TempDir() // isolate any writes
	if _, err := app.OpenFolder(dir); err != nil {
		t.Fatalf("OpenFolder: %v", err)
	}
	return app, dir
}

func TestOpenFolderIndexesVideosAndTranscripts(t *testing.T) {
	app, _ := openFixture(t)
	fv := app.folderView()
	if len(fv.Videos) != 2 {
		t.Fatalf("want 2 videos, got %d", len(fv.Videos))
	}
	var ring, kitchen *VideoView
	for i := range fv.Videos {
		switch fv.Videos[i].Name {
		case "ring.mp4":
			ring = &fv.Videos[i]
		case "kitchen.mov":
			kitchen = &fv.Videos[i]
		}
	}
	if ring == nil || kitchen == nil {
		t.Fatalf("missing indexed videos: %+v", fv.Videos)
	}
	if !ring.HasTranscript {
		t.Error("ring.mp4 should have a transcript")
	}
	if ring.Date != "2026-06-14" || ring.Person != "Test Person" || ring.SourceFPS != 30 {
		t.Errorf("ring meta not loaded from sidecar: %+v", ring)
	}
	if kitchen.HasTranscript {
		t.Error("kitchen.mov should NOT have a transcript")
	}
}

func TestTranscriptReturnsCues(t *testing.T) {
	app, _ := openFixture(t)
	cues, err := app.Transcript("ring.mp4")
	if err != nil {
		t.Fatalf("Transcript: %v", err)
	}
	if len(cues) != 3 {
		t.Fatalf("want 3 cues, got %d", len(cues))
	}
	if cues[0].Start != 1 || cues[0].End != 3 {
		t.Errorf("first cue timing wrong: %+v", cues[0])
	}
	if cues[0].Source == "" || cues[0].Timecode == "" {
		t.Errorf("cue missing source/timecode: %+v", cues[0])
	}

	// A video with no transcript degrades to an empty list, not an error.
	empty, err := app.Transcript("kitchen.mov")
	if err != nil {
		t.Fatalf("Transcript(no transcript) errored: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("want 0 cues for kitchen, got %d", len(empty))
	}
}

func TestSearchKeyword(t *testing.T) {
	app, _ := openFixture(t)
	hits := app.Search("money")
	if len(hits) == 0 {
		t.Fatal("expected a hit for 'money'")
	}
	if hits[0].Name != "ring.mp4" {
		t.Errorf("hit from wrong source: %+v", hits[0])
	}

	// multi-term still finds the cue containing both.
	hits = app.Search("reward Penguin")
	if len(hits) == 0 {
		t.Fatal("expected a hit for 'reward Penguin'")
	}

	// empty query → no results (not a crash).
	if got := app.Search(""); len(got) != 0 {
		t.Errorf("empty query should yield 0 results, got %d", len(got))
	}
}

func TestAddRemoveReorderClip(t *testing.T) {
	app, dir := openFixture(t)
	ring := filepath.Join(dir, "ring.mp4")
	kitchen := filepath.Join(dir, "kitchen.mov")

	tl, err := app.AddClip(ring, 1, 3, "money for the cat")
	if err != nil {
		t.Fatalf("AddClip: %v", err)
	}
	if len(tl.Clips) != 1 {
		t.Fatalf("want 1 clip, got %d", len(tl.Clips))
	}
	c0 := tl.Clips[0]
	if c0.ID != "c1" || c0.In != 1 || c0.Out != 3 || c0.DurSec != 2 {
		t.Errorf("clip fields wrong: %+v", c0)
	}
	// meta pulled from the sidecar.
	if c0.Date != "2026-06-14" || c0.Person != "Test Person" || c0.SourceFPS != 30 {
		t.Errorf("clip meta not populated from sidecar: %+v", c0)
	}

	// a second clip; verify timeline start offset accumulates.
	tl, _ = app.AddClip(kitchen, 0, 5, "kitchen")
	if len(tl.Clips) != 2 {
		t.Fatalf("want 2 clips, got %d", len(tl.Clips))
	}
	if tl.Clips[1].StartSec != 2 {
		t.Errorf("second clip should start at 2s on the timeline, got %v", tl.Clips[1].StartSec)
	}
	if tl.DurationSec != 7 {
		t.Errorf("total duration want 7, got %v", tl.DurationSec)
	}

	// reorder: move c2 to front.
	tl, err = app.Reorder("c2", 0)
	if err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	if tl.Clips[0].ID != "c2" || tl.Clips[1].ID != "c1" {
		t.Errorf("reorder failed: %v, %v", tl.Clips[0].ID, tl.Clips[1].ID)
	}

	// remove c1.
	tl, err = app.RemoveClip("c1")
	if err != nil {
		t.Fatalf("RemoveClip: %v", err)
	}
	if len(tl.Clips) != 1 || tl.Clips[0].ID != "c2" {
		t.Errorf("remove failed: %+v", tl.Clips)
	}

	// removing an unknown id is an error but does not corrupt the timeline.
	if _, err := app.RemoveClip("nope"); err == nil {
		t.Error("removing unknown id should error")
	}
	if got := app.Timeline(); len(got.Clips) != 1 {
		t.Errorf("timeline changed after a failed remove: %d clips", len(got.Clips))
	}
}

func TestAddClipRejectsOutOfFolderSource(t *testing.T) {
	app, _ := openFixture(t)
	outside := filepath.Join(t.TempDir(), "evil.mp4")
	mustWrite(t, outside, "x")
	if _, err := app.AddClip(outside, 0, 2, "x"); err == nil {
		t.Error("AddClip must reject a source outside the open folder")
	}
}

func TestAddClipSwapsReversedInOut(t *testing.T) {
	app, dir := openFixture(t)
	tl, err := app.AddClip(filepath.Join(dir, "ring.mp4"), 6, 2, "")
	if err != nil {
		t.Fatalf("AddClip: %v", err)
	}
	if tl.Clips[0].In != 2 || tl.Clips[0].Out != 6 {
		t.Errorf("reversed in/out not normalised: %+v", tl.Clips[0])
	}
}

func TestSetTrimAndLabel(t *testing.T) {
	app, dir := openFixture(t)
	app.AddClip(filepath.Join(dir, "ring.mp4"), 1, 3, "orig")
	tl, err := app.SetTrim("c1", 1.5, 4.5)
	if err != nil {
		t.Fatalf("SetTrim: %v", err)
	}
	if tl.Clips[0].In != 1.5 || tl.Clips[0].Out != 4.5 {
		t.Errorf("trim not applied: %+v", tl.Clips[0])
	}
	tl, _ = app.SetLabel("c1", "renamed")
	if tl.Clips[0].Label != "renamed" {
		t.Errorf("label not applied: %q", tl.Clips[0].Label)
	}
}

func TestSetOverlay(t *testing.T) {
	app, _ := openFixture(t)
	tl, err := app.SetOverlay("enabled", true, "")
	if err != nil || !tl.Overlay.Enabled {
		t.Fatalf("enable overlay: %v %+v", err, tl.Overlay)
	}
	tl, _ = app.SetOverlay("date", false, "")
	if tl.Overlay.ShowDate {
		t.Error("show_date should be off")
	}
	tl, _ = app.SetOverlay("position", false, "top")
	if tl.Overlay.Position != "top" {
		t.Errorf("position want top, got %q", tl.Overlay.Position)
	}
	if _, err := app.SetOverlay("bogus", true, ""); err == nil {
		t.Error("unknown overlay field should error")
	}
}

func TestSaveLoadReelRoundTrip(t *testing.T) {
	app, dir := openFixture(t)
	app.AddClip(filepath.Join(dir, "ring.mp4"), 1, 3, "money")
	app.SetOverlay("enabled", true, "")

	out := filepath.Join(app.workDir, "case.reel.json")
	saved, err := app.SaveReel(out)
	if err != nil {
		t.Fatalf("SaveReel: %v", err)
	}
	if _, err := os.Stat(saved); err != nil {
		t.Fatalf("reel file not written: %v", err)
	}

	// fresh app loads it back; clip + overlay survive; nextID continues.
	app2 := NewApp()
	app2.workDir = t.TempDir()
	tl, err := app2.LoadReel(saved)
	if err != nil {
		t.Fatalf("LoadReel: %v", err)
	}
	if len(tl.Clips) != 1 || tl.Clips[0].Label != "money" {
		t.Errorf("loaded clips wrong: %+v", tl.Clips)
	}
	if !tl.Overlay.Enabled {
		t.Error("loaded overlay enabled flag lost")
	}
	// next add must be c2 (nextID synced from the loaded c1).
	if app2.nextID != 1 {
		t.Errorf("nextID want 1 after load, got %d", app2.nextID)
	}
}

// TestPickFolderPickedIndexes verifies the native-picker wiring: when the dialog
// returns a real folder, PickFolder indexes it (the existing open flow) and
// reports Picked=true with the FolderView — fed through the pickFolderFn seam so
// no real dialog pops.
func TestPickFolderPickedIndexes(t *testing.T) {
	dir := fixtureFolder(t)
	app := NewApp()
	app.workDir = t.TempDir()

	orig := pickFolderFn
	defer func() { pickFolderFn = orig }()
	pickFolderFn = func() (string, error) { return dir, nil }

	res, err := app.PickFolder()
	if err != nil {
		t.Fatalf("PickFolder: %v", err)
	}
	if !res.Picked {
		t.Fatal("expected Picked=true when the dialog returns a folder")
	}
	if len(res.Folder.Videos) != 2 {
		t.Fatalf("picked folder not indexed: want 2 videos, got %d", len(res.Folder.Videos))
	}
	// The folder is now the open media scope (the index/open flow ran).
	if app.folder == "" {
		t.Error("PickFolder should set the open folder so media serving is scoped")
	}
}

// TestPickFolderCancelledIsNoOp verifies a cancelled dialog (empty path) is a
// no-op: Picked=false, no error, the open folder unchanged.
func TestPickFolderCancelledIsNoOp(t *testing.T) {
	app := NewApp()
	app.workDir = t.TempDir()

	orig := pickFolderFn
	defer func() { pickFolderFn = orig }()
	pickFolderFn = func() (string, error) { return "", nil }

	res, err := app.PickFolder()
	if err != nil {
		t.Fatalf("cancelled pick should not error: %v", err)
	}
	if res.Picked {
		t.Error("cancelled dialog should report Picked=false")
	}
	if app.folder != "" {
		t.Error("cancelled pick must not change the open folder")
	}
}

// TestPickFolderErrorSurfaces verifies a dialog/exec failure surfaces as an error
// (so the UI can fall back to a path prompt) rather than being swallowed.
func TestPickFolderErrorSurfaces(t *testing.T) {
	app := NewApp()
	app.workDir = t.TempDir()

	orig := pickFolderFn
	defer func() { pickFolderFn = orig }()
	pickFolderFn = func() (string, error) { return "", fmt.Errorf("no powershell") }

	if _, err := app.PickFolder(); err == nil {
		t.Error("a picker exec failure should surface as an error")
	}
}

// TestCallPickFolderVerb checks the bridge dispatches pick_folder to PickFolder
// and returns the {ok,data} envelope (default-deny table includes the new verb).
func TestCallPickFolderVerb(t *testing.T) {
	dir := fixtureFolder(t)
	app := NewApp()
	app.workDir = t.TempDir()

	orig := pickFolderFn
	defer func() { pickFolderFn = orig }()
	pickFolderFn = func() (string, error) { return dir, nil }

	r := callEnv(t, app, "pick_folder", `{}`)
	if !r.OK {
		t.Fatalf("pick_folder verb failed: %s", r.Error)
	}
	var res PickFolderResult
	remarshal(t, r.Data, &res)
	if !res.Picked || len(res.Folder.Videos) != 2 {
		t.Fatalf("pick_folder result wrong: %+v", res)
	}
}

func TestAddMarker(t *testing.T) {
	app, _ := openFixture(t)
	tl := app.AddMarker(12.5, "threat")
	if len(tl.Markers) != 1 || tl.Markers[0].At != 12.5 || tl.Markers[0].Label != "threat" {
		t.Errorf("marker not added: %+v", tl.Markers)
	}
	// markers stay sorted.
	tl = app.AddMarker(3.0, "earlier")
	if tl.Markers[0].At != 3.0 {
		t.Errorf("markers not sorted: %+v", tl.Markers)
	}
}
