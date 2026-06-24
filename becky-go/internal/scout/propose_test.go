package scout

import "testing"

func usefulItems() []Item {
	return []Item{
		{Video: Video{ID: "a", Title: "great idea", URL: "https://youtu.be/a"}},
		{Video: Video{ID: "b", Title: "meh", URL: "https://youtu.be/b"}},
	}
}

// Proposer pitches + a judge agrees → APPROVED (two independent models concur).
func TestProposeApprovedOnAgreement(t *testing.T) {
	proposer := FakeProposer{ByID: map[string]Proposal{
		"a": {WorthBuilding: true, Slug: "becky-foo", Capability: "Do foo.", Kind: "extend"},
	}}
	judges := []Judge{FakeJudge{JudgeName: "gemma"}}
	ds := Propose(usefulItems(), proposer, judges, 1)
	if len(ds) != 1 {
		t.Fatalf("want 1 decision (only 'a' was worth proposing), got %d", len(ds))
	}
	if !ds[0].Approved || ds[0].Agrees != 1 {
		t.Fatalf("want approved with 1 agree, got %+v", ds[0])
	}
}

// A proposal the judge rejects is held back, not approved.
func TestProposeHeldWhenJudgeDisagrees(t *testing.T) {
	proposer := FakeProposer{ByID: map[string]Proposal{
		"a": {WorthBuilding: true, Slug: "becky-foo", Capability: "Do foo."},
	}}
	judges := []Judge{FakeJudge{JudgeName: "gemma", Reject: map[string]bool{"becky-foo": true}}}
	ds := Propose(usefulItems(), proposer, judges, 1)
	if len(ds) != 1 || ds[0].Approved {
		t.Fatalf("want 1 held-back decision, got %+v", ds)
	}
}

// minAgree=2 requires BOTH judges; one agreeing is not enough.
func TestProposeNeedsQuorum(t *testing.T) {
	proposer := FakeProposer{ByID: map[string]Proposal{
		"a": {WorthBuilding: true, Slug: "becky-foo", Capability: "Do foo."},
	}}
	judges := []Judge{
		FakeJudge{JudgeName: "gemma"},
		FakeJudge{JudgeName: "claude", Reject: map[string]bool{"becky-foo": true}},
	}
	ds := Propose(usefulItems(), proposer, judges, 2)
	if len(ds) != 1 || ds[0].Approved {
		t.Fatalf("want held back (only 1 of 2 agreed), got %+v", ds)
	}
	if ds[0].Agrees != 1 {
		t.Errorf("want 1 agree, got %d", ds[0].Agrees)
	}
}

// The proposer's own gate filters: worth_building=false yields no decision.
func TestProposeSkipsNotWorth(t *testing.T) {
	proposer := FakeProposer{ByID: map[string]Proposal{
		"a": {WorthBuilding: false, Slug: "becky-foo", Capability: "Do foo."},
	}}
	ds := Propose(usefulItems(), proposer, []Judge{FakeJudge{JudgeName: "g"}}, 1)
	if len(ds) != 0 {
		t.Fatalf("want no decisions when nothing is worth building, got %+v", ds)
	}
}

// No proposer or no judges → corroboration impossible → no decisions (never panics).
func TestProposeNoModels(t *testing.T) {
	if ds := Propose(usefulItems(), nil, nil, 1); ds != nil {
		t.Fatalf("want nil with no models, got %+v", ds)
	}
	proposer := FakeProposer{ByID: map[string]Proposal{"a": {WorthBuilding: true, Slug: "x", Capability: "y"}}}
	if ds := Propose(usefulItems(), proposer, nil, 1); ds != nil {
		t.Fatalf("want nil with no judges (corroboration impossible), got %+v", ds)
	}
}

// An approved Decision converts to a becky-new-tool intake with the right shape.
func TestToIntake(t *testing.T) {
	d := Decision{
		Video:    Video{URL: "https://youtu.be/a"},
		Proposal: Proposal{Slug: "becky-foo", Capability: "Do foo.", InputKind: "audio", Kind: "extend"},
		Approved: true,
	}
	in := d.ToIntake("2026-06-23")
	if in.Slug != "becky-foo" || in.InputKind != "audio" || in.OutputKind != "json" {
		t.Fatalf("intake shape wrong: %+v", in)
	}
	if in.Source != "https://youtu.be/a" || in.CapturedAt != "2026-06-23" {
		t.Errorf("provenance/date wrong: %+v", in)
	}
	if len(in.DefinitionOfDone) == 0 || in.DefinitionOfDone[0] != "go build ./cmd/foo passes" {
		t.Errorf("DoD wrong: %+v", in.DefinitionOfDone)
	}
}
