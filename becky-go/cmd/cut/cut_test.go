package main

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// The two defects these cover both DELETED JORDAN'S WORDS from an edit
// (2026-08-16, IMG_9624.MP4). Both are asserted on the real measured numbers
// from that clip, not on invented ones.

// TestDetectThresholdDBSitsInsideTheFileOwnValley: the threshold must follow
// the recording DOWN without limit (that clamp is what made the Rode shoot
// unreachable), and normal-level footage must keep the behaviour it always had
// -- auto-editor's own default -- so this can only ever keep MORE, never less.
func TestDetectThresholdDBSitsInsideTheFileOwnValley(t *testing.T) {
	cases := []struct {
		name              string
		floorDB, speechDB float64
		want              float64
	}{
		// The 16-clip Rode Wireless GO II shoot, 2026-08-26. Each "want" is
		// floor + 0.52*valley; the OLD estimator picked ~-43 for these and
		// shredded every one of them.
		{"rode VTNZ3433", -88.0, -37.0, -61.48},
		{"rode SNOW_...143", -83.0, -35.0, -58.04},
		{"rode LZTE3925", -85.0, -36.0, -59.52},
		// Normal-level footage: a high floor plus a loud programme level puts the
		// adaptive answer above auto-editor's default, so the ceiling wins and
		// nothing about his already-working iPhone edits changes.
		{"normal level", -40.0, -10.0, -28.0},
		{"loud and compressed", -35.0, -6.0, -28.0},
	}
	for _, c := range cases {
		got := detectThresholdDB(c.floorDB, c.speechDB, defaultValleyFraction, defaultHeadroomDB)
		if math.Abs(got-c.want) > 0.05 {
			t.Errorf("%s: detectThresholdDB(%.1f, %.1f) = %.2f, want %.2f",
				c.name, c.floorDB, c.speechDB, got, c.want)
		}
	}
	// --headroom is an additive nudge on top, with no floor under it: a shoot
	// that needs to keep even more is reachable, which it was not before.
	if got := detectThresholdDB(-88.0, -37.0, defaultValleyFraction, -6.0); math.Abs(got-(-67.48)) > 0.05 {
		t.Errorf("negative headroom: got %.2f, want -67.48", got)
	}
	// The ceiling still holds against a positive nudge.
	if got := detectThresholdDB(-40.0, -10.0, defaultValleyFraction, 12.0); math.Abs(got-defaultThresholdDB) > 0.05 {
		t.Errorf("headroom must not break the ceiling: got %.2f, want %.1f", got, defaultThresholdDB)
	}
	// --valley-fraction reaches the one magic number from the CLI.
	if got := detectThresholdDB(-88.0, -38.0, 0.20, 0.0); math.Abs(got-(-78.0)) > 0.05 {
		t.Errorf("valley fraction 0.20: got %.2f, want -78.0", got)
	}
}

// TestPercentileMatchesNumpyLinearInterpolation keeps becky's floor/speech
// numbers identical to scripts/speechcut.py's, which reports the same two
// percentiles with numpy's default (linear) interpolation.
func TestPercentileMatchesNumpyLinearInterpolation(t *testing.T) {
	sorted := []float64{-90, -80, -70, -60, -50, -40, -30, -20, -10, 0}
	// numpy.percentile(x, 5) = -85.5, numpy.percentile(x, 90) = -9.0
	if got := percentile(sorted, 5); math.Abs(got-(-85.5)) > 1e-9 {
		t.Errorf("percentile 5 = %v, want -85.5", got)
	}
	if got := percentile(sorted, 90); math.Abs(got-(-9.0)) > 1e-9 {
		t.Errorf("percentile 90 = %v, want -9.0", got)
	}
	if got := percentile([]float64{-42.0}, 50); got != -42.0 {
		t.Errorf("single sample = %v, want -42.0", got)
	}
	if got := percentile(nil, 50); got != 0 {
		t.Errorf("empty = %v, want 0", got)
	}
}

// TestFrameDBEnvelopeAndStats: a synthetic recording of loud tone and true
// silence must measure a wide valley with the floor at the silence and the
// speech level at the tone, and produce speechcut.py's frame count.
func TestFrameDBEnvelopeAndStats(t *testing.T) {
	// 1 second: 0.5 s of half-scale square wave, 0.5 s of digital silence.
	samples := make([]int16, levelSampleRate)
	for i := 0; i < levelSampleRate/2; i++ {
		if i%2 == 0 {
			samples[i] = 16384
		} else {
			samples[i] = -16384
		}
	}
	buf := make([]byte, 2*len(samples))
	for i, s := range samples {
		binary.LittleEndian.PutUint16(buf[2*i:], uint16(s))
	}

	db := frameDB(bytes.NewReader(buf))
	wantFrames := 1 + (len(samples)-levelFrame)/levelHop
	if len(db) != wantFrames {
		t.Fatalf("frameDB returned %d frames, want %d", len(db), wantFrames)
	}

	st := statsFromFrameDB(db)
	// Half scale is -6.02 dBFS; digital silence is 20*log10(1e-10) = -200.
	if math.Abs(st.SpeechDB-(-6.02)) > 0.1 {
		t.Errorf("speech level = %.2f dBFS, want -6.02", st.SpeechDB)
	}
	if st.FloorDB > -199.0 {
		t.Errorf("floor = %.2f dBFS, want about -200 on digital silence", st.FloorDB)
	}
	if st.ValleyDB < minValleyDB {
		t.Errorf("valley = %.1f dB, want well above the %.1f dB gate", st.ValleyDB, minValleyDB)
	}
	// Under one analysis window there is nothing to measure, and that must not
	// be an error -- the caller falls back to auto-editor's default.
	if got := frameDB(bytes.NewReader(buf[:10])); got != nil {
		t.Errorf("frameDB on a too-short input = %v, want nil", got)
	}
}

// TestSpeechPctScoresSegmentsAgainstWholeFileSpans is the regression test for
// the deleted words: segment 52.5-53.0s of IMG_9624.MP4 is speech (the whole-file
// VAD covers 52.544-54.0), and the old per-segment call scored it 0% and cut it.
func TestSpeechPctScoresSegmentsAgainstWholeFileSpans(t *testing.T) {
	spans := []span{{Start: 50.0, End: 51.2}, {Start: 52.544, End: 54.0}}
	if got := speechPct(spans, 52.5, 53.0); got < 85 || got > 95 {
		t.Errorf("speech segment scored %.1f%%, want ~91%% (it used to score 0 and be deleted)", got)
	}
	if got := speechPct(spans, 51.3, 52.4); got != 0 {
		t.Errorf("silence scored %.1f%%, want 0", got)
	}
	// Partial overlap on both sides, and out-of-order spans, still add up.
	unordered := []span{{Start: 5, End: 6}, {Start: 1, End: 2}}
	if got := speechPct(unordered, 0, 10); math.Abs(got-20) > 0.001 {
		t.Errorf("speechPct = %.2f, want 20", got)
	}
	if got := speechPct(spans, 3, 3); got != 0 {
		t.Errorf("empty window scored %.1f, want 0", got)
	}
}
