package main

import "testing"

// TestSpliceLayoutPreservesClipOrderAcrossOverlappingSourceTimes pins the fix
// for "the clips are out of fucking order" (Jordan, 2026-08-25): place() used
// to filter events by comparing their SOURCE-relative in/out directly against
// timeline-position window bounds. Two clips with events at similar
// source-relative times (very common - every clip's own clock restarts near
// 0) would get scattered across every splice window instead of staying
// contiguous. This constructs exactly that trap: clip A's only event sits at
// a LARGE source time, clip B's only event sits at a SMALL source time that
// clip A's true source values do not naturally overlap with the early
// windows - the old code would place a slice of the WRONG clip into the
// first window purely because its raw source seconds happened to be small.
func TestSpliceLayoutPreservesClipOrderAcrossOverlappingSourceTimes(t *testing.T) {
	events := []event{
		// clip A: chronologically FIRST (it's first in the events slice, same
		// as main.go builds it - one entry per clip in creation-time order),
		// but its kept span happens to sit late in ITS OWN source file.
		{Source: "A.mp4", In: 500, Out: 510, TLStart: 0},
		// clip B: chronologically SECOND, but its kept span sits early in its
		// own source file - overlapping clip A's TIMELINE window if (and only
		// if) the bug compares raw source seconds instead of TLStart.
		{Source: "B.mp4", In: 10, Out: 20, TLStart: 10},
	}
	// marker sits exactly at the A/B boundary (clip A occupies TL [0,10),
	// since its kept span is 500-510) so this tests clip ORDER, not a
	// mid-event split (that is TestSpliceLayoutSplitsAnEventAtAMidSpliceMarker).
	markers := []markerOut{{T: 10, Title: "QUOTE: between-A-and-B"}}
	quotes := []quoteIn{{Q: "QUOTE: between-A-and-B", Source: "q.mp4", In: 0, Out: 3}}

	lay := spliceLayout(events, markers, quotes)

	if len(lay.Events) != 2 {
		t.Fatalf("want exactly 2 output events (one whole slice per clip, no scattering), got %d: %+v", len(lay.Events), lay.Events)
	}
	if lay.Events[0].Source != "A.mp4" || lay.Events[1].Source != "B.mp4" {
		t.Fatalf("clip order must survive the splice (A before B, matching input order) - got %s then %s",
			lay.Events[0].Source, lay.Events[1].Source)
	}
	// clip A must keep its OWN large source seconds (500-510) whole - the old
	// bug would let clip B's small source seconds (10-20) leak into an early
	// window meant for clip A, or truncate A against B's source range.
	if lay.Events[0].In != 500 || lay.Events[0].Out != 510 {
		t.Errorf("clip A must keep its full, unsplit source range (500-510), got %v-%v", lay.Events[0].In, lay.Events[0].Out)
	}
	if lay.Events[1].In != 10 || lay.Events[1].Out != 20 {
		t.Errorf("clip B must keep its own source range (10-20), got %v-%v", lay.Events[1].In, lay.Events[1].Out)
	}
}

// TestSpliceLayoutSplitsAnEventAtAMidSpliceMarker checks the edge the fix
// touches directly: a marker landing in the MIDDLE of one event's timeline
// window must split that one event into two source-contiguous pieces with
// the quote's own track in between, not drop or duplicate any of it.
func TestSpliceLayoutSplitsAnEventAtAMidSpliceMarker(t *testing.T) {
	events := []event{{Source: "A.mp4", In: 100, Out: 110, TLStart: 0}}
	markers := []markerOut{{T: 4, Title: "QUOTE: X"}}
	quotes := []quoteIn{{Q: "QUOTE: X", Source: "q.mp4", In: 0, Out: 2}}

	lay := spliceLayout(events, markers, quotes)

	if len(lay.Events) != 2 {
		t.Fatalf("a marker mid-event must split it into 2 pieces, got %d: %+v", len(lay.Events), lay.Events)
	}
	first, second := lay.Events[0], lay.Events[1]
	if first.In != 100 || first.Out != 104 {
		t.Errorf("first half must be source 100-104 (the 4s before the marker), got %v-%v", first.In, first.Out)
	}
	if second.In != 104 || second.Out != 110 {
		t.Errorf("second half must be source 104-110 (the remaining 6s after the quote), got %v-%v", second.In, second.Out)
	}
	if second.TL != first.TL+(first.Out-first.In)+2 {
		t.Errorf("second half must start after first half PLUS the 2s quote, got TL=%v", second.TL)
	}
	if len(lay.Quotes) != 1 || lay.Quotes[0].TL != first.TL+(first.Out-first.In) {
		t.Fatalf("the quote must sit exactly between the two split halves, got %+v", lay.Quotes)
	}
}
