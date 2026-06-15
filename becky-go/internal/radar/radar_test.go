package radar

import (
	"errors"
	"testing"
	"time"

	"becky-go/internal/freshness"
)

// fakeSource is a synthetic HistorySource so tests need no real Chrome, DB, or
// network — it just returns canned visits (or an error to test degrade).
type fakeSource struct {
	visits []Visit
	err    error
}

func (f fakeSource) Visits(time.Time) ([]Visit, error) { return f.visits, f.err }

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// testDeps mirrors the shape of the real freshness manifest closely enough to
// exercise cross-referencing without depending on its exact contents.
func testDeps() []freshness.Dependency {
	return []freshness.Dependency{
		{
			ID: "paddleocr-vl", Name: "PaddleOCR VL", UsedBy: []string{"becky-ocr"},
			Pinned: "PP-OCRv5", Upstream: freshness.Upstream{Type: "hf-model", Ref: "PaddlePaddle/PaddleOCR-VL"},
		},
		{
			ID: "rapidocr", Name: "rapidocr", UsedBy: []string{"becky-ocr"},
			Pinned: "1.3.0", Upstream: freshness.Upstream{Type: "pypi", Ref: "rapidocr"},
		},
	}
}

func TestClassify_table(t *testing.T) {
	cases := []struct {
		name      string
		v         Visit
		wantClass string
		wantOK    bool
	}{
		{"hf model card", Visit{URL: "https://huggingface.co/PaddlePaddle/PaddleOCR-VL"}, "hf-model", true},
		{"github repo", Visit{URL: "https://github.com/openai/whisper"}, "github-repo", true},
		{"pypi package", Visit{URL: "https://pypi.org/project/rapidocr/"}, "pypi", true},
		{"www prefix stripped", Visit{URL: "https://www.huggingface.co/x/y"}, "hf-model", true},
		{"arxiv with keyword", Visit{URL: "https://arxiv.org/abs/2401.00001", Title: "A new OCR model"}, "model-keyword", true},
		{"arxiv without keyword", Visit{URL: "https://arxiv.org/abs/2401.00002", Title: "About cats"}, "", false},
		{"non-model host ignored", Visit{URL: "https://news.example.com/story"}, "", false},
		{"empty url ignored", Visit{URL: ""}, "", false},
		{"ollama keyword tag", Visit{URL: "https://ollama.com/library/llama3"}, "model-keyword", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, class, ok := Classify(c.v)
			if ok != c.wantOK || class != c.wantClass {
				t.Errorf("Classify(%q) = (%q,%v), want (%q,%v)", c.v.URL, class, ok, c.wantClass, c.wantOK)
			}
		})
	}
}

