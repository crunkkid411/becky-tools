package captions

// fetch_test covers the deterministic, NETWORK-FREE helpers in fetch.go (pick the
// best of several yt-dlp subtitle files; rank language flavours; move across the
// fake "volume"). The yt-dlp exec itself is not run here — that is exercised live
// in the manual verification step and through the FetchAutoSubs seam in
// captions_test.go. writeFile/fileExists/errFake are defined alongside in the
// package (captions_test.go / captions.go).

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSubLangRank(t *testing.T) {
	cases := []struct {
		name string
		want int
	}{
		{"VIDEOID0001.en.srt", 0},
		{"VIDEOID0001.en-US.srt", 1},
		{"VIDEOID0001.en-orig.srt", 1},
		{"VIDEOID0001.srt", 1}, // no language token
	}
	for _, c := range cases {
		if got := subLangRank(c.name); got != c.want {
			t.Fatalf("subLangRank(%q) = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestBestSRT_PrefersPlainEN(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"ID.en-US.srt", "ID.en.srt", "ID.en-orig.srt"} {
		writeFile(t, filepath.Join(dir, n), "x")
	}
	got := bestSRT(dir)
	if filepath.Base(got) != "ID.en.srt" {
		t.Fatalf("bestSRT = %q, want ID.en.srt", filepath.Base(got))
	}
}

func TestBestSRT_None(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ID.info.json"), "{}")
	if got := bestSRT(dir); got != "" {
		t.Fatalf("bestSRT with no .srt = %q, want empty", got)
	}
}

func TestBestSRT_Deterministic(t *testing.T) {
	// Same-rank, same-length names resolve lexically and stably.
	dir := t.TempDir()
	for _, n := range []string{"ID.en-zz.srt", "ID.en-aa.srt"} {
		writeFile(t, filepath.Join(dir, n), "x")
	}
	a := bestSRT(dir)
	b := bestSRT(dir)
	if a != b {
		t.Fatalf("bestSRT non-deterministic: %q != %q", a, b)
	}
	if filepath.Base(a) != "ID.en-aa.srt" {
		t.Fatalf("bestSRT = %q, want ID.en-aa.srt (lexical tie-break)", filepath.Base(a))
	}
}

func TestMoveFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.srt")
	dst := filepath.Join(dir, "out.en.srt")
	writeFile(t, src, "subtitle body")
	if err := moveFile(src, dst); err != nil {
		t.Fatalf("moveFile: %v", err)
	}
	if fileExists(src) {
		t.Fatalf("source should be gone after move")
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(b) != "subtitle body" {
		t.Fatalf("dst content = %q", string(b))
	}
}

func TestLastLine(t *testing.T) {
	got := lastLine("WARNING: foo\nERROR: video unavailable\n\n", nil)
	if got != "ERROR: video unavailable" {
		t.Fatalf("lastLine = %q", got)
	}
	if got := lastLine("   ", errFake("boom")); got != "boom" {
		t.Fatalf("lastLine fallback = %q, want boom", got)
	}
}
