package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"becky-go/internal/pathx"
)

func TestDeriveOutPath(t *testing.T) {
	tests := []struct {
		name            string
		out, video, srt string
		wantBase        string
	}{
		{"video stem", "", `C:\v\foo.mp4`, `C:\v\foo.en.srt`, "foo_QUOTES.srt"},
		{"explicit out wins", `C:\custom\x.srt`, `C:\v\foo.mp4`, `C:\v\foo.en.srt`, "x.srt"},
		{"srt en stripped", "", "", `C:\v\stream.en.srt`, "stream_QUOTES.srt"},
		{"srt bare", "", "", `C:\v\stream.srt`, "stream_QUOTES.srt"},
		{"srt lang variant", "", "", `C:\v\talk.en-US.srt`, "talk_QUOTES.srt"},
		{"video with dots+date", "", `C:\v\2026-06-16 18-24-32.mp4`, "", "2026-06-16 18-24-32_QUOTES.srt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// pathx.Base is separator-agnostic: deriveOutPath emits Windows
			// paths (C:\...), and filepath.Base can't split "\" on Linux CI
			// (CLAUDE.md §2 invariant).
			got := pathx.Base(deriveOutPath(tt.out, tt.video, tt.srt))
			if got != tt.wantBase {
				t.Errorf("deriveOutPath = %q, want base %q", got, tt.wantBase)
			}
		})
	}
}

func TestStripSrtStem_DoesNotEatRealNameParts(t *testing.T) {
	// a non-language final segment must be preserved.
	if got := stripSrtStem("interview.part2.srt"); got != "interview.part2" {
		t.Errorf("stripSrtStem ate a real name part: got %q", got)
	}
	// ".en" IS a language tag and should go.
	if got := stripSrtStem("interview.en.srt"); got != "interview" {
		t.Errorf("stripSrtStem should strip .en: got %q", got)
	}
}

func TestLooksLikeLangTag(t *testing.T) {
	yes := []string{"en", "en-US", "en-orig", "spa", "fr"}
	no := []string{"part2", "2025", "v1", "", "toolongtag"}
	for _, s := range yes {
		if !looksLikeLangTag(s) {
			t.Errorf("looksLikeLangTag(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if looksLikeLangTag(s) {
			t.Errorf("looksLikeLangTag(%q) = true, want false", s)
		}
	}
}

const cliSRT = `1
00:00:01,000 --> 00:00:03,500
Good evening and welcome to the stream tonight.

2
00:00:03,600 --> 00:00:06,200
She filed two restraining orders against me last year.

3
00:00:06,300 --> 00:00:09,900
So I said I would press charges if she kept it up.
`

// writeTempSRT writes cliSRT into a temp dir and returns its path.
func writeTempSRT(t *testing.T) (dir, srtPath string) {
	t.Helper()
	dir = t.TempDir()
	srtPath = filepath.Join(dir, "clip.en.srt")
	if err := os.WriteFile(srtPath, []byte(cliSRT), 0o644); err != nil {
		t.Fatalf("write srt: %v", err)
	}
	return dir, srtPath
}

func TestRealMain_MissingSRT(t *testing.T) {
	if code := realMain(cliFlags{}); code != 2 {
		t.Errorf("missing --srt should exit 2, got %d", code)
	}
}

func TestRealMain_Exact_WritesQuotesSRT(t *testing.T) {
	dir, srt := writeTempSRT(t)
	video := filepath.Join(dir, "clip.mp4")
	// Two NON-adjacent matches (cue 1 and cue 3; the unmatched cue 2 sits between
	// them), so they stay as two SEPARATE regions and we can assert each region's
	// verbatim cue boundaries (SPEC hard rule #4). Consecutive-cue merging is
	// covered separately by TestRealMain_Exact_AdjacentCuesMerge.
	code := realMain(cliFlags{
		srt:          srt,
		video:        video,
		exact:        "welcome to the stream|press charges",
		maxRegionSec: 90,
	})
	if code != 0 {
		t.Fatalf("exact run exit = %d, want 0", code)
	}
	out := filepath.Join(dir, "clip_QUOTES.srt")
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", out, err)
	}
	body := string(data)
	if !strings.Contains(body, "press charges") {
		t.Errorf("output srt missing the matched quote:\n%s", body)
	}
	// Both regions' boundaries must be copied verbatim from real source cues.
	for _, ts := range []string{
		"00:00:01,000 --> 00:00:03,500", // cue 1 (welcome to the stream)
		"00:00:06,300 --> 00:00:09,900", // cue 3 (press charges)
	} {
		if !strings.Contains(body, ts) {
			t.Errorf("output srt missing verbatim cue timestamp %q:\n%s", ts, body)
		}
	}
	// no comment lines.
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			t.Errorf("comment line in srt: %q", line)
		}
	}
}

