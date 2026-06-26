package main

import (
	"encoding/json"
	"testing"

	"becky-go/internal/orchestrate"
)

// a REAL-shaped becky-identify output: one corroborated name, one single-modality match, and
// one demoted candidate (matches cmd/identify's JSON contract).
const identifyFixture = `{
  "file": "case.mp4",
  "identifications": [
    {"type":"corroborated","name":"Shelby","confidence":0.88,"corroborated_by":["voice","face"]},
    {"type":"voice","name":"John","confidence":0.71}
  ],
  "unidentified": [
    {"type":"voice","candidate":"Marcus","candidate_confidence":0.62,"why_unnamed":"below-name-threshold"}
  ]
}`

func parseFixture(t *testing.T) idOutput {
	t.Helper()
	var o idOutput
	if err := json.Unmarshal([]byte(identifyFixture), &o); err != nil {
		t.Fatalf("fixture parse: %v", err)
	}
	return o
}

// TestMapping_CorroboratedConcludes: a corroborated identification (voice+face) becomes a claim
// with two independent sources and therefore CONCLUDES; a single-modality match does not.
func TestMapping_CorroboratedConcludes(t *testing.T) {
	claims := claimsFromIdentify(parseFixture(t))
	got := map[string]orchestrate.Status{}
	for _, c := range claims {
		got[c.Key] = orchestrate.Corroborate(c, orchestrate.DefaultRules()).Status
	}
	if got["person=Shelby"] != orchestrate.Concluded {
		t.Errorf("Shelby (voice+face) => %s, want concluded", got["person=Shelby"])
	}
	if got["person=John"] != orchestrate.Candidate {
		t.Errorf("John (single voice match) => %s, want candidate (one signal is not a name)", got["person=John"])
	}
	if got["person=Marcus"] != orchestrate.Candidate {
		t.Errorf("Marcus (demoted candidate) => %s, want candidate", got["person=Marcus"])
	}
}

// fakeLadder corroborates exactly one named claim at level 1 (a stand-in for Gemma-4 agreeing).
type fakeLadder struct{ agreesWith string }

func (f fakeLadder) Validate(c orchestrate.Claim, level int) (orchestrate.Signal, error) {
	if c.Key == f.agreesWith {
		return orchestrate.Signal{Source: "gemma4-e4b", Kind: orchestrate.KindPrint, Confidence: 0.8}, nil
	}
	return orchestrate.Signal{}, errStub
}

var errStub = &stubErr{}

type stubErr struct{}

func (*stubErr) Error() string { return "model did not corroborate" }

// TestResolve_EscalationNamesACandidate: the self-regulation that identify lacks — a candidate
// is escalated and, when the model corroborates it, BECOMES a concluded name; one that the model
// can't corroborate stays held (never falsely named).
func TestResolve_EscalationNamesACandidate(t *testing.T) {
	claims := claimsFromIdentify(parseFixture(t))
	res := orchestrate.Resolve(claims, orchestrate.DefaultRules(), fakeLadder{agreesWith: "person=John"}, 2)

	concluded := map[string]bool{}
	for _, v := range res.Concluded {
		concluded[v.Claim] = true
	}
	if !concluded["person=Shelby"] {
		t.Errorf("Shelby should be concluded (already corroborated)")
	}
	if !concluded["person=John"] {
		t.Errorf("John should be concluded after the model corroborated the escalation")
	}
	// Marcus was NOT corroborated by the model -> must stay a held candidate, never named.
	for _, v := range res.Concluded {
		if v.Claim == "person=Marcus" {
			t.Errorf("Marcus was named without corroboration — protocol violated")
		}
	}
	stillCandidate := false
	for _, v := range res.Candidates {
		if v.Claim == "person=Marcus" {
			stillCandidate = true
		}
	}
	if !stillCandidate {
		t.Errorf("Marcus should remain a held candidate, got %+v", res)
	}
}
