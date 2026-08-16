package main

import (
	"math"
	"testing"
)

// The two defects these cover both DELETED JORDAN'S WORDS from an edit
// (2026-08-16, IMG_9624.MP4). Both are asserted on the real measured numbers
// from that clip, not on invented ones.

// TestDetectThresholdDBFollowsTheFileLevel: a quiet recording must get a quiet
// threshold, and normal/loud footage must keep the behaviour it always had
// (auto-editor's own default) so this can only ever keep MORE, never less.
func TestDetectThresholdDBFollowsTheFileLevel(t *testing.T) {
	cases := []struct {
		name   string
		meanDB float64
		want   float64
	}{
		// Jordan's IMG_9624.MP4 — the clip that came out shredded.
		{"quiet phone clip", -42.2, -41.2},
		// His polished reference audio measures -27.8: already loud enough, so the
		// clamp hands it auto-editor's default (within ~1dB of the -27dB he types).
		{"his polished audio", -27.8, -28.0},
		// cli-cut's own test_VAD.mp4 — normal level, must not change.
		{"normal level", -17.8, -28.0},
		{"loud", -8.0, -28.0},
		// Near-silent: the floor stops us chasing room tone.
		{"near silent", -70.0, -50.0},
	}
	for _, c := range cases {
		got := detectThresholdDB(c.meanDB)
		if math.Abs(got-c.want) > 0.05 {
			t.Errorf("%s: detectThresholdDB(%.1f) = %.2f, want %.2f", c.name, c.meanDB, got, c.want)
		}
	}
}

func TestParseMeanVolumeDB(t *testing.T) {
	const real = `[Parsed_volumedetect_0 @ 0000022b] n_samples: 7864320
[Parsed_volumedetect_0 @ 0000022b] mean_volume: -42.2 dB
[Parsed_volumedetect_0 @ 0000022b] max_volume: -9.6 dB`
	v, ok := parseMeanVolumeDB(real)
	if !ok || math.Abs(v-(-42.2)) > 0.001 {
		t.Fatalf("parseMeanVolumeDB = %v, %v; want -42.2, true", v, ok)
	}
	if _, ok := parseMeanVolumeDB("no audio stream here"); ok {
		t.Error("parseMeanVolumeDB should report ok=false when the line is absent")
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
