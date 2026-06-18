package assistant

import (
	"os"
	"path/filepath"
	"testing"

	"becky-go/internal/footage"
)

// makeCandidates builds N synthetic candidates in one source for window/cap tests.
func makeCandidates(n int) []footage.Candidate {
	out := make([]footage.Candidate, n)
	for i := 0; i < n; i++ {
		out[i] = footage.Candidate{
			Source:    "C:/case/clip.mp4",
			Name:      "clip.mp4",
			Timestamp: float64(i),
			End:       float64(i) + 1,
			Text:      "cue",
			Score:     float64(n - i),
		}
	}
	return out
}

// TestRetrieveBoundsTopK confirms retrieval caps to TopK so the model never sees
// more than the bound regardless of how many cues match (the 500GB invariant's
// first gate).
func TestRetrieveBoundsTopK(t *testing.T) {
	f := &Funnel{TopK: 10, WindowCues: 5, WindowOverlap: 1}
	// 50 grep-style hits supplied as search hits; index empty (no transcripts).
	hits := makeCandidates(50)
	got := f.Retrieve(footage.FolderIndex{}, nil, hits)
	if len(got) != 10 {
		t.Fatalf("Retrieve capped to %d, want TopK=10", len(got))
	}
}

// TestWindowsBounded confirms windows never exceed WindowCues and overlap by
// exactly WindowOverlap — so each MAP model call is token-bounded.
func TestWindowsBounded(t *testing.T) {
	f := &Funnel{TopK: 100, WindowCues: 10, WindowOverlap: 2}
	cands := makeCandidates(25)
	wins := f.Windows(cands)
	if len(wins) == 0 {
		t.Fatal("expected windows")
	}
	for _, w := range wins {
		if len(w.Candidates) > 10 {
			t.Fatalf("window %d has %d cues, exceeds WindowCues=10", w.Index, len(w.Candidates))
		}
	}
	// Overlap check: window[1] should start 8 (=10-2) into the candidate list.
	if len(wins) >= 2 && wins[1].Candidates[0].Timestamp != 8 {
		t.Fatalf("window 1 starts at ts=%v, want 8 (step=size-overlap)", wins[1].Candidates[0].Timestamp)
	}
}

// TestReduceDedupSorts confirms reduce de-duplicates overlapping selections and
// sorts chronologically.
func TestReduceDedupSorts(t *testing.T) {
	a := footage.Candidate{Source: "s", Timestamp: 5, Text: "five"}
	b := footage.Candidate{Source: "s", Timestamp: 1, Text: "one"}
	dup := footage.Candidate{Source: "s", Timestamp: 5, Text: "five"}
	out := Reduce([][]footage.Candidate{{a, b}, {dup}})
	if len(out) != 2 {
		t.Fatalf("Reduce = %d, want 2 (dedup the ts=5 dup)", len(out))
	}
	if out[0].Timestamp != 1 || out[1].Timestamp != 5 {
		t.Fatalf("Reduce not sorted chronologically: %+v", out)
	}
}

// TestWriteAnchorsAndCommand verifies the find_quotes anchors file + the
// becky-quotes --select-from-json command are formed correctly (without the binary
// present — the brief's requirement that command-building is unit-testable).
func TestWriteAnchorsAndCommand(t *testing.T) {
	dir := t.TempDir()
	anchors := []QuoteAnchor{
		{Source: "C:/case/ring.mp4", In: "00:13:12,640", Out: "00:13:20,560", Text: "pay you for the cat"},
	}
	path, err := WriteAnchors(dir, anchors)
	if err != nil {
		t.Fatalf("WriteAnchors error = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("anchors file missing: %v", err)
	}
	if filepath.Base(path) != "quote_anchors.json" {
		t.Fatalf("anchors filename = %q", filepath.Base(path))
	}

	cmd := QuotesCommand("ring.srt", path)
	if cmd.Bin != "becky-quotes" {
		t.Fatalf("command bin = %q, want becky-quotes", cmd.Bin)
	}
	if !hasArgPair(cmd.Args, "--select-from-json", path) {
		t.Fatalf("command args missing --select-from-json %s: %v", path, cmd.Args)
	}
	if !hasArgPair(cmd.Args, "--srt", "ring.srt") {
		t.Fatalf("command args missing --srt: %v", cmd.Args)
	}
}

// TestSecondsToTimecodeRoundsRight pins the seconds→SRT-timecode formatter.
func TestSecondsToTimecodeRoundsRight(t *testing.T) {
	tests := []struct {
		sec  float64
		want string
	}{
		{0, "00:00:00,000"},
		{1.5, "00:00:01,500"},
		{792.64, "00:13:12,640"},
		{3661.001, "01:01:01,001"},
	}
	for _, tt := range tests {
		if got := secondsToTimecode(tt.sec); got != tt.want {
			t.Fatalf("secondsToTimecode(%v) = %q, want %q", tt.sec, got, tt.want)
		}
	}
}

// TestAnchorsFromCandidates confirms candidate seconds are formatted back to SRT
// timecodes verbatim for the anchors.
func TestAnchorsFromCandidates(t *testing.T) {
	cands := []footage.Candidate{{Source: "s.mp4", Timestamp: 1.5, End: 3.0, Text: "hi"}}
	got := AnchorsFromCandidates(cands)
	if len(got) != 1 || got[0].In != "00:00:01,500" || got[0].Out != "00:00:03,000" {
		t.Fatalf("AnchorsFromCandidates = %+v", got)
	}
}

func hasArgPair(args []string, flag, val string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == val {
			return true
		}
	}
	return false
}
