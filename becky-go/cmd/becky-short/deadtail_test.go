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
