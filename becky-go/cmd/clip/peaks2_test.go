package main

// peaks2_test.go covers the accurate-waveform verb's pure parts: the shared
// .bpk cache format (roundtrip must stay byte-compatible with the C++
// timeline), the fnv1a cache-key hash, coverage-run math, and — the whole
// point of the verb — that column levels are ABSOLUTE, never normalized.
// The ffmpeg decode itself is exercised by the real-media smoke check
// (becky-clip call peaks2), same policy as peaks_test.go.

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

// TestFnv1a64KnownVectors pins the hash to the values the C++ timeline
// produces (native/becky-timeline/main.cpp fnv1a64). NOTE: the C++ offset
// basis 1469598103934665603 is NOT the textbook FNV-1a basis (it is a digit
// short) — irrelevant for a cache key, but matching the C++ byte-for-byte IS
// the shared-cache contract, so this test pins the C++ constants, not FNV.
func TestFnv1a64KnownVectors(t *testing.T) {
	if got := fnv1a64(""); got != 1469598103934665603 {
		t.Fatalf("fnv1a64(\"\") = %d", got)
	}
	if got := fnv1a64("a"); got != 0x44bd8ad473cd9906 {
		t.Fatalf("fnv1a64(\"a\") = %#x", got)
	}
}

// TestBpkRoundTrip proves save→load preserves duration, coverage, and bins —
// the contract that lets the engine and the native timeline share one file.
func TestBpkRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.bpk")
	p := newPeakFile(path, 3.0)
	p.n0[10], p.x0[10] = -32, 31
	p.n0[1500], p.x0[1500] = -128, 127
	p.markSeconds(0, 2)
	p.save()

	q := loadBPK(path)
	if q == nil {
		t.Fatal("loadBPK returned nil for a file we just saved")
	}
	if q.duration != 3.0 || len(q.n0) != len(p.n0) {
		t.Fatalf("duration/bins mismatch: %v/%d", q.duration, len(q.n0))
	}
	if q.secFilled[0] != 1 || q.secFilled[1] != 1 || q.secFilled[2] != 0 {
		t.Fatalf("coverage map lost: %v", q.secFilled[:3])
	}
	if q.n0[10] != -32 || q.x0[10] != 31 || q.n0[1500] != -128 || q.x0[1500] != 127 {
		t.Fatal("bin values lost in roundtrip")
	}
	if q.n0[11] != 127 || q.x0[11] != -128 {
		t.Fatal("empty sentinel lost in roundtrip")
	}
}

// TestLoadBPKRejectsGarbage: torn/foreign files must read as "no cache",
// never a crash or wrong data (a concurrent becky-timeline save can tear).
func TestLoadBPKRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.bpk")
	os.WriteFile(bad, []byte("BPK2 not really a valid file"), 0o644)
	if loadBPK(bad) != nil {
		t.Fatal("garbage file parsed as a cache")
	}
	if loadBPK(filepath.Join(dir, "absent.bpk")) != nil {
		t.Fatal("missing file parsed as a cache")
	}
}

// TestColumnsAbsoluteScale is the honesty test: a quarter-scale signal must
// read ~0.25, NOT 1.0 (the old `peaks` verb normalizes any window's loudest
// moment to 1.0 — exactly what peaks2 exists to fix).
func TestColumnsAbsoluteScale(t *testing.T) {
	p := newPeakFile("", 2.0)
	samples := make([]int16, 2*bpkRate)
	for i := range samples {
		if i%2 == 0 {
			samples[i] = 8192 // 0.25 of int16 full scale
		} else {
			samples[i] = -8192
		}
	}
	p.fill(samples, 0)
	mins, maxs := p.columns(0, 2, 20)
	for i := range maxs {
		if math.Abs(maxs[i]-0.25) > 0.03 || math.Abs(mins[i]+0.25) > 0.03 {
			t.Fatalf("col %d: want ~±0.25 absolute, got [%f,%f] (normalized?)", i, mins[i], maxs[i])
		}
	}
}

// TestColumnsTransientNeverDropped: a single full-scale sample inside a wide
// column must surface in that column's max (true min/max, not a resample).
func TestColumnsTransientNeverDropped(t *testing.T) {
	p := newPeakFile("", 2.0)
	samples := make([]int16, 2*bpkRate)
	samples[bpkRate] = 32767 // one spike at t=1.0s
	p.fill(samples, 0)
	mins, maxs := p.columns(0, 2, 4)
	if maxs[2] < 0.9 {
		t.Fatalf("spike dropped: col 2 max = %f", maxs[2])
	}
	if maxs[0] != 0 || mins[0] != 0 {
		t.Fatalf("silent column not ~0: [%f,%f]", mins[0], maxs[0])
	}
}

// TestColumnsUndecodedIsZero: never-decoded bins draw as silence, not junk.
func TestColumnsUndecodedIsZero(t *testing.T) {
	p := newPeakFile("", 5.0)
	mins, maxs := p.columns(0, 5, 10)
	for i := range mins {
		if mins[i] != 0 || maxs[i] != 0 {
			t.Fatalf("undecoded col %d not 0,0: [%f,%f]", i, mins[i], maxs[i])
		}
	}
}

// TestUncoveredRuns verifies the decode planner: only never-decoded whole
// seconds are re-decoded (the windowed-decode guarantee).
func TestUncoveredRuns(t *testing.T) {
	p := newPeakFile("", 10.0)
	p.markSeconds(2, 4)
	p.markSeconds(7, 8)
	runs := p.uncoveredRuns(0, 10)
	want := [][2]int{{0, 2}, {4, 7}, {8, 10}}
	if len(runs) != len(want) {
		t.Fatalf("runs = %v", runs)
	}
	for i := range want {
		if runs[i] != want[i] {
			t.Fatalf("run %d = %v, want %v", i, runs[i], want[i])
		}
	}
	if got := p.uncoveredRuns(2, 4); len(got) != 0 {
		t.Fatalf("covered span reported uncovered: %v", got)
	}
}

// TestPeaks2UnknownSourceDegrades: bridge-level contract — unresolved source
// is an empty reply, never an error (blank lane, app keeps working).
func TestPeaks2UnknownSourceDegrades(t *testing.T) {
	app, _ := openFixture(t)
	r := callEnv(t, app, "peaks2", `{"source":"nope.mp4","in":0,"out":5,"columns":50}`)
	if !r.OK {
		t.Fatalf("peaks2 should degrade, not error: %s", r.Error)
	}
	var pr Peaks2Result
	remarshal(t, r.Data, &pr)
	if pr.Count != 0 || len(pr.Min) != 0 || len(pr.Max) != 0 {
		t.Fatalf("want empty degrade, got %+v", pr)
	}
}

// TestResolvePeak2Cols pins the column-count contract.
func TestResolvePeak2Cols(t *testing.T) {
	if resolvePeak2Cols(0) != defaultPeak2Cols || resolvePeak2Cols(-5) != defaultPeak2Cols {
		t.Fatal("default not applied")
	}
	if resolvePeak2Cols(99999) != maxPeak2Cols {
		t.Fatal("cap not applied")
	}
	if resolvePeak2Cols(640) != 640 {
		t.Fatal("valid count mangled")
	}
}
