package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
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

func TestWithTempDirDeletesOnFailure(t *testing.T) {
	root := filepath.Join(t.TempDir(), "TEMP")
	err := withTempDir(root, "GeYevz27gyc", func(dir string) error {
		_ = os.WriteFile(filepath.Join(dir, "x.json3"), []byte("x"), 0o644)
		return errors.New("download failed")
	})
	if err == nil {
		t.Fatal("expected the failure to come back")
	}
	if _, err := os.Stat(filepath.Join(root, "GeYevz27gyc")); !os.IsNotExist(err) {
		t.Fatal("temp folder survived a failed run")
	}
}

// Jordan's rule: one yt-dlp request per 90 seconds.
func TestYtdlpWaitsNinetySeconds(t *testing.T) {
	stamp := filepath.Join(t.TempDir(), "ytdlp-last-call.txt")
	if w := ytdlpWait(stamp); w != 0 {
		t.Fatalf("no previous call should mean no wait, got %v", w)
	}
	_ = os.WriteFile(stamp, nil, 0o644)
	if w := ytdlpWait(stamp); w < 89*time.Second || w > 90*time.Second {
		t.Fatalf("call just made: wait %v, want about 90s", w)
	}
	old := time.Now().Add(-91 * time.Second)
	_ = os.Chtimes(stamp, old, old)
	if w := ytdlpWait(stamp); w != 0 {
		t.Fatalf("call 91s ago: wait %v, want 0", w)
	}
}

func TestJSON3TextMakesTimedParagraphs(t *testing.T) {
	raw := `{"events":[{"tStartMs":0,"dDurationMs":9},
	 {"tStartMs":0,"segs":[{"utf8":"Right"},{"utf8":" now,"}]},
	 {"tStartMs":2590,"aAppend":1,"segs":[{"utf8":"\n"}]},
	 {"tStartMs":2600,"segs":[{"utf8":"raising"},{"utf8":" money."}]},
	 {"tStartMs":31000,"segs":[{"utf8":"Done."}]},
	 {"tStartMs":3725000,"segs":[{"utf8":"Late"}]}]}`
	got, err := json3Text([]byte(raw))
	want := "[00:00] Right now, raising money. Done.\n\n[1:02:05] Late"
	if err != nil || got != want {
		t.Fatalf("got %q, %v\nwant %q", got, err, want)
	}
}

func TestCaptionTrackPrefersCreatorThenOriginal(t *testing.T) {
	x := json.RawMessage(`[]`)
	cases := []struct {
		v    video
		want string
	}{
		{video{Subtitles: map[string]json.RawMessage{"en-US": x}, AutoCaptions: map[string]json.RawMessage{"en-orig": x}}, "en-US"},
		{video{AutoCaptions: map[string]json.RawMessage{"en": x, "en-orig": x, "fr": x}}, "en-orig"},
		{video{AutoCaptions: map[string]json.RawMessage{"en": x, "fr": x}}, "en"},
		{video{Subtitles: map[string]json.RawMessage{"live_chat": x}, AutoCaptions: map[string]json.RawMessage{"fr": x}}, ""},
	}
	for i, c := range cases {
		if got, _ := captionTrack(c.v); got != c.want {
			t.Errorf("case %d: got %q, want %q", i, got, c.want)
		}
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
