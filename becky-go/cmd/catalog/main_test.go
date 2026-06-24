package main

import (
	"encoding/json"
	"testing"
)

func find(d doc, verb string) (entry, bool) {
	for _, e := range d.Tools {
		if e.Verb == verb {
			return e, true
		}
	}
	return entry{}, false
}

// TestBuildDoc_KnownToolsAndTiers asserts VALUES the GUI depends on: the list is
// non-empty, known tools are present with the right tier, and every tool has a
// resolved (never-empty) tier + non-nil keywords.
func TestBuildDoc_KnownToolsAndTiers(t *testing.T) {
	d := buildDoc()
	if len(d.Tools) == 0 {
		t.Fatal("Tools is empty — the GUI would show nothing")
	}

	tr, ok := find(d, "becky-transcribe")
	if !ok {
		t.Fatal("becky-transcribe missing from Tools")
	}
	if tr.Tier != "green" {
		t.Errorf("becky-transcribe tier = %q, want green", tr.Tier)
	}

	ex, ok := find(d, "becky-export")
	if !ok {
		t.Fatal("becky-export missing from Tools")
	}
	if ex.Tier != "red" {
		t.Errorf("becky-export tier = %q, want red (it leaves the machine)", ex.Tier)
	}

	for _, e := range d.Tools {
		if e.Verb == "" || e.Summary == "" {
			t.Errorf("tool with empty verb/summary: %+v", e)
		}
		switch e.Tier {
		case "green", "yellow", "red":
		default:
			t.Errorf("tool %s has unresolved tier %q (must be green/yellow/red)", e.Verb, e.Tier)
		}
		if e.Keywords == nil {
			t.Errorf("tool %s has nil keywords; want [] so the JSON is stable", e.Verb)
		}
	}
}

// TestBuildDoc_RoundTripsAsJSON asserts the document marshals and parses back intact —
// the exact contract the WPF window relies on.
func TestBuildDoc_RoundTripsAsJSON(t *testing.T) {
	d := buildDoc()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var rt doc
	if err := json.Unmarshal(b, &rt); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	if len(rt.Tools) != len(d.Tools) {
		t.Errorf("round-trip tools = %d, want %d", len(rt.Tools), len(d.Tools))
	}
	if len(rt.Ops) != len(d.Ops) {
		t.Errorf("round-trip ops = %d, want %d", len(rt.Ops), len(d.Ops))
	}
}
