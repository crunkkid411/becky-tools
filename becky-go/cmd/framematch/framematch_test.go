// framematch_test.go — unit tests for the pieces that decide what a detective
// sees: pair RANKING (closest-first, greedy 1:1, threshold + cap), the
// hash/Hamming math, and the honest-enhance LOG (every eq edit recorded, source
// untouched, neutral = no-op). The enhance test shells the real ffmpeg from
// config when present and otherwise skips, so the suite stays green offline.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"becky-go/internal/config"
)

// mkFrame is a tiny constructor for test frames.
func mkFrame(label string, idx int, hash string) Frame {
	return Frame{SourceLabel: label, Index: idx, Timestamp: float64(idx), TimeLabel: timeLabel(float64(idx)),
		Path: label + "/" + hash + ".jpg", Hash: hash}
}

func TestPairFramesRanksClosestFirst(t *testing.T) {
	// A0 is identical to B1 (ham 0); A1 is 1 bit from B0. Expect the ham-0 pair
	// ranked first, then the ham-1 pair, both within threshold.
	framesA := []Frame{
		mkFrame("A", 0, "00000000000000ff"),
		mkFrame("A", 1, "00000000000000fe"), // 1 bit from B0's "...ff"
	}
	framesB := []Frame{
		mkFrame("B", 0, "00000000000000ff"),
		mkFrame("B", 1, "00000000000000ff"),
	}
	pairs := pairFrames(framesA, framesB, 8, 0)
	if len(pairs) != 2 {
		t.Fatalf("expected 2 pairs, got %d", len(pairs))
	}
	if pairs[0].Rank != 1 || pairs[0].Hamming != 0 {
		t.Errorf("rank 1 should be the ham-0 pair, got rank=%d ham=%d", pairs[0].Rank, pairs[0].Hamming)
	}
	if pairs[1].Hamming < pairs[0].Hamming {
		t.Errorf("pairs not sorted closest-first: %d before %d", pairs[0].Hamming, pairs[1].Hamming)
	}
	// Similarity must track distance: ham 0 -> 1.0.
	if pairs[0].Similarity != 1.0 {
		t.Errorf("ham 0 should be similarity 1.0, got %v", pairs[0].Similarity)
	}
}

func TestPairFramesThresholdRejectsNonMatches(t *testing.T) {
	// A0 and B0 differ by many bits (well above threshold) — must NOT pair.
	framesA := []Frame{mkFrame("A", 0, "ffffffffffffffff")}
	framesB := []Frame{mkFrame("B", 0, "0000000000000000")} // ham 64
	pairs := pairFrames(framesA, framesB, 10, 0)
	if len(pairs) != 0 {
		t.Fatalf("expected 0 pairs above threshold, got %d", len(pairs))
	}
}

func TestPairFramesGreedyOneToOne(t *testing.T) {
	// Two A frames both closest to the SAME B0; greedy 1:1 must use B0 once, then
	// fall back to the next-best B for the second A.
	framesA := []Frame{
		mkFrame("A", 0, "00000000000000ff"), // ham 0 to B0
		mkFrame("A", 1, "00000000000000ff"), // also ham 0 to B0, but B0 is taken
	}
	framesB := []Frame{
		mkFrame("B", 0, "00000000000000ff"), // ham 0 to both A
		mkFrame("B", 1, "00000000000000fc"), // ham 2 to the A frames
	}
	pairs := pairFrames(framesA, framesB, 8, 0)
	if len(pairs) != 2 {
		t.Fatalf("expected 2 distinct 1:1 pairs, got %d", len(pairs))
	}
	usedB := map[int]int{}
	for _, p := range pairs {
		usedB[p.B.Index]++
	}
	for bi, n := range usedB {
		if n != 1 {
			t.Errorf("B frame %d reused %d times; pairing must be 1:1", bi, n)
		}
	}
}

func TestPairFramesMaxPairsCap(t *testing.T) {
	framesA := []Frame{mkFrame("A", 0, "0000000000000000"), mkFrame("A", 1, "0000000000000001"), mkFrame("A", 2, "0000000000000003")}
	framesB := []Frame{mkFrame("B", 0, "0000000000000000"), mkFrame("B", 1, "0000000000000001"), mkFrame("B", 2, "0000000000000003")}
	pairs := pairFrames(framesA, framesB, 64, 2)
	if len(pairs) != 2 {
		t.Fatalf("max-pairs=2 should cap at 2, got %d", len(pairs))
	}
}

