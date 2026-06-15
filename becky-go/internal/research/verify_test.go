package research

import (
	"testing"
)

func TestMakeFinding_statusRule(t *testing.T) {
	cases := []struct {
		name       string
		verdict    string
		cites      []int
		wantStatus string
	}{
		{"supports + 2 cites = corroborated", "supports", []int{1, 2}, StatusCorroborated},
		{"supports + 1 cite = candidate", "supports", []int{1}, StatusCandidate},
		{"partial = candidate", "partial", []int{1, 2}, StatusCandidate},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := makeFinding("a claim", c.verdict, c.cites)
			if f.Status != c.wantStatus {
				t.Errorf("status=%q want %q", f.Status, c.wantStatus)
			}
			if len(f.Cites) == 0 {
				t.Error("a finding must carry cites (no naked claims)")
			}
			if f.Confidence <= 0 || f.Confidence > 1 {
				t.Errorf("confidence out of range: %v", f.Confidence)
			}
		})
	}
	// Corroborated must be more confident than a lone candidate.
	if makeFinding("x", "supports", []int{1, 2}).Confidence <= makeFinding("x", "supports", []int{1}).Confidence {
		t.Error("corroborated should outrank a single-source candidate")
	}
}

func TestValidCites_dropsUnresolvable(t *testing.T) {
	textByID := map[int]string{1: "a", 3: "c"}
	got := validCites([]int{3, 1, 2, 1}, textByID) // 2 missing; 1 duplicated
	if len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Errorf("validCites should keep {1,3} sorted/deduped, got %v", got)
	}
}

func TestWorstVerdict_takesWeakest(t *testing.T) {
	helper := &FakeHelper{VerdictByClaim: map[string]string{}}
	be := Backends{Helper: helper}
	textByID := map[int]string{1: "t1", 2: "t2"}
	var deg []string

	// Same claim against both cites; FakeHelper returns the configured verdict for
	// the claim text. Force "partial" → worst across cites is partial.
	helper.VerdictByClaim["claim"] = "partial"
	if got := worstVerdict(be, "claim", []int{1, 2}, textByID, &deg); got != "partial" {
		t.Errorf("worst verdict = %q want partial", got)
	}
	helper.VerdictByClaim["claim"] = "supports"
	if got := worstVerdict(be, "claim", []int{1, 2}, textByID, &deg); got != "supports" {
		t.Errorf("all-supports should be supports, got %q", got)
	}
}

func TestVerifyDrafts_dropsUnsupportedAndNoCite(t *testing.T) {
	helper := &FakeHelper{VerdictByClaim: map[string]string{
		"good":        "supports",
		"weak":        "partial",
		"unsupported": "unsupported",
	}}
	be := Backends{Helper: helper}
	textByID := map[int]string{1: "snap1", 2: "snap2"}
	drafts := []DraftClaim{
		{Claim: "good", Cites: []int{1, 2}},     // corroborated
		{Claim: "weak", Cites: []int{1}},        // candidate (partial)
		{Claim: "unsupported", Cites: []int{2}}, // dropped (verdict)
		{Claim: "nocite", Cites: []int{99}},     // dropped (cite unresolvable)
	}
	var deg []string
	findings, dropped := verifyDrafts(be, drafts, textByID, &deg)

	if len(findings) != 2 {
		t.Fatalf("expected 2 findings (good, weak), got %d: %+v", len(findings), findings)
	}
	// Corroborated sorts before candidate.
	if findings[0].Claim != "good" || findings[0].Status != StatusCorroborated {
		t.Errorf("corroborated 'good' should sort first, got %+v", findings[0])
	}
	if len(dropped) != 2 {
		t.Fatalf("expected 2 dropped, got %d: %+v", len(dropped), dropped)
	}
	for _, d := range dropped {
		if d.Reason == "" {
			t.Error("every dropped claim must record a reason (auditable)")
		}
	}
}

func TestBuildFindings_noHelperDegradesSourcesOnly(t *testing.T) {
	var deg []string
	f, d := buildFindings(Config{}, Backends{Helper: nil}, nil, nil, &deg)
	if f != nil || d != nil {
		t.Error("no-helper run should produce no findings, just a degrade note")
	}
	if len(deg) == 0 {
		t.Error("expected a 'no-model; sources only' degrade note")
	}
}
