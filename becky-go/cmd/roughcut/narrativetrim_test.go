package main

import "testing"

func TestDedupeCaptionChunksCollapsesRollingWindow(t *testing.T) {
	events := []tlEvent{
		{Source: "a.mp4", In: 0, Out: 1, TL: 0, Dialogue: "The FBI sent"},
		{Source: "a.mp4", In: 1, Out: 2.5, TL: 1, Dialogue: "The FBI sent someone here"},
		{Source: "a.mp4", In: 2.5, Out: 4, TL: 2.5, Dialogue: "The FBI sent someone here to investigate"},
		// new sentence - not a superstring extension of the previous chunk's text
		{Source: "a.mp4", In: 4, Out: 5, TL: 4, Dialogue: "Do you want to know what happened"},
	}
	chunks := dedupeCaptionChunks(events)
	if len(chunks) != 2 {
		t.Fatalf("want 2 chunks (one rolling sentence + one new sentence), got %d: %+v", len(chunks), chunks)
	}
	if chunks[0].Text != "The FBI sent someone here to investigate" {
		t.Errorf("chunk 0 should collapse to the FULLEST rolling text, got %q", chunks[0].Text)
	}
	if len(chunks[0].EventIdx) != 3 {
		t.Errorf("chunk 0 should cover all 3 rolling events, got %v", chunks[0].EventIdx)
	}
	if chunks[1].Text != "Do you want to know what happened" {
		t.Errorf("chunk 1 wrong text: %q", chunks[1].Text)
	}
}

func TestDedupeCaptionChunksBreaksOnGap(t *testing.T) {
	events := []tlEvent{
		{Source: "a.mp4", In: 0, Out: 1, TL: 0, Dialogue: "hello"},
		// big silent gap before the next event, even though the text still LOOKS
		// like a continuation
		{Source: "a.mp4", In: 5, Out: 6, TL: 5, Dialogue: "hello again"},
	}
	chunks := dedupeCaptionChunks(events)
	if len(chunks) != 2 {
		t.Fatalf("a >0.75s gap must force a new chunk regardless of text, got %d chunks", len(chunks))
	}
}

func TestGroupChunksIntoBeatsCapsLength(t *testing.T) {
	// 5 chunks of 10s each, contiguous - must split into 2 beats under a 30s cap,
	// never merge past narrativeBeatCapSec.
	var chunks []capChunk
	for i := 0; i < 5; i++ {
		start := float64(i * 10)
		chunks = append(chunks, capChunk{Text: "x", TLStart: start, TLEnd: start + 10, EventIdx: []int{i}})
	}
	beats := groupChunksIntoBeats(chunks)
	for _, b := range beats {
		if b.TLEnd-b.TLStart > narrativeBeatCapSec {
			t.Errorf("beat %+v exceeds the %vs cap", b, narrativeBeatCapSec)
		}
	}
	total := 0
	for _, b := range beats {
		total += len(b.EventIdx)
	}
	if total != 5 {
		t.Errorf("want all 5 source events accounted for across beats, got %d", total)
	}
}

