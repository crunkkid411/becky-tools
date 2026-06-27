package radar

import (
	"errors"
	"testing"
	"time"
)

// fakeSynced is a synthetic SyncedSource so tests need no Chrome/DB/network.
type fakeSynced struct {
	visits []Visit
	err    error
}

func (f fakeSynced) SyncedVisits(time.Time) ([]Visit, error) { return f.visits, f.err }

func TestIsArchivable(t *testing.T) {
	cases := map[string]bool{
		"https://www.reapertips.com/post/drum-racks-in-reaper": true,
		"https://github.com/VoloBuilds/toaster":                true,
		"https://huggingface.co/papers/2606.20781":             true,
		"http://example.com/article":                           true,
		"https://www.youtube.com/redirect?q=x&redir_token=abc": false, // redirect hop
		"https://www.youtube.com/watch?v=abc123":               true,  // a real watch page is content
		"https://www.google.com/search?q=hello":                false, // search page
		"https://www.google.com/":                              false, // bare root of nav host
		"https://accounts.google.com/signin":                   true,  // a real path that is not search/root
		"ftp://files.example.com/x":                            false, // non-http
		"javascript:void(0)":                                   false,
		"":                                                     false,
	}
	for in, want := range cases {
		if got := IsArchivable(in); got != want {
			t.Errorf("IsArchivable(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestBuildList_dedupesFiltersSorts(t *testing.T) {
	src := fakeSynced{visits: []Visit{
		{URL: "https://b.example.com/p", Title: "B", LastVisit: ts("2026-06-02T00:00:00Z"), VisitCount: 1},
		{URL: "https://a.example.com/p", Title: "A", LastVisit: ts("2026-06-05T00:00:00Z"), VisitCount: 1},
		{URL: "https://b.example.com/p", Title: "B", LastVisit: ts("2026-06-01T00:00:00Z"), VisitCount: 2}, // dup
		{URL: "https://www.google.com/search?q=x", Title: "junk", LastVisit: ts("2026-06-06T00:00:00Z")},   // junk
	}}
	rep := BuildList(src, "chrome-local", 30, ts("2026-06-01T00:00:00Z"), true, []string{"Default"})

	if rep.Degraded {
		t.Fatalf("unexpected degrade: %s", rep.Note)
	}
	if rep.Count != 2 {
		t.Fatalf("want 2 archivable URLs, got %d (%+v)", rep.Count, rep.URLs)
	}
	if rep.FilteredOut != 1 {
		t.Errorf("want 1 filtered-out (the search page), got %d", rep.FilteredOut)
	}
	// Most recent first; the merged b/p keeps its newest visit (06-02) and summed count.
	if rep.URLs[0].URL != "https://a.example.com/p" {
		t.Errorf("want a/p first (most recent), got %q", rep.URLs[0].URL)
	}
	if rep.URLs[1].URL == "https://b.example.com/p" {
		if !rep.URLs[1].LastVisit.Equal(ts("2026-06-02T00:00:00Z")) {
			t.Errorf("merged b/p should keep newest visit, got %s", rep.URLs[1].LastVisit)
		}
		if rep.URLs[1].VisitCount != 3 {
			t.Errorf("merged b/p visit count = %d, want 3", rep.URLs[1].VisitCount)
		}
	}
}

func TestBuildList_keepsJunkWhenCleanFalse(t *testing.T) {
	src := fakeSynced{visits: []Visit{
		{URL: "https://www.google.com/search?q=x", LastVisit: ts("2026-06-06T00:00:00Z")},
		{URL: "https://real.example.com/post", LastVisit: ts("2026-06-05T00:00:00Z")},
	}}
	rep := BuildList(src, "chrome-local", 30, ts("2026-06-01T00:00:00Z"), false, nil)
	if rep.Count != 2 {
		t.Fatalf("clean=false should keep all 2, got %d", rep.Count)
	}
	if rep.FilteredOut != 0 {
		t.Errorf("clean=false should filter nothing, got %d", rep.FilteredOut)
	}
}

func TestBuildList_degradesOnError(t *testing.T) {
	rep := BuildList(fakeSynced{err: errors.New("db locked")}, "chrome-local", 30, ts("2026-06-01T00:00:00Z"), true, nil)
	if !rep.Degraded || rep.Note == "" {
		t.Fatal("expected a degraded report with a plain-language note")
	}
	if rep.Count != 0 || len(rep.URLs) != 0 {
		t.Error("degraded feed should carry no URLs")
	}
}
