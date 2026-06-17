package main

import (
	"os"
	"path/filepath"
	"testing"

	"becky-go/internal/scout"
)

// parseFlatPlaylist maps yt-dlp's --flat-playlist -J shape into scout videos,
// numbering positions and synthesizing youtu.be URLs from ids when absent.
func TestParseFlatPlaylist(t *testing.T) {
	js := []byte(`{"id":"PL1","title":"ai - useful -","webpage_url":"https://youtube.com/playlist?list=PL1",
		"entries":[{"id":"aaa","title":"first"},{"id":"bbb","title":"second","url":"https://youtu.be/bbb"}]}`)
	pl, err := parseFlatPlaylist(js)
	if err != nil {
		t.Fatal(err)
	}
	if pl.Title != "ai - useful -" || len(pl.Videos) != 2 {
		t.Fatalf("playlist parse wrong: %+v", pl)
	}
	if pl.Videos[0].URL != "https://youtu.be/aaa" || pl.Videos[0].Position != 1 {
		t.Errorf("entry 0 wrong: %+v", pl.Videos[0])
	}
	if pl.Videos[1].Position != 2 {
		t.Errorf("entry 1 position wrong: %+v", pl.Videos[1])
	}
}

// applyVideoJSON merges per-video deep fields, preferring channel over uploader
// and keeping existing data when a field is absent.
func TestApplyVideoJSON(t *testing.T) {
	v := scout.Video{ID: "x", Title: "flat title", URL: "https://youtu.be/x"}
	applyVideoJSON(&v, []byte(`{"uploader":"Some Channel","description":"about OCR","tags":["ocr","pdf"]}`))
	if v.Channel != "Some Channel" || v.Description != "about OCR" || len(v.Tags) != 2 {
		t.Fatalf("deep merge wrong: %+v", v)
	}
	if v.Title != "flat title" {
		t.Errorf("title should be preserved when deep title empty, got %q", v.Title)
	}
}

// stateFilterSource drops already-seen videos and records every fetched id.
func TestStateFilterSource(t *testing.T) {
	inner := scout.FakePlaylist{PL: scout.Playlist{ID: "PL", Videos: []scout.Video{
		{ID: "old", Title: "seen before"},
		{ID: "new", Title: "brand new"},
	}}}
	var fetched []string
	s := &stateFilterSource{inner: inner, seen: map[string]bool{"old": true}, fetched: &fetched}
	pl, err := s.Playlist("PL")
	if err != nil {
		t.Fatal(err)
	}
	if len(pl.Videos) != 1 || pl.Videos[0].ID != "new" {
		t.Fatalf("want only the new video, got %+v", pl.Videos)
	}
	if len(fetched) != 2 {
		t.Errorf("fetched should record all ids, got %v", fetched)
	}
}

// State round-trips through disk: saved ids load back as the seen set.
func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(path, map[string]bool{"a": true, "b": true}); err != nil {
		t.Fatal(err)
	}
	seen := loadState(path)
	if !seen["a"] || !seen["b"] || len(seen) != 2 {
		t.Fatalf("round-trip wrong: %v", seen)
	}
	// A missing file is an empty set, not an error.
	if got := loadState(filepath.Join(t.TempDir(), "nope.json")); len(got) != 0 {
		t.Errorf("missing state should be empty, got %v", got)
	}
}

// fileSource must accept both a bare video array and a {videos:[...]} object,
// and fill in missing positions in file order — the offline --from-json contract.
func TestFileSourceFormats(t *testing.T) {
	dir := t.TempDir()

	arrayPath := filepath.Join(dir, "array.json")
	if err := os.WriteFile(arrayPath, []byte(`[
		{"id":"a","url":"https://youtu.be/a","title":"first"},
		{"id":"b","url":"https://youtu.be/b","title":"second"}
	]`), 0o644); err != nil {
		t.Fatal(err)
	}
	pl, err := fileSource{path: arrayPath}.Playlist(arrayPath)
	if err != nil {
		t.Fatalf("array form: %v", err)
	}
	if len(pl.Videos) != 2 || pl.Videos[0].Position != 1 || pl.Videos[1].Position != 2 {
		t.Fatalf("array form positions wrong: %+v", pl.Videos)
	}

	objPath := filepath.Join(dir, "obj.json")
	if err := os.WriteFile(objPath, []byte(`{"id":"PL","title":"mine","videos":[
		{"id":"x","title":"only"}
	]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	pl, err = fileSource{path: objPath}.Playlist(objPath)
	if err != nil {
		t.Fatalf("object form: %v", err)
	}
	if pl.Title != "mine" || len(pl.Videos) != 1 || pl.Videos[0].Position != 1 {
		t.Fatalf("object form wrong: %+v", pl)
	}
}

// A missing file degrades to an error (which Build turns into a noted, non-crash
// report), not a panic.
func TestFileSourceMissing(t *testing.T) {
	if _, err := (fileSource{path: "/no/such/file.json"}).Playlist("x"); err == nil {
		t.Fatal("want error for missing file")
	}
}