// TestApplyNarrativeCutsShiftsQuotesAndMarkersPastACut is the load-bearing
// check: cutting a MIDDLE beat must shift every later event, quote, and
// marker left by exactly the cut's duration, and must drop (not corrupt) a
// marker that falls inside the cut span - the same class of bug the TL-shift
// mismatch in triage.go's reshiftPendingTL was built to fix, here for a
// removal instead of an insertion.
func TestApplyNarrativeCutsShiftsQuotesAndMarkersPastACut(t *testing.T) {
	events := []tlEvent{
		{Source: "a.mp4", In: 0, Out: 5, TL: 0, Dialogue: "keep this"},   // beat 0: kept
		{Source: "a.mp4", In: 5, Out: 10, TL: 5, Dialogue: "cut this"},   // beat 1: cut, 5s
		{Source: "a.mp4", In: 10, Out: 15, TL: 10, Dialogue: "keep too"}, // beat 2: kept
	}
	quotes := []quoteOut{{Source: "q.mp4", In: 0, Out: 3, TL: 15}}
	markers := []markerOut{
		{T: 2, Title: "inside kept beat 0"},
		{T: 7, Title: "inside cut beat 1 - must be dropped"},
		{T: 12, Title: "inside kept beat 2 - must shift left 5s"},
	}
	beats := []narrativeBeat{
		{Index: 0, TLStart: 0, TLEnd: 5, EventIdx: []int{0}},
		{Index: 1, TLStart: 5, TLEnd: 10, EventIdx: []int{1}},
		{Index: 2, TLStart: 10, TLEnd: 15, EventIdx: []int{2}},
	}
	verdicts := []narrativeVerdict{
		{Index: 0, Cut: false},
		{Index: 1, Cut: true, Reason: "redundant"},
		{Index: 2, Cut: false},
	}

	newEvents, newQuotes, newMarkers, newRegions, cutLog := applyNarrativeCuts(events, quotes, markers, verdicts, beats)

	if len(newEvents) != 2 {
		t.Fatalf("want 2 surviving events, got %d: %+v", len(newEvents), newEvents)
	}
	if newEvents[0].TL != 0 || newEvents[1].TL != 5 {
		t.Errorf("surviving events must butt-join from 0 with the 5s gap closed, got tl=%v,%v", newEvents[0].TL, newEvents[1].TL)
	}
	if len(newQuotes) != 1 || newQuotes[0].TL != 10 {
		t.Fatalf("quote must shift left by the cut's 5s (15 -> 10), got %+v", newQuotes)
	}
	if len(newMarkers) != 2 {
		t.Fatalf("want 2 surviving markers (the one inside the cut span must be dropped), got %d: %+v", len(newMarkers), newMarkers)
	}
	if newMarkers[0].T != 2 {
		t.Errorf("marker before the cut must not move, got %v", newMarkers[0].T)
	}
	if newMarkers[1].T != 7 {
		t.Errorf("marker after the cut must shift left by 5s (12 -> 7), got %v", newMarkers[1].T)
	}
	for _, m := range newMarkers {
		if m.Title == "inside cut beat 1 - must be dropped" {
			t.Errorf("the marker anchored inside the cut span must be dropped, not kept at a wrong position")
		}
	}
	if len(newRegions) != 1 || newRegions[0].Len != 10 {
		t.Errorf("regions must be rebuilt fresh over the 2 surviving events (10s total), got %+v", newRegions)
	}
	if len(cutLog) != 1 || cutLog[0]["reason"] != "redundant" {
		t.Errorf("cut log must record exactly the one real cut with its reason, got %+v", cutLog)
	}
}

func TestApplyNarrativeCutsNeverTouchesQuotesAsCutCandidates(t *testing.T) {
	// verdicts can only ever reference beats (built from events); there is no
	// code path that lets a quote be marked cut. This test pins that quotes
	// always survive regardless of how aggressively events are cut.
	events := []tlEvent{{Source: "a.mp4", In: 0, Out: 5, TL: 0, Dialogue: "cut everything"}}
	quotes := []quoteOut{{Source: "q.mp4", In: 0, Out: 3, TL: 5}}
	beats := []narrativeBeat{{Index: 0, TLStart: 0, TLEnd: 5, EventIdx: []int{0}}}
	verdicts := []narrativeVerdict{{Index: 0, Cut: true}}

	newEvents, newQuotes, _, _, _ := applyNarrativeCuts(events, quotes, nil, verdicts, beats)
	if len(newEvents) != 0 {
		t.Errorf("the cut event must be gone, got %+v", newEvents)
	}
	if len(newQuotes) != 1 || newQuotes[0].TL != 0 {
		t.Fatalf("the quote must survive and shift to the front (5 -> 0), got %+v", newQuotes)
	}
}

func TestParseNarrativeVerdictsToleratesStrayFormatting(t *testing.T) {
	raw := "Sure, here are the verdicts:\n" +
		"- {\"index\":0,\"cut\":true,\"reason\":\"already said\"}\n" +
		"{\"index\":1,\"cut\":false,\"reason\":\"new fact\"}\n" +
		"not json at all\n"
	got := parseNarrativeVerdicts(raw)
	if len(got) != 2 {
		t.Fatalf("want 2 parsed verdicts despite stray prefix/junk lines, got %d: %+v", len(got), got)
	}
	if !got[0].Cut || got[0].Reason != "already said" {
		t.Errorf("verdict 0 parsed wrong: %+v", got[0])
	}
	if got[1].Cut {
		t.Errorf("verdict 1 should be cut:false, got %+v", got[1])
	}
}
