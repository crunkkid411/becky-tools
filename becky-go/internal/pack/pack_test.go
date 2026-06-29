package pack_test

import (
	"testing"

	"becky-go/internal/catalog"
	"becky-go/internal/pack"
)

func TestDefaultPackOffers(t *testing.T) {
	p := pack.DefaultPack()
	for _, tool := range []string{
		"becky-transcribe", "becky-diarize", "becky-identify",
		"becky-pipeline", "becky-ocr", "becky-search",
		"becky-research", "becky-radar", "becky-scout", "find",
	} {
		if !p.Offers(tool) {
			t.Errorf("DefaultPack.Offers(%q) = false, want true", tool)
		}
	}
}

func TestDefaultPackDoesNotOfferReaperBridge(t *testing.T) {
	if pack.DefaultPack().Offers("reaper-bridge") {
		t.Error("DefaultPack.Offers(reaper-bridge) = true, want false")
	}
}

func TestReaperPackOffersBridge(t *testing.T) {
	if !pack.ReaperPack().Offers("reaper-bridge") {
		t.Error("ReaperPack.Offers(reaper-bridge) = false, want true")
	}
}

// TestSwitchingPackChangesOfferedSet is the value assertion from the handoff:
// with reaper active, a non-pack tool is NOT offered; switching to default changes the set.
func TestSwitchingPackChangesOfferedSet(t *testing.T) {
	def := pack.DefaultPack()
	reaper := pack.ReaperPack()

	if !def.Offers("becky-transcribe") {
		t.Error("default must offer becky-transcribe")
	}
	if reaper.Offers("becky-transcribe") {
		// VALUE assertion: reaper pack does NOT offer transcribe — pack scoping works.
		t.Error("reaper must NOT offer becky-transcribe (pack scoping failure)")
	}
	if def.Offers("reaper-bridge") {
		t.Error("default must NOT offer reaper-bridge")
	}
	if !reaper.Offers("reaper-bridge") {
		t.Error("reaper must offer reaper-bridge")
	}
}

func TestTierOverrideApplied(t *testing.T) {
	// Build a Pack inline (all fields exported) with a tier override and assert it wins.
	p := pack.Pack{
		Name:          "test",
		Tools:         []string{"becky-transcribe"},
		TierOverrides: map[string]catalog.Tier{"becky-transcribe": catalog.TierRed},
	}
	got := p.TierFor("becky-transcribe")
	if got != catalog.TierRed {
		// VALUE assertion: override must produce red, not the catalog's green.
		t.Errorf("TierFor(becky-transcribe) with red override = %q, want %q", got, catalog.TierRed)
	}
}

func TestTierForCatalogFallback(t *testing.T) {
	p := pack.DefaultPack() // no tier_overrides in default.json

	tests := []struct {
		tool string
		want catalog.Tier
	}{
		{"becky-transcribe", catalog.TierGreen}, // catalog = green
		{"becky-export", catalog.TierRed},       // catalog = red
		{"becky-cut", catalog.TierYellow},       // catalog = yellow
	}
	for _, tc := range tests {
		got := p.TierFor(tc.tool)
		if got != tc.want {
			t.Errorf("TierFor(%q) = %q, want %q (catalog fallback)", tc.tool, got, tc.want)
		}
	}
}

func TestLoadBuiltinDefault(t *testing.T) {
	p, err := pack.Load("default")
	if err != nil {
		t.Fatalf("Load(default): %v", err)
	}
	if p.Name != "default" {
		t.Errorf("Name = %q, want default", p.Name)
	}
	if !p.Offers("becky-transcribe") {
		t.Error("default pack must offer becky-transcribe")
	}
}

func TestLoadBuiltinReaper(t *testing.T) {
	p, err := pack.Load("reaper")
	if err != nil {
		t.Fatalf("Load(reaper): %v", err)
	}
	if p.Name != "reaper" {
		t.Errorf("Name = %q, want reaper", p.Name)
	}
	if !p.Offers("reaper-bridge") {
		t.Error("reaper pack must offer reaper-bridge")
	}
}

func TestLoadUnknownReturnsError(t *testing.T) {
	_, err := pack.Load("does-not-exist-xyzzy")
	if err == nil {
		t.Error("Load(unknown) returned nil error, want error")
	}
}
