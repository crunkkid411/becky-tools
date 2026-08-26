package main

import "testing"

// TestBurnQuoteOverlaysDegradesOnRenderFailure pins degrade-never-crash: a
// quote whose source can't actually be rendered (missing file here) must
// keep its ORIGINAL source/in/out untouched, not disappear or panic. Losing
// the overlay on one quote is not worth losing the quote itself.
func TestBurnQuoteOverlaysDegradesOnRenderFailure(t *testing.T) {
	quotes := []quoteIn{
		{Q: "QUOTE: x", Source: `X:\nonexistent\does-not-exist.mp4`, In: 0, Out: 5},
	}
	out := burnQuoteOverlays(quotes, t.TempDir(), false)
	if len(out) != 1 {
		t.Fatalf("want 1 quote back, got %d", len(out))
	}
	if out[0].Source != quotes[0].Source || out[0].In != quotes[0].In || out[0].Out != quotes[0].Out {
		t.Errorf("a failed burn must leave the quote exactly as it was, got %+v want %+v", out[0], quotes[0])
	}
}
