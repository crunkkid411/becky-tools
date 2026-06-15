package vox

import "testing"

// alignedFeatures builds a guide and a slightly-late alt (same onset pattern shifted
// +30 ms), with one detected note each, the simplest end-to-end align fixture.
func alignedFeatures() VoxFeatures {
	guide := []FeatureFrame{
		{T: 0.00, Onset: 1, Chroma: 0.5},
		{T: 0.10, Onset: 0, Chroma: 0.5},
		{T: 0.20, Onset: 1, Chroma: 0.5},
		{T: 0.30, Onset: 0, Chroma: 0.5},
	}
	alt := []FeatureFrame{
		{T: 0.03, Onset: 1, Chroma: 0.5},
		{T: 0.13, Onset: 0, Chroma: 0.5},
		{T: 0.23, Onset: 1, Chroma: 0.5},
		{T: 0.33, Onset: 0, Chroma: 0.5},
	}
	return VoxFeatures{
		Guide: guide, Alt: alt,
		GuideNotes: []DetectedNote{{StartMs: 0, EndMs: 300, Hz: 220.0, Confidence: 0.9}},
		AltNotes:   []DetectedNote{{StartMs: 30, EndMs: 330, Hz: 219.0, Confidence: 0.9}},
		DurGuideMs: 300, DurAltMs: 330,
	}
}

func TestAlign_EndToEnd(t *testing.T) {
	res := Align(alignedFeatures(), DefaultAlignOptions(), "lead.wav", "double.wav")
	if res.Tool != "becky-vox" || res.SchemaVersion != SchemaVersion {
		t.Fatalf("envelope wrong: %s v%d", res.Tool, res.SchemaVersion)
	}
	if len(res.WarpMap) == 0 {
		t.Error("warp map should be populated")
	}
	if len(res.Notes) != 1 {
		t.Errorf("expected 1 aligned note, got %d", len(res.Notes))
	}
	if len(res.Phrases) == 0 {
		t.Error("phrases should be populated")
	}
}

func TestAlign_SkippedDegrades(t *testing.T) {
	res := Align(VoxFeatures{Skipped: true, Reason: "whisper"}, DefaultAlignOptions(), "g", "a")
	if !res.Degraded || res.Reason == "" {
		t.Errorf("skipped features should degrade with a reason, got %+v", res)
	}
}

func TestAlign_FlaggedWarpSeedsCorrection(t *testing.T) {
	// A big timing offset (alt 200 ms late) exceeds the flag tolerance -> flagged +
	// a corrections-log seed (preference-learning substrate).
	f := alignedFeatures()
	for i := range f.Alt {
		f.Alt[i].T += 0.2
	}
	res := Align(f, DefaultAlignOptions(), "g", "a")
	flagged := false
	for _, w := range res.WarpMap {
		if w.Flagged {
			flagged = true
		}
	}
	if !flagged {
		t.Error("a 200 ms offset should flag at least one warp entry")
	}
	if len(res.Corrections) == 0 {
		t.Error("a flagged warp should seed a correction (auto value, corrected nil)")
	}
	for _, c := range res.Corrections {
		if c.Corrected != nil {
			t.Error("a fresh correction must have Corrected == nil until Jordan edits")
		}
	}
}

func TestAlign_Deterministic(t *testing.T) {
	a := Align(alignedFeatures(), DefaultAlignOptions(), "g", "a")
	b := Align(alignedFeatures(), DefaultAlignOptions(), "g", "a")
	if len(a.WarpMap) != len(b.WarpMap) || len(a.Notes) != len(b.Notes) {
		t.Fatal("Align not deterministic")
	}
}

func TestBuildRenderPlan_DefaultsToUnflagged(t *testing.T) {
	res := Align(alignedFeatures(), DefaultAlignOptions(), "g", "a")
	plan := BuildRenderPlan(res, "g.wav", "a.wav", nil)
	if len(plan.Accept) != len(res.Notes) {
		t.Fatalf("accept mask length %d, want %d", len(plan.Accept), len(res.Notes))
	}
	for i, n := range res.Notes {
		if plan.Accept[i] == n.Flagged {
			t.Errorf("default accept[%d]=%v should be the opposite of Flagged=%v", i, plan.Accept[i], n.Flagged)
		}
	}
}
