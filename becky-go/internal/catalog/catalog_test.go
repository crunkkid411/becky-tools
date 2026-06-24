package catalog

import "testing"

// An unknown verb must default to RED (fail-safe — SPEC-BECKY-VOICE.md §4.1).
func TestTierOf_UnknownDefaultsRed(t *testing.T) {
	if got := TierOf("becky-does-not-exist"); got != TierRed {
		t.Errorf("unknown tool tier = %q, want %q", got, TierRed)
	}
}

// A Capability with no Tier set is treated as RED via TierOf().
func TestCapability_ZeroTierIsRed(t *testing.T) {
	var c Capability
	if got := c.TierOf(); got != TierRed {
		t.Errorf("zero-value Capability tier = %q, want %q", got, TierRed)
	}
}

// The known tools the first packs use must carry their explicit tier + pack.
func TestKnownToolsTierAndPack(t *testing.T) {
	cases := []struct {
		verb string
		tier Tier
		pack string
	}{
		{"becky-transcribe", TierGreen, "default"},
		{"becky-diarize", TierGreen, "default"},
		{"becky-identify", TierGreen, "forensic"},
		{"becky-pipeline", TierGreen, "default"},
		{"becky-ocr", TierGreen, "default"},
		{"becky-search", TierGreen, "default"},
		{"becky-research", TierGreen, "default"},
		{"becky-radar", TierGreen, "default"},
		{"becky-scout", TierGreen, "default"},
		{"becky-export", TierRed, "default"},
		{"reaper-bridge", TierYellow, "reaper"},
	}
	for _, tc := range cases {
		c, ok := Lookup(tc.verb)
		if !ok {
			t.Errorf("%s: not found in catalog", tc.verb)
			continue
		}
		if c.TierOf() != tc.tier {
			t.Errorf("%s: tier = %q, want %q", tc.verb, c.TierOf(), tc.tier)
		}
		if c.Pack != tc.pack {
			t.Errorf("%s: pack = %q, want %q", tc.verb, c.Pack, tc.pack)
		}
	}
}

// InPack returns only the entries assigned to that pack, and reaper-bridge is reaper-only.
func TestInPack(t *testing.T) {
	reaper := InPack("reaper")
	if len(reaper) != 1 || reaper[0].Verb != "reaper-bridge" {
		t.Fatalf("reaper pack = %+v, want exactly [reaper-bridge]", reaper)
	}
	for _, c := range InPack("forensic") {
		if c.Pack != "forensic" {
			t.Errorf("InPack(forensic) returned %s with pack %q", c.Verb, c.Pack)
		}
	}
}

// Lookup of an unknown verb returns ok=false.
func TestLookupUnknown(t *testing.T) {
	if _, ok := Lookup("nope"); ok {
		t.Error("Lookup(nope) should report ok=false")
	}
}

// MatchCapabilities still keyword-matches (behavior preserved from cmd/ask).
func TestMatchCapabilities_Preserved(t *testing.T) {
	hits := MatchCapabilities("can you transcribe this video")
	found := false
	for _, h := range hits {
		if h.Verb == "becky-transcribe" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected becky-transcribe in hits for 'transcribe'; got %+v", hits)
	}
}

// AllOpsList is sorted by verb and contains only orchestrator ops.
func TestAllOpsList_Sorted(t *testing.T) {
	ops := AllOpsList()
	if len(ops) != len(OrchestratorOps) {
		t.Fatalf("AllOpsList len = %d, want %d", len(ops), len(OrchestratorOps))
	}
	for i := 1; i < len(ops); i++ {
		if ops[i-1].Verb > ops[i].Verb {
			t.Errorf("not sorted: %q before %q", ops[i-1].Verb, ops[i].Verb)
		}
	}
}
