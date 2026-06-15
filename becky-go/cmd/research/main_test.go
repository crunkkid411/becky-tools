package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRun_badInvocationExits2(t *testing.T) {
	// No question and no URLs → exit 2 (bad invocation), never a panic.
	if code := run([]string{"--out", t.TempDir()}); code != exitBadArgs {
		t.Errorf("empty invocation exit=%d want %d", code, exitBadArgs)
	}
}

func TestRun_topicProducesReportExit0(t *testing.T) {
	dir := t.TempDir()
	// Model-free fallback: no search/fetch/model wired, so this is a degraded but
	// valid run. It must still exit 0 and write report.md.
	code := run([]string{"--out", dir, "--format", "both", "best local diarization model"})
	if code != exitOK {
		t.Fatalf("topic run exit=%d want %d", code, exitOK)
	}
	if _, err := os.Stat(filepath.Join(dir, "report.md")); err != nil {
		t.Errorf("report.md not written: %v", err)
	}
}

func TestRun_readingListFromFile(t *testing.T) {
	dir := t.TempDir()
	urlsFile := filepath.Join(dir, "urls.txt")
	content := "# a reading list\nhttps://e.com/p1\n\nhttps://e.com/p2\n"
	if err := os.WriteFile(urlsFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	code := run([]string{"--urls", urlsFile, "--out", filepath.Join(dir, "run")})
	if code != exitOK {
		t.Errorf("reading-list run exit=%d want %d", code, exitOK)
	}
}

func TestSlug(t *testing.T) {
	cases := []struct{ q, want string }{
		{"Best Local Model 2026", "best-local-model-2026"},
		{"", "run"},
		{"!!!", "run"},
	}
	for _, c := range cases {
		if got := slug(c.q, nil); got != c.want {
			t.Errorf("slug(%q)=%q want %q", c.q, got, c.want)
		}
	}
	// Falls back to a path basename for a URL-only run (pathx handles Windows paths).
	if got := slug("", []string{`C:\downloads\reading.txt`}); got != "readingtxt" {
		t.Errorf("slug from windows path = %q", got)
	}
}

func TestParseFlags_flagsAfterQuestion(t *testing.T) {
	// A non-dev may put --out AFTER the question; it must NOT be swallowed.
	o, _, done := parseFlags([]string{"the question here", "--out", "myrun", "--offline"})
	if done {
		t.Fatal("valid flags should not signal done")
	}
	if o.question != "the question here" {
		t.Errorf("question=%q — trailing flags leaked into it", o.question)
	}
	if o.out != "myrun" {
		t.Errorf("--out after the question was lost: %q", o.out)
	}
	if !o.offline {
		t.Error("--offline after the question was lost")
	}
}

func TestParseFlags_selfUpgradeOff(t *testing.T) {
	o, _, done := parseFlags([]string{"--self-upgrade", "off", "q"})
	if done {
		t.Fatal("valid flags should not signal done")
	}
	if o.selfUpgrade {
		t.Error("--self-upgrade off should disable the watch")
	}
	if o.question != "q" {
		t.Errorf("question=%q want q", o.question)
	}
}