// TestRealMain_Exact_AdjacentCuesMerge locks the intended behavior: two matches in
// CONSECUTIVE cues (cue 2 then cue 3) collapse into ONE region whose start/end are
// still verbatim real cue boundaries (cue2.start .. cue3.end) — continuous speech
// is one clip, not two fragments.
func TestRealMain_Exact_AdjacentCuesMerge(t *testing.T) {
	dir, srt := writeTempSRT(t)
	code := realMain(cliFlags{
		srt:          srt,
		video:        filepath.Join(dir, "clip.mp4"),
		exact:        "restraining orders|press charges",
		maxRegionSec: 90,
	})
	if code != 0 {
		t.Fatalf("exact run exit = %d, want 0", code)
	}
	body, err := os.ReadFile(filepath.Join(dir, "clip_QUOTES.srt"))
	if err != nil {
		t.Fatalf("expected output srt: %v", err)
	}
	// merged region spans cue 2 start .. cue 3 end — both verbatim boundaries.
	if !strings.Contains(string(body), "00:00:03,600 --> 00:00:09,900") {
		t.Errorf("adjacent cues should merge into one verbatim-bounded region:\n%s", string(body))
	}
}

func TestRealMain_SourceSRTUnchanged(t *testing.T) {
	_, srt := writeTempSRT(t)
	before, _ := os.ReadFile(srt)
	realMain(cliFlags{srt: srt, exact: "press charges"})
	after, _ := os.ReadFile(srt)
	if string(before) != string(after) {
		t.Error("source .srt was modified by the run")
	}
}

func TestRealMain_SelectFromJSON(t *testing.T) {
	dir, srt := writeTempSRT(t)
	anchors := filepath.Join(dir, "anchors.json")
	if err := os.WriteFile(anchors, []byte(`{"anchors":[{"quote":"press charges if she kept it up","because":"threat"}]}`), 0o644); err != nil {
		t.Fatalf("write anchors: %v", err)
	}
	code := realMain(cliFlags{
		srt:            srt,
		video:          filepath.Join(dir, "clip.mp4"),
		selectFromJSON: anchors,
		maxRegionSec:   90,
	})
	if code != 0 {
		t.Fatalf("select-from-json run exit = %d, want 0", code)
	}
	out := filepath.Join(dir, "clip_QUOTES.srt")
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("expected %s: %v", out, err)
	}
	if !strings.Contains(string(data), "press charges") {
		t.Errorf("select-from-json output missing the anchor quote:\n%s", string(data))
	}
}

func TestRealMain_LLMUnavailable_Degrades(t *testing.T) {
	// default mode (no --exact / --select-from-json) with a guaranteed-missing
	// model must degrade with a nonzero exit, NOT crash.
	_, srt := writeTempSRT(t)
	t.Setenv("BECKY_QUOTES_MODEL", `X:\definitely\not\here.gguf`)
	t.Setenv("BECKY_QUOTES_SERVER_URL", "")
	code := realMain(cliFlags{srt: srt, criteria: "find threats"})
	if code == 0 {
		t.Error("LLM mode with no model should NOT exit 0 (must degrade with a note)")
	}
}

func TestRealMain_LogSidecar(t *testing.T) {
	dir, srt := writeTempSRT(t)
	logPath := filepath.Join(dir, "rationale.json")
	code := realMain(cliFlags{
		srt:     srt,
		video:   filepath.Join(dir, "clip.mp4"),
		exact:   "press charges",
		logPath: logPath,
	})
	if code != 0 {
		t.Fatalf("run exit = %d", code)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("expected --log sidecar: %v", err)
	}
	if !strings.Contains(string(data), `"tool": "becky-quotes"`) {
		t.Errorf("log sidecar missing tool field:\n%s", string(data))
	}
}
