package orchestrate

import "testing"

// TestPresence_WatchConcludesWindow: a mention + a model WATCH in the same window conclude
// presence (2 independent sources + a watch); the watch is what makes presence statable.
func TestPresence_WatchConcludesWindow(t *testing.T) {
	sigs := []TimedSignal{
		{Source: "becky-transcribe", Kind: KindMention, Confidence: 0.9, Start: 10, End: 11},
		{Source: "becky-validate", Kind: KindWatched, Confidence: 0.9, Start: 10, End: 14},
	}
	claims := CorrelatePresence("cat", sigs, 2.0)
	if len(claims) != 1 {
		t.Fatalf("want one window, got %d (%+v)", len(claims), claims)
	}
	if v := Corroborate(claims[0], DefaultRules()); v.Status != Concluded {
		t.Errorf("mention + watch => %s, want concluded. reason=%q", v.Status, v.Reason)
	}
}

// TestPresence_NoWatchNeverConcludes: a mention + a motion burst, with NO watch, can never be
// stated as on-screen — the load-bearing forensic rule.
func TestPresence_NoWatchNeverConcludes(t *testing.T) {
	sigs := []TimedSignal{
		{Source: "becky-transcribe", Kind: KindMention, Confidence: 0.99, Start: 10, End: 11},
		{Source: "becky-motion", Kind: KindMotion, Confidence: 0.99, Start: 10, End: 12},
	}
	claims := CorrelatePresence("cat", sigs, 2.0)
	if len(claims) != 1 {
		t.Fatalf("want one window, got %d", len(claims))
	}
	if v := Corroborate(claims[0], DefaultRules()); v.Status == Concluded {
		t.Errorf("presence with no watch must NEVER conclude, got concluded: %q", v.Reason)
	}
}

// TestPresence_SplitsFarApartWindows: signals separated by more than mergeGap are separate
// windows, so becky reports TIGHT intervals, not one smeared blob.
func TestPresence_SplitsFarApartWindows(t *testing.T) {
	sigs := []TimedSignal{
		{Source: "becky-validate", Kind: KindWatched, Confidence: 0.9, Start: 10, End: 12},
		{Source: "becky-validate", Kind: KindWatched, Confidence: 0.9, Start: 40, End: 42},
	}
	claims := CorrelatePresence("cat", sigs, 2.0)
	if len(claims) != 2 {
		t.Fatalf("two far-apart watches => want two windows, got %d (%+v)", len(claims), claims)
	}
}

// TestPresence_Empty: no signals => no claims (degrade, don't crash).
func TestPresence_Empty(t *testing.T) {
	if got := CorrelatePresence("cat", nil, 2.0); got != nil {
		t.Errorf("no signals => want nil claims, got %+v", got)
	}
}
