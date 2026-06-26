package main

import "testing"

const idJSON = `{"identifications":[
  {"type":"corroborated","name":"Shelby","confidence":0.88,"corroborated_by":["voice","face"]},
  {"type":"voice","name":"John","confidence":0.71}
]}`

const trJSON = `{"segments":[{"start":10,"end":11,"text":"the cat is here"}]}`
const moJSON = `{"motion_bursts":[{"window_start":10,"window_end":12}]}`
const vaJSON = `{"observations":[{"segment_start":10,"segment_end":13,"visual":"a cat on the floor","finding":"cat present","confidence":0.86}]}`

// TestPlan_DiarizeIsConditional: the deterministic plan skips diarize for one speaker and
// includes it for several — the diarize-conditional protocol, decided in code.
func TestPlan_DiarizeIsConditional(t *testing.T) {
	has := func(steps []string, name string) bool {
		for _, s := range steps {
			if s == name {
				return true
			}
		}
		return false
	}
	if has(plan(1), "becky-diarize") {
		t.Errorf("one speaker: plan must NOT include diarize, got %v", plan(1))
	}
	if !has(plan(3), "becky-diarize") {
		t.Errorf("three speakers: plan MUST include diarize, got %v", plan(3))
	}
}

// TestReport_OneCorroboratedOutput: the complete behavior — one call yields names only when
// corroborated, on-screen only where watched, and everything else held (never dumped).
func TestReport_OneCorroboratedOutput(t *testing.T) {
	rep := report("case.mp4", "cat", 1,
		[]byte(idJSON), []byte(trJSON), []byte(moJSON), []byte(vaJSON))

	// plan: one speaker -> no diarize
	for _, s := range rep.Plan {
		if s == "becky-diarize" {
			t.Errorf("one speaker plan should not diarize: %v", rep.Plan)
		}
	}
	// names: Shelby corroborated -> stated; John single -> held, not stated
	named := map[string]bool{}
	for _, v := range rep.Names {
		named[v.Claim] = true
	}
	if !named["person=Shelby"] {
		t.Errorf("Shelby (voice+face) should be a stated name, got %+v", rep.Names)
	}
	if named["person=John"] {
		t.Errorf("John (single signal) must NOT be stated as a name")
	}
	// on-screen: the cat was watched -> stated
	onscreen := false
	for _, v := range rep.OnScreen {
		if v.Claim == "onscreen=cat@[10.0-13.0]" {
			onscreen = true
		}
	}
	if !onscreen {
		t.Errorf("cat was watched -> should be a stated on-screen interval, got %+v", rep.OnScreen)
	}
	// John must appear among held (corroborate-or-hold, never a flood-of-maybes in Names)
	heldJohn := false
	for _, v := range rep.Held {
		if v.Claim == "person=John" {
			heldJohn = true
		}
	}
	if !heldJohn {
		t.Errorf("John should be a HELD candidate, got held=%+v", rep.Held)
	}
}
