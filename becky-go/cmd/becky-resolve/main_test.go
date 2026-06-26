package main

import (
	"os"
	"path/filepath"
	"testing"

	"becky-go/internal/forensic"
	"becky-go/internal/orchestrate"
)

func hasVerdict(vs []orchestrate.Verdict, key string) bool {
	for _, v := range vs {
		if v.Claim == key {
			return true
		}
	}
	return false
}

// becky-resolve states a name ONLY when corroborated and HOLDS a single weak match as a candidate —
// the protocol, via the --identify JSON path (no model). (The model ladder + its degrade are tested
// canonically in internal/forensicrun; the mapping in internal/forensic.)
func TestResolve_CorroboratedNamed_SingleHeld(t *testing.T) {
	id := `{"identifications":[
	  {"type":"corroborated","name":"Shelby","confidence":0.9,"corroborated_by":["voice","face"]},
	  {"type":"voice","name":"John","confidence":0.7}
	]}`
	tmp := filepath.Join(t.TempDir(), "id.json")
	if err := os.WriteFile(tmp, []byte(id), 0o644); err != nil {
		t.Fatal(err)
	}

	raw, label, degraded := loadIdentify("clip.mp4", tmp, "kb-final")
	if degraded != "" {
		t.Fatalf("unexpected degrade: %s", degraded)
	}
	if label != "clip.mp4" {
		t.Errorf("file label = %q, want clip.mp4", label)
	}

	res := orchestrate.Resolve(forensic.IdentifyToClaims(raw), orchestrate.DefaultRules(), nil, 0)
	if !hasVerdict(res.Concluded, "person=Shelby") {
		t.Errorf("Shelby (voice+face) must be CONCLUDED, got %+v", res.Concluded)
	}
	if hasVerdict(res.Concluded, "person=John") {
		t.Errorf("John (single signal) must NOT be concluded")
	}
	if !hasVerdict(res.Candidates, "person=John") {
		t.Errorf("John must be a held CANDIDATE, got %+v", res.Candidates)
	}
}

// A missing --identify file degrades (does not crash) with an honest reason.
func TestLoadIdentify_MissingFileDegrades(t *testing.T) {
	_, _, degraded := loadIdentify("clip.mp4", filepath.Join(t.TempDir(), "nope.json"), "kb-final")
	if degraded == "" {
		t.Error("a missing --identify file must degrade with a reason, not silently succeed")
	}
}