func TestParseHashAndHamming(t *testing.T) {
	a, bad := parseHash("00000000000000ff")
	if bad {
		t.Fatal("valid hash flagged bad")
	}
	b, _ := parseHash("00000000000000f0")
	if got := hamming64(a, b); got != 4 {
		t.Errorf("hamming(ff,f0) want 4, got %d", got)
	}
	if _, bad := parseHash("xyz"); !bad {
		t.Error("malformed hash should be flagged bad")
	}
	if _, bad := parseHash("00000000000000fg"); !bad {
		t.Error("non-hex char should be flagged bad")
	}
}

func TestEnhanceOptsActiveAndFilter(t *testing.T) {
	neutral := enhanceOpts{brightness: 0, contrast: 1, gamma: 1, saturation: 1}
	if neutral.active() {
		t.Error("neutral opts must be inactive (no-op)")
	}
	o := enhanceOpts{brightness: 0.2, contrast: 1, gamma: 1, saturation: 1}
	if !o.active() {
		t.Error("a non-zero brightness must be active")
	}
	want := "eq=brightness=0.2:contrast=1:gamma=1:saturation=1"
	if got := o.eqFilter(); got != want {
		t.Errorf("eqFilter = %q, want %q", got, want)
	}
}

// TestApplyEnhanceLogsAndLeavesSourceUntouched runs the real ffmpeg eq on a
// generated PNG and asserts: the Enhance record captures the exact filter +
// output path, an enhanced COPY is produced, and the ORIGINAL frame is byte-for-
// byte unchanged. Skips cleanly if ffmpeg is unavailable.
func TestApplyEnhanceLogsAndLeavesSourceUntouched(t *testing.T) {
	cfg := config.Load()
	if _, err := exec.LookPath(cfg.FFmpeg); err != nil {
		if _, statErr := os.Stat(cfg.FFmpeg); statErr != nil {
			t.Skipf("ffmpeg not available (%s); skipping enhance integration test", cfg.FFmpeg)
		}
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "frame.png")
	// Generate a tiny test PNG with ffmpeg (a flat color frame is enough).
	gen := exec.Command(cfg.FFmpeg, "-y", "-f", "lavfi", "-i", "color=c=gray:s=64x64",
		"-frames:v", "1", "-loglevel", "error", src)
	if err := gen.Run(); err != nil {
		t.Skipf("could not generate test frame: %v", err)
	}
	before, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read source: %v", err)
	}

	o := enhanceOpts{brightness: 0.2, contrast: 1.1, gamma: 1.3, saturation: 1}
	rec, err := applyEnhance(cfg, "B", src, o, "reveal shadow detail")
	if err != nil {
		t.Fatalf("applyEnhance: %v", err)
	}
	// The log must record what was done.
	if rec.Frame != "B" {
		t.Errorf("Enhance.Frame = %q, want B", rec.Frame)
	}
	if rec.Filter != o.eqFilter() {
		t.Errorf("Enhance.Filter = %q, want %q", rec.Filter, o.eqFilter())
	}
	if rec.Brightness != 0.2 || rec.Gamma != 1.3 {
		t.Errorf("Enhance numeric fields not logged: %+v", rec)
	}
	if rec.Note == "" {
		t.Error("Enhance.Note must record the reason")
	}
	// The enhanced COPY must exist and be a different file from the source.
	out := filepath.FromSlash(rec.OutputPath)
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("enhanced copy not written: %v", err)
	}
	if out == src {
		t.Fatal("enhance must write a COPY, not overwrite the source")
	}
	// The ORIGINAL must be unchanged.
	after, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("re-read source: %v", err)
	}
	if string(before) != string(after) {
		t.Error("source frame was modified — enhance must only read the source")
	}
}

func TestWhatToLookForScalesWithDistance(t *testing.T) {
	veryClose := whatToLookFor(2)
	mid := whatToLookFor(8)
	far := whatToLookFor(13)
	if veryClose == mid || mid == far || veryClose == far {
		t.Error("what-to-look-for hint should differ by match closeness")
	}
	for _, h := range []string{veryClose, mid, far} {
		if h == "" {
			t.Error("hint must not be empty")
		}
	}
}

func TestTimeLabel(t *testing.T) {
	cases := map[float64]string{0: "0:00.0", 73.4: "1:13.4", 5: "0:05.0"}
	for sec, want := range cases {
		if got := timeLabel(sec); got != want {
			t.Errorf("timeLabel(%v) = %q, want %q", sec, got, want)
		}
	}
}
