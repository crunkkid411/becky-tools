package main

import "testing"

// A segment fully inside a speech span counts as 100% speech.
func TestSegmentSpeechPctFullOverlap(t *testing.T) {
	spans := []vadSpan{{Start: 1.0, End: 5.0}}
	got := segmentSpeechPct(Segment{Start: 2.0, End: 3.0}, spans)
	if got < 99.9 {
		t.Fatalf("fully-covered segment should be ~100%%, got %.1f", got)
	}
}

// A segment with no overlapping speech span counts as 0% — this is the
// hallucination case ("Thank you for watching" over silence).
func TestSegmentSpeechPctNoOverlap(t *testing.T) {
	spans := []vadSpan{{Start: 10.0, End: 12.0}}
	got := segmentSpeechPct(Segment{Start: 1.0, End: 2.0}, spans)
	if got != 0 {
		t.Fatalf("non-overlapping segment should be 0%%, got %.1f", got)
	}
}

// Half-covered segment counts as ~50%.
func TestSegmentSpeechPctHalfOverlap(t *testing.T) {
	spans := []vadSpan{{Start: 0.0, End: 1.5}}
	got := segmentSpeechPct(Segment{Start: 1.0, End: 2.0}, spans)
	if got < 49 || got > 51 {
		t.Fatalf("half-covered segment should be ~50%%, got %.1f", got)
	}
}

// A zero-length segment is treated as speech (never dropped on a timing edge).
func TestSegmentSpeechPctZeroLength(t *testing.T) {
	if got := segmentSpeechPct(Segment{Start: 1.0, End: 1.0}, nil); got != 100 {
		t.Fatalf("zero-length segment should be 100%%, got %.1f", got)
	}
}

// The drop threshold: 0% overlap is below minSegmentSpeechPct (the segment would
// be dropped), and a fully-covered one is above it (kept).
func TestSegmentSpeechThreshold(t *testing.T) {
	spans := []vadSpan{{Start: 5.0, End: 6.0}}
	hallucination := segmentSpeechPct(Segment{Start: 0.0, End: 1.0}, spans)
	realSpeech := segmentSpeechPct(Segment{Start: 5.1, End: 5.9}, spans)
	if hallucination >= minSegmentSpeechPct {
		t.Errorf("a no-speech segment (%.1f%%) must fall below the %.0f%% drop threshold", hallucination, minSegmentSpeechPct)
	}
	if realSpeech < minSegmentSpeechPct {
		t.Errorf("a real-speech segment (%.1f%%) must be kept (>= %.0f%%)", realSpeech, minSegmentSpeechPct)
	}
}

// segmentsText joins only non-empty kept segments and never re-narrates dropped
// text.
func TestSegmentsText(t *testing.T) {
	got := segmentsText([]Segment{{Text: "hello"}, {Text: "  "}, {Text: "world"}})
	if got != "hello world" {
		t.Fatalf("segmentsText = %q, want %q", got, "hello world")
	}
}

// --- F4: brief-real-speech corroboration ---

// longestOverlappingRegion returns the FULL length of the longest Silero region the
// segment touches — even when the segment only clips its edge. This is the brief-
// "thank you" case from the Shelby clip: segment 2.48-3.12 clips a 0.616s Silero
// region (2.74-3.356), and the corroboration signal is that full 0.616s region.
func TestLongestOverlappingRegionEdgeClip(t *testing.T) {
	spans := []vadSpan{{Start: 2.74, End: 3.356}} // 0.616s real region
	seg := Segment{Start: 2.48, End: 3.12}        // overlaps only 0.38s of it
	got := longestOverlappingRegion(seg, spans)
	if got < 0.61 || got > 0.62 {
		t.Fatalf("region length = %.3f, want ~0.616 (the FULL Silero region, not the overlap)", got)
	}
}

// No overlap -> region 0; longest of several overlapped regions wins.
func TestLongestOverlappingRegionSelection(t *testing.T) {
	if got := longestOverlappingRegion(Segment{Start: 0, End: 1}, []vadSpan{{Start: 10, End: 12}}); got != 0 {
		t.Errorf("non-overlapping region = %.3f, want 0", got)
	}
	spans := []vadSpan{{Start: 0.5, End: 0.7}, {Start: 1.0, End: 3.0}} // 0.2s and 2.0s
	if got := longestOverlappingRegion(Segment{Start: 0.0, End: 5.0}, spans); got != 2.0 {
		t.Errorf("longest region = %.3f, want 2.0", got)
	}
}

// The F4 GUARANTEE: a brief real vocalization riding a real Silero region (>= the
// real-speech floor) is KEPT and NOT flagged low_confidence — the regression Jordan
// reported (the "thank you" was being buried).
func TestGateKeepsBriefRealSpeech(t *testing.T) {
	// Region 0.616s >= realSpeechRegionSec, so the segment is trustworthy speech.
	region := longestOverlappingRegion(Segment{Start: 2.48, End: 3.12}, []vadSpan{{Start: 2.74, End: 3.356}})
	if region < realSpeechRegionSec {
		t.Fatalf("a 0.616s Silero region (%.3f) must clear the real-speech floor %.2f so brief speech is kept",
			region, realSpeechRegionSec)
	}
}

// A transient with NO real region behind it AND near-zero overlap is the regime that
// still earns the low_confidence flag (a true sliver, not a real word).
func TestGateFlagsTransientSliver(t *testing.T) {
	seg := Segment{Start: 5.0, End: 6.0}
	spans := []vadSpan{{Start: 5.95, End: 6.0}} // a 0.05s sliver; overlap 0.05s
	region := longestOverlappingRegion(seg, spans)
	_, overlap := segmentSpeechOverlap(seg, spans)
	flagged := region < realSpeechRegionSec && overlap < lowConfMinOverlapSec
	if !flagged {
		t.Fatalf("a sub-floor sliver (region=%.3f overlap=%.3f) should be flagged low_confidence", region, overlap)
	}
}
