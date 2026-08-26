package main

import "testing"

func TestFindFreshStartsFlagsClipChangeAndRealGap(t *testing.T) {
	events := []tlEvent{
		{Source: "A.mp4", In: 10, Out: 15, TL: 0},   // 0: first event - always fresh
		{Source: "A.mp4", In: 15, Out: 20, TL: 5},   // 1: continuous (gap 0) - NOT fresh
		{Source: "A.mp4", In: 25, Out: 30, TL: 10},  // 2: 5s source gap - fresh
		{Source: "B.mp4", In: 2, Out: 8, TL: 15},    // 3: clip change - fresh
		{Source: "B.mp4", In: 8.05, Out: 9, TL: 21}, // 4: 0.05s gap (rounding) - NOT fresh
	}
	got := findFreshStarts(events)
	wantIdx := []int{0, 2, 3}
	if len(got) != len(wantIdx) {
		t.Fatalf("want %d fresh starts, got %d: %+v", len(wantIdx), len(got), got)
	}
	for i, w := range wantIdx {
		if got[i].EventIdx != w {
			t.Errorf("fresh start %d: want event index %d, got %d", i, w, got[i].EventIdx)
		}
	}
}

func TestSpeakingConfidentAtStart(t *testing.T) {
	speaking := []speakingWindow{{Start: 10, End: 20, BestFrac: 0.7}}
	if !speakingConfidentAtStart(speaking, 10.5) {
		t.Error("0.7 confidence covering the first second should count as confident")
	}
	if speakingConfidentAtStart(speaking, 25) {
		t.Error("no overlapping window at all should never be confident")
	}
	low := []speakingWindow{{Start: 10, End: 20, BestFrac: 0.2}}
	if speakingConfidentAtStart(low, 10.5) {
		t.Error("0.2 confidence is below the bar and must not count as confident")
	}
}

func TestParseLeadTrimSeconds(t *testing.T) {
	cases := []struct {
		raw    string
		wantV  float64
		wantOK bool
	}{
		{"He is settling in, adjusting his chair.\nSECONDS: 1.4", 1.4, true},
		{"Already mid-sentence.\nSECONDS: 0", 0, true},
		{"seconds:2.75", 2.75, true},
		{"Can't quite tell.\nSECONDS: -1", 0, false},
		{"I'm not sure", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		v, ok := parseLeadTrimSeconds(c.raw)
		if ok != c.wantOK {
			t.Errorf("parseLeadTrimSeconds(%q): ok=%v want %v", c.raw, ok, c.wantOK)
			continue
		}
		if ok && v != c.wantV {
			t.Errorf("parseLeadTrimSeconds(%q): v=%v want %v", c.raw, v, c.wantV)
		}
	}
}

// TestApplyLeadTrimsShrinksFrontAndShiftsEverythingAfter is the load-bearing
// check: trimming one event's front must shrink ONLY that event (never its
// end), and ripple-shift every later event/quote/marker earlier by exactly
// the trimmed amount - while anything BEFORE the trim point stays untouched.
func TestApplyLeadTrimsShrinksFrontAndShiftsEverythingAfter(t *testing.T) {
	events := []tlEvent{
		{Source: "A.mp4", In: 0, Out: 5, TL: 0},    // untouched (before the trim)
		{Source: "A.mp4", In: 5, Out: 15, TL: 5},   // trimmed 2s off the front
		{Source: "A.mp4", In: 15, Out: 20, TL: 15}, // must shift left by 2s
	}
	quotes := []quoteOut{{Source: "q.mp4", In: 0, Out: 3, TL: 20}} // must shift left by 2s
	markers := []markerOut{
		{T: 2, Title: "before the trim - must not move"},
		{T: 17, Title: "after the trim - must shift left by 2s"},
	}
	verdicts := []leadTrimVerdict{{EventIdx: 1, OffsetIntoWindow: 2, Confident: true}}

	newEvents, newQuotes, newMarkers, trimmed := applyLeadTrims(events, quotes, markers, verdicts)

	if trimmed != 2 {
		t.Fatalf("want 2s total trimmed, got %v", trimmed)
	}
	if newEvents[0].TL != 0 || newEvents[0].In != 0 {
		t.Errorf("event before the trim must be untouched, got %+v", newEvents[0])
	}
	if newEvents[1].In != 7 || newEvents[1].Out != 15 || newEvents[1].TL != 5 {
		t.Fatalf("trimmed event must keep its TL, gain 2s on In, keep its Out, got %+v", newEvents[1])
	}
	if newEvents[2].TL != 13 || newEvents[2].In != 15 {
		t.Fatalf("event after the trim must shift TL left by 2s and keep its own In/Out unchanged, got %+v", newEvents[2])
	}
	if newQuotes[0].TL != 18 {
		t.Errorf("quote after the trim must shift left by 2s (20->18), got %v", newQuotes[0].TL)
	}
	if newMarkers[0].T != 2 {
		t.Errorf("marker before the trim must not move, got %v", newMarkers[0].T)
	}
	if newMarkers[1].T != 15 {
		t.Errorf("marker after the trim must shift left by 2s (17->15), got %v", newMarkers[1].T)
	}
}

func TestApplyLeadTrimsNoVerdictsChangesNothing(t *testing.T) {
	events := []tlEvent{{Source: "A.mp4", In: 0, Out: 5, TL: 0}}
	newEvents, _, _, trimmed := applyLeadTrims(events, nil, nil, nil)
	if trimmed != 0 || newEvents[0] != events[0] {
		t.Errorf("no verdicts must mean no change at all, got %+v trimmed=%v", newEvents, trimmed)
	}
}
