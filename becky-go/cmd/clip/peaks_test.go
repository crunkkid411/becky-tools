package main

// peaks_test.go covers the waveform reduction: bucketPeaks (pure — no ffmpeg)
// and the App.Peaks bridge contract's degrade path. The ffmpeg decode itself
// (decodePCMWindow) is exercised only indirectly (via the unknown-source
// degrade, which never shells out), same as how TestProbeVerbDegrades covers
// Probe without requiring ffprobe to be installed.

import "testing"

// TestBucketPeaks asserts VALUES: a loud bucket normalizes to exactly 1.0
// (against the window's own global max), a silent bucket is exactly 0.0, and
// the bucket count matches what was requested.
func TestBucketPeaks(t *testing.T) {
	samples := []int16{
		32000, 32000, 32000, 32000, // bucket 0: loud (positive)
		0, 0, 0, 0, // bucket 1: silent
		-32000, -32000, -32000, -32000, // bucket 2: loud (negative -> abs)
		0, 0, 0, 0, // bucket 3: silent
	}
	peaks := bucketPeaks(samples, 4)
	if len(peaks) != 4 {
		t.Fatalf("want 4 buckets, got %d", len(peaks))
	}
	if peaks[0] != 1.0 {
		t.Errorf("loud bucket 0 = %v, want 1.0", peaks[0])
	}
	if peaks[1] != 0.0 {
		t.Errorf("silent bucket 1 = %v, want 0.0", peaks[1])
	}
	if peaks[2] != 1.0 {
		t.Errorf("loud bucket 2 (negative samples) = %v, want 1.0", peaks[2])
	}
	if peaks[3] != 0.0 {
		t.Errorf("silent bucket 3 = %v, want 0.0", peaks[3])
	}
}

// TestBucketPeaksQuietRelativeToLoud checks normalization is against the
// GLOBAL max, not per-bucket: a half-amplitude bucket reads 0.5, not 1.0.
func TestBucketPeaksQuietRelativeToLoud(t *testing.T) {
	samples := []int16{
		32000, 32000, // bucket 0: full-scale
		16000, 16000, // bucket 1: half-scale
	}
	peaks := bucketPeaks(samples, 2)
	if peaks[0] != 1.0 {
		t.Errorf("full-scale bucket = %v, want 1.0", peaks[0])
	}
	if peaks[1] != 0.5 {
		t.Errorf("half-scale bucket = %v, want 0.5 (normalized against the global max)", peaks[1])
	}
}

// TestBucketPeaksEmptyAndAllSilent: no samples, or all-zero samples, both
// yield the requested bucket count filled with zeros — never NaN/Inf/panic.
func TestBucketPeaksEmptyAndAllSilent(t *testing.T) {
	empty := bucketPeaks(nil, 5)
	if len(empty) != 5 {
		t.Fatalf("empty input: want 5 buckets, got %d", len(empty))
	}
	for i, v := range empty {
		if v != 0 {
			t.Errorf("empty input bucket %d = %v, want 0", i, v)
		}
	}

	silent := make([]int16, 100) // all zero
	got := bucketPeaks(silent, 10)
	if len(got) != 10 {
		t.Fatalf("all-silent input: want 10 buckets, got %d", len(got))
	}
	for i, v := range got {
		if v != 0 {
			t.Errorf("all-silent bucket %d = %v, want 0", i, v)
		}
	}
}

// TestBucketPeaksZeroBucketsDefaultsSafely: buckets<=0 must not divide by
// zero — it falls back to the 200-bucket default.
func TestBucketPeaksZeroBucketsDefaultsSafely(t *testing.T) {
	got := bucketPeaks([]int16{100, -100}, 0)
	if len(got) != defaultPeakBuckets {
		t.Fatalf("buckets<=0 should default to %d, got %d buckets", defaultPeakBuckets, len(got))
	}
}

// TestBucketPeaksMoreBucketsThanSamples: requesting more buckets than there
// are samples must not panic (empty buckets past the sample count read 0).
func TestBucketPeaksMoreBucketsThanSamples(t *testing.T) {
	got := bucketPeaks([]int16{32000, -32000}, 8)
	if len(got) != 8 {
		t.Fatalf("want 8 buckets, got %d", len(got))
	}
}

// TestResolvePeakBuckets covers the <=0-default and the ~2000 cap.
func TestResolvePeakBuckets(t *testing.T) {
	cases := map[int]int{
		0:    defaultPeakBuckets,
		-5:   defaultPeakBuckets,
		50:   50,
		2000: 2000,
		5000: maxPeakBuckets,
	}
	for in, want := range cases {
		if got := resolvePeakBuckets(in); got != want {
			t.Errorf("resolvePeakBuckets(%d) = %d, want %d", in, got, want)
		}
	}
}

// TestPeaksUnknownSourceDegrades: a source outside the open folder yields the
// {peaks:[],count:0} degrade contract, deterministically (no ffmpeg needed).
func TestPeaksUnknownSourceDegrades(t *testing.T) {
	app, _ := openFixture(t)
	got := app.Peaks("nope.mp4", 0, 5, 100)
	if got.Count != 0 {
		t.Errorf("unknown source Count = %d, want 0", got.Count)
	}
	if len(got.Peaks) != 0 {
		t.Errorf("unknown source Peaks = %v, want empty", got.Peaks)
	}
}
