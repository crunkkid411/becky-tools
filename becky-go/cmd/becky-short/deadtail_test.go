package main

import (
	"testing"

	"becky-go/internal/config"
)

// The guards, asserted as VALUES on the real numbers. trimDeadTail itself needs
// a video and a pose model, so what is tested here is the budget arithmetic it
// enforces before ever calling resolveCrop.
func TestDeadTailGuardsAreTheOnesMeasured(t *testing.T) {
	// Short 01 from the chain run: 28.2s, ending on 7.7s with no subject.
	// 7.7 / 28.2 = 0.273, so the budget must allow it.
	if 7.7/28.2 > deadTailMaxFrac {
		t.Errorf("deadTailMaxFrac %.2f would not trim the 7.7s tail measured on a 28.2s short",
			deadTailMaxFrac)
	}
	// Short 02: 15.6s ending on 2.3s. 15.6 - 2.3 = 13.3s left, well over the floor.
	if 15.6-2.3 < deadTailMinKeep {
		t.Errorf("deadTailMinKeep %.1f would refuse to trim short 02", deadTailMinKeep)
	}
	// But a short that is MOSTLY untrackable is a bad pick, not a bad ending -
	// trimming it to a stub would hide that from the moment ranker.
	if deadTailMaxFrac >= 0.5 {
		t.Errorf("deadTailMaxFrac %.2f lets the trim remove half the short or more", deadTailMaxFrac)
	}
	if deadTailMinKeep < 3 {
		t.Errorf("deadTailMinKeep %.1f would leave something too short to post", deadTailMinKeep)
	}
}

// One span is the whole short; there is no tail to trim, only a short to delete.
func TestTrimDeadTailNeverEmptiesTheShort(t *testing.T) {
	one := []keepSpan{{In: 0, Out: 10}}
	got, sec, n := trimDeadTail(config.Config{}, job{}, one, "9:16", 0, 0.6, 2, nil)
	if n != 0 || sec != 0 || len(got) != 1 {
		t.Errorf("a single-span short was trimmed: spans=%d dropped=%d (%.2fs)", len(got), n, sec)
	}
	if got2, _, n2 := trimDeadTail(config.Config{}, job{}, nil, "9:16", 0, 0.6, 2, nil); n2 != 0 || got2 != nil {
		t.Errorf("nil spans returned %+v / %d", got2, n2)
	}
}

// THE MODEL'S OUT POINT IS THE OUT POINT. Jordan, 2026-08-21: "tracking a
// subject does not determine if the clip is good or not." The tail trim was the
// last place a pose tracker still had a vote on CONTENT, and once a model has
// actually watched the clip and chosen where it ends, it does not get one.
func TestWatchedShortIsNeverTailTrimmed(t *testing.T) {
	spans := []keepSpan{{In: 0, Out: 10}, {In: 10, Out: 20}, {In: 20, Out: 25}}

	setShortWatched(true)
	defer setShortWatched(false)
	got, sec, n := trimDeadTail(config.Config{}, job{}, spans, "9:16", 0, 0.6, 2, nil)
	if n != 0 || sec != 0 || len(got) != len(spans) {
		t.Errorf("a watched short lost %d span(s) / %.2fs to the tail trim; the model chose the end",
			n, sec)
	}
	if len(got) > 0 && got[len(got)-1].Out != 25 {
		t.Errorf("the watched out point moved from 25.00 to %.2f", got[len(got)-1].Out)
	}
}

// ...and resetting a short must clear that, or short 2 in a --reel run inherits
// short 1's protection and is never trimmed either.
func TestResetClearsTheWatchedFlag(t *testing.T) {
	setShortWatched(true)
	resetShortFraming(0, 10)
	if shortWatched {
		t.Error("resetShortFraming left shortWatched set; the next short would inherit it")
	}
}