func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"https://huggingface.co/a/b":      "huggingface.co",
		"http://www.github.com/x":         "github.com",
		"https://pypi.org:443/project/y/": "pypi.org",
		"huggingface.co/no/scheme":        "huggingface.co",
		"https://user@arxiv.org/abs/1":    "arxiv.org",
		"":                                "",
	}
	for in, want := range cases {
		if got := hostOf(in); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuild_flagsTrackedDependency(t *testing.T) {
	src := fakeSource{visits: []Visit{
		{URL: "https://huggingface.co/PaddlePaddle/PaddleOCR-VL", Title: "PaddleOCR-VL", LastVisit: ts("2026-06-10T22:14:00Z"), VisitCount: 3},
		{URL: "https://github.com/foo/new-thing", Title: "new-thing", LastVisit: ts("2026-06-09T08:00:00Z"), VisitCount: 1},
	}}
	rep := Build(src, testDeps(), "chrome-local", 30, ts("2026-06-01T00:00:00Z"), []string{"Default"})

	if rep.Degraded {
		t.Fatalf("unexpected degrade: %s", rep.Note)
	}
	if len(rep.Flagged) != 1 {
		t.Fatalf("want 1 flagged, got %d (%+v)", len(rep.Flagged), rep.Flagged)
	}
	m := rep.Flagged[0].BeckyMatch
	if m == nil || m.DependencyID != "paddleocr-vl" {
		t.Fatalf("flagged item not matched to paddleocr-vl: %+v", rep.Flagged[0])
	}
	if m.BeckyPinned != "PP-OCRv5" {
		t.Errorf("want pinned PP-OCRv5, got %q", m.BeckyPinned)
	}
	if len(rep.Seen) != 1 || rep.Seen[0].BeckyMatch != nil {
		t.Errorf("want 1 unmatched seen item, got %+v", rep.Seen)
	}
}

func TestBuild_degradesOnSourceError(t *testing.T) {
	src := fakeSource{err: errors.New("db locked")}
	rep := Build(src, testDeps(), "chrome-local", 30, ts("2026-06-01T00:00:00Z"), nil)
	if !rep.Degraded {
		t.Fatal("expected degraded report on source error")
	}
	if rep.Note == "" {
		t.Error("expected a plain-language degrade note")
	}
	if len(rep.Flagged) != 0 || len(rep.Seen) != 0 {
		t.Error("degraded report should carry no items")
	}
}

func TestBuild_sortsAndDedups(t *testing.T) {
	src := fakeSource{visits: []Visit{
		{URL: "https://github.com/b/repo", LastVisit: ts("2026-06-02T00:00:00Z"), VisitCount: 1},
		{URL: "https://github.com/a/repo", LastVisit: ts("2026-06-05T00:00:00Z"), VisitCount: 1},
		// duplicate URL with an earlier visit: should merge, not double-count.
		{URL: "https://github.com/b/repo", LastVisit: ts("2026-06-01T00:00:00Z"), VisitCount: 2},
	}}
	rep := Build(src, testDeps(), "chrome-local", 30, ts("2026-06-01T00:00:00Z"), nil)

	if len(rep.Seen) != 2 {
		t.Fatalf("want 2 deduped items, got %d", len(rep.Seen))
	}
	// Most recent first: a/repo (06-05) before b/repo (06-02 after merge).
	if rep.Seen[0].URL != "https://github.com/a/repo" {
		t.Errorf("want a/repo first (most recent), got %q", rep.Seen[0].URL)
	}
	for _, it := range rep.Seen {
		if it.URL == "https://github.com/b/repo" {
			if !it.LastVisit.Equal(ts("2026-06-02T00:00:00Z")) {
				t.Errorf("merged b/repo should keep newest visit, got %s", it.LastVisit)
			}
			if it.VisitCount != 3 {
				t.Errorf("merged b/repo visit count = %d, want 3", it.VisitCount)
			}
		}
	}
}

func TestBuild_deterministicTieBreakByURL(t *testing.T) {
	same := ts("2026-06-10T00:00:00Z")
	src := fakeSource{visits: []Visit{
		{URL: "https://github.com/zzz/repo", LastVisit: same},
		{URL: "https://github.com/aaa/repo", LastVisit: same},
	}}
	rep := Build(src, testDeps(), "chrome-local", 30, ts("2026-06-01T00:00:00Z"), nil)
	if len(rep.Seen) != 2 {
		t.Fatalf("want 2 items, got %d", len(rep.Seen))
	}
	// Equal timestamps -> stable order by URL ascending.
	if rep.Seen[0].URL != "https://github.com/aaa/repo" {
		t.Errorf("tie-break should sort by URL asc, got %q first", rep.Seen[0].URL)
	}
}

func TestBuild_matchByTitleWhenURLLacksRef(t *testing.T) {
	// HF blog-style URL whose path doesn't carry the ref, but the title does.
	src := fakeSource{visits: []Visit{
		{URL: "https://huggingface.co/blog/announcement", Title: "Introducing rapidocr 2.0", LastVisit: ts("2026-06-10T00:00:00Z")},
	}}
	rep := Build(src, testDeps(), "chrome-local", 30, ts("2026-06-01T00:00:00Z"), nil)
	if len(rep.Flagged) != 1 || rep.Flagged[0].BeckyMatch == nil {
		t.Fatalf("expected title-based match to rapidocr, got %+v", rep.Flagged)
	}
	if rep.Flagged[0].BeckyMatch.DependencyID != "rapidocr" {
		t.Errorf("want rapidocr match, got %q", rep.Flagged[0].BeckyMatch.DependencyID)
	}
}

func TestChromeTime(t *testing.T) {
	// 13253932800000000 us after 1601-01-01 == 2021-01-01T00:00:00Z.
	got := chromeTime(13253932800000000)
	want := ts("2021-01-01T00:00:00Z")
	if !got.Equal(want) {
		t.Errorf("chromeTime = %s, want %s", got, want)
	}
	if !chromeTime(0).IsZero() {
		t.Error("chromeTime(0) should be the zero time (never visited)")
	}
	if !chromeTime(-5).IsZero() {
		t.Error("chromeTime(negative) should be the zero time")
	}
}

func TestChromeSource_noDBs(t *testing.T) {
	// No paths configured -> typed error, not a crash (degrade upstream in Build).
	_, err := ChromeSource{}.Visits(time.Time{})
	if err == nil {
		t.Fatal("expected an error when no Chrome DB paths are configured")
	}
}

func TestRefMatches_realManifestSmoke(t *testing.T) {
	// Smoke test against the actual embedded manifest: the very dependency that
	// becky-radar exists to catch must be findable from a viewed PaddleOCR URL.
	deps, err := freshness.LoadManifest()
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	v := Visit{URL: "https://huggingface.co/PaddlePaddle/PaddleOCR-VL", Title: "PaddleOCR-VL"}
	if matchDependency(v, deps) == nil {
		t.Error("a viewed PaddleOCR-VL page should match a tracked becky dependency")
	}
}
