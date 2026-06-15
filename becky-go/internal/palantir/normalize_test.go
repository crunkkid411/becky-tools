package palantir

import (
	"strings"
	"testing"
)

func nodeWithProv(id, kind string) Node {
	return Node{NodeID: id, Kind: kind, Label: id, Provenance: []Provenance{{SourceFile: "c1.mp4", From: "identify.json"}}}
}

func edgeWith(id, kind, src, tgt string, sigs []Signal) Edge {
	return Edge{EdgeID: id, Kind: kind, Source: src, Target: tgt,
		CorroboratingSignals: sigs, Weight: 2,
		Provenance: []Provenance{{SourceFile: "c1.mp4", From: "identify.json"}}}
}

func TestNormalize_concludeRule(t *testing.T) {
	nodes := []Node{nodeWithProv("person:a", KindPerson), nodeWithProv("person:b", KindPerson)}
	cases := []struct {
		name      string
		sigs      []Signal
		concludeN int
		want      string
	}{
		{"two distinct families -> documented",
			[]Signal{{Signal: "same-clip", Count: 9}, {Signal: "same-room", Count: 2}}, 2, StatusDocumented},
		{"one family -> candidate",
			[]Signal{{Signal: "same-clip", Count: 9}}, 2, StatusCandidate},
		{"two of the SAME family is still one family -> candidate",
			[]Signal{{Signal: "same-clip", Count: 1}, {Signal: "same-clip", Count: 1}}, 2, StatusCandidate},
		{"threshold of 1 lets a single family conclude",
			[]Signal{{Signal: "same-clip", Count: 3}}, 1, StatusDocumented},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			edges := []Edge{edgeWith("e", EdgeCoOccurrence, "person:a", "person:b", c.sigs)}
			_, out, _ := Normalize(nodes, edges, c.concludeN)
			if len(out) != 1 {
				t.Fatalf("want 1 edge, got %d", len(out))
			}
			if out[0].Status != c.want {
				t.Errorf("status = %q, want %q", out[0].Status, c.want)
			}
			if out[0].Summary == "" {
				t.Error("summary must be plain-language, not empty")
			}
		})
	}
}

func TestNormalize_dropsHallucinatedEdge(t *testing.T) {
	nodes := []Node{nodeWithProv("person:a", KindPerson)} // person:ghost is NOT a node
	edges := []Edge{edgeWith("e", EdgeCoOccurrence, "person:a", "person:ghost",
		[]Signal{{Signal: "same-clip", Count: 1}})}
	_, out, notes := Normalize(nodes, edges, 2)
	if len(out) != 0 {
		t.Fatalf("hallucinated edge survived: %+v", out)
	}
	if !containsSub(notes, "hallucination guard") {
		t.Errorf("expected a hallucination-guard note, got %v", notes)
	}
}

func TestNormalize_dropsOutOfSchemaKinds(t *testing.T) {
	nodes := []Node{nodeWithProv("person:a", KindPerson), nodeWithProv("alien:x", "alien")}
	edges := []Edge{
		edgeWith("good", EdgeCoOccurrence, "person:a", "person:a", []Signal{{Signal: "s", Count: 1}}),
		edgeWith("bad", "telepathy", "person:a", "person:a", []Signal{{Signal: "s", Count: 1}}),
	}
	outNodes, outEdges, notes := Normalize(nodes, edges, 2)
	for _, n := range outNodes {
		if n.Kind == "alien" {
			t.Error("out-of-schema node kept")
		}
	}
	for _, e := range outEdges {
		if e.Kind == "telepathy" {
			t.Error("out-of-schema edge kept")
		}
	}
	if !containsSub(notes, "closed schema") {
		t.Errorf("expected closed-schema drop notes, got %v", notes)
	}
}

func TestNormalize_everyEdgeHasProvenance(t *testing.T) {
	nodes := []Node{nodeWithProv("person:a", KindPerson), nodeWithProv("person:b", KindPerson)}
	edges := []Edge{edgeWith("e", EdgeCoOccurrence, "person:a", "person:b",
		[]Signal{{Signal: "x", Count: 2}, {Signal: "y", Count: 1}})}
	_, out, _ := Normalize(nodes, edges, 2)
	for _, e := range out {
		if len(e.Provenance) == 0 {
			t.Errorf("edge %q has no provenance — an untraceable finding is a bug", e.EdgeID)
		}
	}
}

func TestBuildSummary_countsAndTopFindings(t *testing.T) {
	edges := []Edge{
		{EdgeID: "a", Status: StatusDocumented, Confidence: 0.9, Summary: "A"},
		{EdgeID: "b", Status: StatusDocumented, Confidence: 0.6, Summary: "B"},
		{EdgeID: "c", Status: StatusCandidate, Confidence: 0.4, Summary: "C"},
	}
	s := BuildSummary(edges)
	if s.DocumentedEdges != 2 || s.CandidateEdges != 1 {
		t.Errorf("counts = %d documented / %d candidate", s.DocumentedEdges, s.CandidateEdges)
	}
	if len(s.TopFindings) == 0 || s.TopFindings[0] != "A" {
		t.Errorf("top finding should be the highest-confidence documented edge, got %v", s.TopFindings)
	}
}

func containsSub(notes []string, sub string) bool {
	for _, n := range notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}
