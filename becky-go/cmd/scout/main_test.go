package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"becky-go/internal/scout"
)

// TestFileSourceFormats verifies that fileSource accepts both the bare JSON
// array shape (what a simple scraper emits) and the {id,title,url,videos:[...]}
// object shape (what yt-dlp --flat-playlist produces), and that fillPositions
// assigns 1-based positions to entries that don't carry one.
func TestFileSourceFormats(t *testing.T) {
	videos := []scout.Video{
		{ID: "aaa", Title: "first video"},
		{ID: "bbb", Title: "second video"},
	}

	// --- bare array ---
	arrayBytes, err := json.Marshal(videos)
	if err != nil {
		t.Fatal(err)
	}
	arrayFile := filepath.Join(t.TempDir(), "array.json")
	if err := os.WriteFile(arrayFile, arrayBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	plFromArray, err := fileSource{path: arrayFile}.Playlist("ignored")
	if err != nil {
		t.Fatalf("array format: %v", err)
	}
	if len(plFromArray.Videos) != 2 {
		t.Fatalf("array format: want 2 videos, got %d", len(plFromArray.Videos))
	}
	// fillPositions should have assigned 1-based positions
	if plFromArray.Videos[0].Position != 1 || plFromArray.Videos[1].Position != 2 {
		t.Errorf("array format: positions=%v,%v want 1,2",
			plFromArray.Videos[0].Position, plFromArray.Videos[1].Position)
	}
	// ref is used as URL when the bare array carries no URL
	if plFromArray.URL != arrayFile {
		t.Errorf("array format: URL=%q want %q", plFromArray.URL, arrayFile)
	}

	// --- object with id/title/url/videos ---
	pl := scout.Playlist{
		ID:     "PL-test",
		Title:  "my test playlist",
		URL:    "https://youtube.com/playlist?list=PL-test",
		Videos: videos,
	}
	objBytes, err := json.Marshal(pl)
	if err != nil {
		t.Fatal(err)
	}
	objFile := filepath.Join(t.TempDir(), "playlist.json")
	if err := os.WriteFile(objFile, objBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	plFromObj, err := fileSource{path: objFile}.Playlist("ignored")
	if err != nil {
		t.Fatalf("object format: %v", err)
	}
	if plFromObj.ID != "PL-test" {
		t.Errorf("object format: ID=%q want PL-test", plFromObj.ID)
	}
	if plFromObj.Title != "my test playlist" {
		t.Errorf("object format: Title=%q", plFromObj.Title)
	}
	if len(plFromObj.Videos) != 2 {
		t.Fatalf("object format: want 2 videos, got %d", len(plFromObj.Videos))
	}
	if plFromObj.Videos[0].Position != 1 || plFromObj.Videos[1].Position != 2 {
		t.Errorf("object format: positions=%v,%v want 1,2",
			plFromObj.Videos[0].Position, plFromObj.Videos[1].Position)
	}
}

// TestFileSourcePreservesExistingPositions confirms that fillPositions leaves
// already-numbered videos alone (only fills the zeros).
func TestFileSourcePreservesExistingPositions(t *testing.T) {
	videos := []scout.Video{
		{ID: "x", Title: "explicitly positioned", Position: 5},
		{ID: "y", Title: "no position"},
	}
	b, _ := json.Marshal(videos)
	f := filepath.Join(t.TempDir(), "pos.json")
	if err := os.WriteFile(f, b, 0o644); err != nil {
		t.Fatal(err)
	}
	pl, err := fileSource{path: f}.Playlist("")
	if err != nil {
		t.Fatal(err)
	}
	if pl.Videos[0].Position != 5 {
		t.Errorf("want position 5 preserved, got %d", pl.Videos[0].Position)
	}
	if pl.Videos[1].Position != 2 {
		t.Errorf("want position 2 filled, got %d", pl.Videos[1].Position)
	}
}

// TestFileSourceMissing checks that a missing file returns an error (degrade,
// not a crash) — the unwiredSource is the other degrade path tested by the
// package-level TestDegradeOnSourceError.
func TestFileSourceMissing(t *testing.T) {
	_, err := fileSource{path: "/no/such/file.json"}.Playlist("")
	if err == nil {
		t.Error("want error for missing file, got nil")
	}
}

// TestUnwiredSourceDegrades verifies the cloud-side stub returns a non-nil
// error with a useful message (the local agent is told how to wire yt-dlp).
func TestUnwiredSourceDegrades(t *testing.T) {
	_, err := unwiredSource{}.Playlist("PLtest")
	if err == nil {
		t.Fatal("unwiredSource should return an error, got nil")
	}
	msg := err.Error()
	if len(msg) < 20 {
		t.Errorf("error message too short: %q", msg)
	}
}
