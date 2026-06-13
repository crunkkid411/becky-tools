package main

import (
	"strings"
	"testing"
)

func TestScoreFactsRecallAndAliases(t *testing.T) {
	facts := []Fact{
		{ID: "waist", Aliases: []string{"lateral waist", "iliac crest"}, Weight: 1, Category: "contact_region"},
		{ID: "thigh", Aliases: []string{"upper thigh", "lateral thigh"}, Weight: 1, Category: "contact_region"},
		{ID: "lookdown", Aliases: []string{"looks down", "downward gaze"}, Weight: 2, Category: "body_language"},
		{ID: "buttock", Aliases: []string{"gluteal", "buttock"}, Weight: 1, Category: "contact_region"},
	}
	// Output recalls waist + thigh + lookdown (weight 2) but NOT buttock.
	out := "Male hand on the lateral waist. Then the right hand moves to the upper thigh. The receiving person exhibits a downward gaze."
	hits, recall := scoreFacts(out, facts)
	if len(hits) != 4 {
		t.Fatalf("want 4 fact hits, got %d", len(hits))
	}
	// recalled weight = 1+1+2 = 4; total = 1+1+2+1 = 5 -> 0.8
	if recall != 0.8 {
		t.Errorf("weighted recall = %v, want 0.8", recall)
	}
	if hitCount(hits) != 3 {
		t.Errorf("unweighted hit count = %d, want 3", hitCount(hits))
	}
	for _, h := range hits {
		switch h.ID {
		case "waist":
			if !h.Hit || h.Matched != "lateral waist" {
				t.Errorf("waist should hit on 'lateral waist', got %+v", h)
			}
		case "buttock":
			if h.Hit {
				t.Errorf("buttock should miss (not in output), got %+v", h)
			}
		}
	}
}

func TestScoreFactsCaseInsensitive(t *testing.T) {
	facts := []Fact{{ID: "name", Aliases: []string{"Hair Jordan"}, Weight: 1}}
	_, recall := scoreFacts("...and that is it hair jordan is the person...", facts)
	if recall != 1.0 {
		t.Errorf("case-insensitive match should give recall 1.0, got %v", recall)
	}
}

func TestScoreFactsEmptyKey(t *testing.T) {
	hits, recall := scoreFacts("anything", nil)
	if len(hits) != 0 || recall != 0 {
		t.Errorf("empty answer key -> 0 hits, 0 recall; got %d hits, %v recall", len(hits), recall)
	}
}

func TestRankConfigsOrdersByRecallAndSplits(t *testing.T) {
	results := []CaseResult{
		{CaseID: "a", Config: "lo", Status: "ok", Recall: 0.2, Holdout: false},
		{CaseID: "b", Config: "lo", Status: "ok", Recall: 0.4, Holdout: false},
		{CaseID: "a", Config: "hi", Status: "ok", Recall: 0.8, Holdout: false},
		{CaseID: "b", Config: "hi", Status: "ok", Recall: 0.9, Holdout: false},
		// holdout rows must NOT enter the train ranking.
		{CaseID: "h", Config: "hi", Status: "ok", Recall: 0.7, Holdout: true},
		{CaseID: "h", Config: "lo", Status: "ok", Recall: 0.1, Holdout: true},
	}
	train := rankConfigs(results, false)
	if len(train) != 2 {
		t.Fatalf("want 2 ranked configs, got %d", len(train))
	}
	if train[0].Config != "hi" {
		t.Errorf("best train config should be 'hi', got %q", train[0].Config)
	}
	if train[0].MeanRecall != 0.85 { // (0.8+0.9)/2
		t.Errorf("hi mean recall = %v, want 0.85", train[0].MeanRecall)
	}
	if train[0].Cases != 2 {
		t.Errorf("hi should have 2 train cases, got %d", train[0].Cases)
	}
	hold := rankConfigs(results, true)
	if len(hold) != 2 || hold[0].Config != "hi" || hold[0].MeanRecall != 0.7 {
		t.Errorf("holdout ranking wrong: %+v", hold)
	}
}

func TestRankConfigsFailedCountsAsZeroRecall(t *testing.T) {
	results := []CaseResult{
		{CaseID: "a", Config: "x", Status: "ok", Recall: 1.0, Holdout: false},
		{CaseID: "b", Config: "x", Status: "failed", Holdout: false},
	}
	r := rankConfigs(results, false)
	if len(r) != 1 {
		t.Fatalf("want 1 config, got %d", len(r))
	}
	// (1.0 + 0)/2 = 0.5; a crash drags the mean down.
	if r[0].MeanRecall != 0.5 {
		t.Errorf("mean recall with one failure = %v, want 0.5", r[0].MeanRecall)
	}
	if r[0].Failed != 1 || r[0].OK != 1 {
		t.Errorf("ok/failed counts wrong: %+v", r[0])
	}
}

func TestReadOutputTextFlattensJSON(t *testing.T) {
	js := `{"observations":[{"visual":"hand on lateral waist","finding":"downward gaze"}],"note":"upbeat"}`
	got := readOutputText("/nonexistent/path", js)
	for _, want := range []string{"lateral waist", "downward gaze", "upbeat"} {
		if !strings.Contains(got, want) {
			t.Errorf("flattened output missing %q\n%s", want, got)
		}
	}
}

func TestReadOutputTextNonJSONPassthrough(t *testing.T) {
	got := readOutputText("/nonexistent/path", "plain transcript text hair jordan 6,000 people")
	if !strings.Contains(got, "hair jordan") {
		t.Errorf("non-JSON output should pass through: %q", got)
	}
}
