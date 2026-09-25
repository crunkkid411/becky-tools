package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMeasureFindsUniqueRepos(t *testing.T) {
	v := video{Duration: 911, Description: "00:10 - openmuse https://github.com/CopilotKit/openmuse\n" +
		"again https://github.com/copilotkit/openmuse.\nsponsor https://github.com/sponsors/someone\n" +
		"site https://githubawesome.com/x\nlaya https://github.com/receptron/laya.git",
		Chapters: []chapter{{}, {}}}
	f := measure(v)
	if want := []string{"CopilotKit/openmuse", "receptron/laya"}; !reflect.DeepEqual(f.Repos, want) {
		t.Fatalf("repos = %v, want %v", f.Repos, want)
	}
	if f.Links != 5 || f.Chapters != 2 {
		t.Fatalf("links=%d chapters=%d, want 5 and 2", f.Links, f.Chapters)
	}
}

// The cleanup must be impossible to aim anywhere but TEMP\<video id>.
func TestTempDirForRefusesAnythingElse(t *testing.T) {
	root := filepath.Join(t.TempDir(), "TEMP")
	if _, err := tempDirFor(filepath.Dir(root), "GeYevz27gyc"); err == nil {
		t.Fatal("accepted a root not named TEMP")
	}
	for _, bad := range []string{"..", "../../x", "", "GeYevz27gyc/..", "short"} {
		if _, err := tempDirFor(root, bad); err == nil {
			t.Fatalf("accepted id %q", bad)
		}
	}
	if got, err := tempDirFor(root, "GeYevz27gyc"); err != nil || got != filepath.Join(root, "GeYevz27gyc") {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestWithTempVideoDeletesOnFailure(t *testing.T) {
	root := filepath.Join(t.TempDir(), "TEMP")
	t.Setenv("BECKY_YTDLP", "definitely-not-a-real-binary")
	if err := withTempVideo(root, "GeYevz27gyc", func(string) error { return nil }); err == nil {
		t.Fatal("expected the fake yt-dlp to fail")
	}
	if _, err := os.Stat(filepath.Join(root, "GeYevz27gyc")); !os.IsNotExist(err) {
		t.Fatal("temp folder survived a failed run")
	}
}

func TestNearestPicksClosestPain(t *testing.T) {
	pains := []pain{{ID: "a", Pain: "alpha"}, {ID: "b", Pain: "beta"}}
	pv := [][]float64{{1, 0}, {0, 1}}
	got := nearest(pains, pv, [][]float64{{0.2, 0.9}, {0.8, 0.1}})
	if got[0].PainID != "b" || got[1].PainID != "a" || got[0].Sim != 0.9 {
		t.Fatalf("got %+v", got)
	}
}

func TestSweepTempKeepsNonVideoFolders(t *testing.T) {
	root := filepath.Join(t.TempDir(), "TEMP")
	_ = os.MkdirAll(filepath.Join(root, "GeYevz27gyc"), 0o755)
	_ = os.MkdirAll(filepath.Join(root, "keep-me"), 0o755)
	sweepTemp(root)
	if _, err := os.Stat(filepath.Join(root, "GeYevz27gyc")); !os.IsNotExist(err) {
		t.Fatal("leftover video folder not swept")
	}
	if _, err := os.Stat(filepath.Join(root, "keep-me")); err != nil {
		t.Fatal("sweep removed a folder that is not a video id")
	}
}

// A wrong "links" loses a video's content, so it needs both signals.
func TestGuardRouteNeedsReposAndConfidence(t *testing.T) {
	many := facts{Repos: []string{"a/a", "b/b", "c/c"}}
	cases := []struct {
		name  string
		p     float64
		f     facts
		route string
	}{
		{"confident and 3 repos", 0.75, many, routeLinks},
		{"confident but 1 repo", 0.9, facts{Repos: []string{"a/a"}}, routeSpeech},
		{"3 repos but unsure", 0.65, many, routeSpeech},
	}
	for _, c := range cases {
		d := guardRoute(routeLinks, map[string]float64{routeLinks: c.p, routeSpeech: 1 - c.p}, 0.5, c.f)
		if d.Route != c.route {
			t.Errorf("%s: route %s, want %s", c.name, d.Route, c.route)
		}
	}
	if d := guardRoute(routeSpeech, nil, 0, many); d.Route != routeSpeech || d.Note != "" {
		t.Errorf("speech must pass through untouched, got %+v", d)
	}
}
