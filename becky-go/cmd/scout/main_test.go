package main

import (
	"os"
	"path/filepath"
	"testing"
)

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
