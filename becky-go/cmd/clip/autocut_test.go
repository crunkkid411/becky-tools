package main

// autocut_test.go covers the becky-cut wiring WITHOUT shelling the real
// auto-editor/VAD/ffmpeg pipeline: parseAutoCutKeepSegments is exercised
// against a synthetic sample of becky-cut's real --dry-run JSON shape (see
// cmd/cut/main.go's report + decisions()), and AutoCutSilence's degrade paths
// (unknown video, missing binary) are exercised directly. A fake-seam test
// (mirroring transcribe_test.go's withFakeTranscribe) proves the full
// resolve->run->parse wiring without touching a real becky-cut binary.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// sampleCutDryRunJSON is a synthetic instance of becky-cut's real --dry-run
// stdout shape: the full report map plus a "decisions" list of
// {status,start,end} chunks (cmd/cut/main.go's decisions()). Unrelated report
// fields (codec/fps/vad_applied/...) are included to prove the parser ignores
// them rather than requiring an exact schema match.
const sampleCutDryRunJSON = `{
	"input": "X:\\case\\clip.mp4",
	"output": "X:\\case\\clip_edited.mp4",
	"export": "mp4",
	"codec": "h264_nvenc",
	"fps": 30,
	"duration": 12.5,
	"keep_segments": 2,
	"total_chunks": 4,
	"removed_by_vad": 1,
	"vad_applied": true,
	"rendered": false,
	"decisions": [
		{"status": "cut", "start": 0, "end": 0.5},
		{"status": "keep", "start": 0.5, "end": 5.2},
		{"status": "cut", "start": 5.2, "end": 5.6},
		{"status": "keep", "start": 5.6, "end": 12.5}
	]
}`

// TestParseAutoCutKeepSegments asserts VALUES: only the "keep" decisions
// survive, converted to {in,out}, in their original order.
func TestParseAutoCutKeepSegments(t *testing.T) {
	segs, err := parseAutoCutKeepSegments([]byte(sampleCutDryRunJSON))
	if err != nil {
		t.Fatalf("parseAutoCutKeepSegments: %v", err)
	}
	want := []AutoCutSegment{{In: 0.5, Out: 5.2}, {In: 5.6, Out: 12.5}}
	if len(segs) != len(want) {
		t.Fatalf("want %d keep segments, got %d: %+v", len(want), len(segs), segs)
	}
	for i := range want {
		if segs[i] != want[i] {
			t.Errorf("segment %d = %+v, want %+v", i, segs[i], want[i])
		}
	}
}

// TestParseAutoCutKeepSegmentsAllCut: a report with no "keep" decisions yields
// an empty (never nil) slice, not an error.
func TestParseAutoCutKeepSegmentsAllCut(t *testing.T) {
	sample := `{"decisions":[{"status":"cut","start":0,"end":9.9}]}`
	segs, err := parseAutoCutKeepSegments([]byte(sample))
	if err != nil {
		t.Fatalf("parseAutoCutKeepSegments: %v", err)
	}
	if len(segs) != 0 {
		t.Fatalf("want 0 keep segments, got %+v", segs)
	}
}

// TestParseAutoCutKeepSegmentsBadJSON: unparseable stdout is an error (the
// caller then degrades to {segments:[],note}), never a panic.
func TestParseAutoCutKeepSegmentsBadJSON(t *testing.T) {
	if _, err := parseAutoCutKeepSegments([]byte("not json")); err == nil {
		t.Fatal("want an error for unparseable becky-cut output")
	}
}

// TestAutoCutSilenceUnknownVideoDegrades: a basename not in the open folder
// degrades to {segments:[],note:...}, never an error/panic.
func TestAutoCutSilenceUnknownVideoDegrades(t *testing.T) {
	app, _ := openFixture(t)
	got := app.AutoCutSilence("nope.mp4")
	if len(got.Segments) != 0 {
		t.Errorf("unknown video Segments = %+v, want empty", got.Segments)
	}
	if got.Note == "" {
		t.Error("unknown video should carry a plain-language note")
	}
}

// TestAutoCutSilenceMissingBinaryDegrades: with BECKY_CUT unset/unresolvable
// (and no becky-cut on PATH in the test env), AutoCutSilence degrades rather
// than erroring.
func TestAutoCutSilenceMissingBinaryDegrades(t *testing.T) {
	t.Setenv("BECKY_CUT", filepath.Join(t.TempDir(), "does-not-exist.exe"))
	app, _ := openFixture(t)
	got := app.AutoCutSilence("ring.mp4")
	if len(got.Segments) != 0 {
		t.Errorf("missing binary Segments = %+v, want empty", got.Segments)
	}
	if got.Note == "" {
		t.Error("missing binary should carry a plain-language note")
	}
}

// TestAutoCutSilenceRunsBeckyCutAndReturnsSegments proves the full
// resolve->run->parse wiring using the runAutoCut seam (mirrors
// transcribe_test.go's withFakeTranscribe): a fake becky-cut binary path plus
// a fake runAutoCut returning sampleCutDryRunJSON must produce the same two
// keep segments TestParseAutoCutKeepSegments asserts directly.
func TestAutoCutSilenceRunsBeckyCutAndReturnsSegments(t *testing.T) {
	origRun := runAutoCut
	t.Cleanup(func() { runAutoCut = origRun })
	calls := 0
	runAutoCut = func(_ context.Context, _bin, _video string) ([]byte, error) {
		calls++
		return []byte(sampleCutDryRunJSON), nil
	}

	fakeBin := filepath.Join(t.TempDir(), cutExeName())
	if err := os.WriteFile(fakeBin, []byte("not-a-real-binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BECKY_CUT", fakeBin)

	app, _ := openFixture(t)
	got := app.AutoCutSilence("ring.mp4")
	if calls != 1 {
		t.Fatalf("the becky-cut seam should run exactly once, ran %d", calls)
	}
	want := []AutoCutSegment{{In: 0.5, Out: 5.2}, {In: 5.6, Out: 12.5}}
	if len(got.Segments) != len(want) {
		t.Fatalf("want %d segments, got %+v", len(want), got.Segments)
	}
	for i := range want {
		if got.Segments[i] != want[i] {
			t.Errorf("segment %d = %+v, want %+v", i, got.Segments[i], want[i])
		}
	}
	if got.Note != "" {
		t.Errorf("a successful run should carry no note, got %q", got.Note)
	}
}
