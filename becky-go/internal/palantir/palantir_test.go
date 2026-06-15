package palantir

import (
	"errors"
	"fmt"
	"testing"
)

// fakeEnricher is a synthetic GraphEnricher for the openplanter path — it never
// touches the network or a binary, so the orchestrator can be tested end to end.
type fakeEnricher struct {
	raw RawGraph
	err error
}

func (f fakeEnricher) Name() string { return "openplanter" }
func (f fakeEnricher) Enrich(EnrichOptions) (RawGraph, error) {
	return f.raw, f.err
}

func ingestTwoSharedClips() Ingest {
	in := Ingest{FilesIngested: 2}
	for _, c := range []string{"c1.mp4", "c2.mp4"} {
		in.Observations = append(in.Observations,
			Observation{RowID: c + ":john", Kind: KindPerson, NodeID: "person:john", Label: "John",
				Status: StatusDocumented, SourceFile: c, Signal: "face", From: "identify.json"},
			Observation{RowID: c + ":a", Kind: KindPerson, NodeID: "person:a", Label: "Person A",
				Status: StatusCandidate, SourceFile: c, Signal: "face-cluster", From: "cluster.json"},
		)
	}
	return in
}

func TestBuild_cooccurDefaultIsDeterministicAndOffline(t *testing.T) {
	g := Build(ingestTwoSharedClips(), Options{Engine: EngineCooccurOnly})
	if g.Engine != EngineCooccurOnly {
		t.Errorf("engine = %q", g.Engine)
	}
	if g.Determinism.Reasoning != "deterministic" {
		t.Errorf("reasoning = %q, want deterministic", g.Determinism.Reasoning)
	}
	if g.Enrichment.WebSearch {
		t.Error("default must not enable web search")
	}
	if g.Determinism.InputSHA256 == "" {
		t.Error("input hash must be set")
	}
	g2 := Build(ingestTwoSharedClips(), Options{Engine: EngineCooccurOnly})
	if g.Determinism.InputSHA256 != g2.Determinism.InputSHA256 {
		t.Error("input hash not stable across identical inputs")
	}
}

func TestBuild_openplanterMissingDegradesToFloor(t *testing.T) {
	g := Build(ingestTwoSharedClips(), Options{
		Engine:   EngineOpenPlanter,
		Enricher: fakeEnricher{err: fmt.Errorf("nope: %w", ErrEnricherUnavailable)},
	})
	if !g.Degraded {
		t.Fatal("a missing engine must degrade")
	}
	if g.Engine != EngineCooccurOnly {
		t.Errorf("degraded engine = %q, want cooccur-only", g.Engine)
	}
	if g.Notes["degrade"] == "" {
		t.Error("degrade must carry a plain-language note")
	}
	if len(g.Edges) == 0 {
		t.Error("degrade should still yield the deterministic floor graph")
	}
}

func TestBuild_openplanterReattachesProvenanceAndDropsHallucinations(t *testing.T) {
	in := ingestTwoSharedClips()
	raw := RawGraph{
		Nodes: []RawNode{
			{NodeID: "person:john", Kind: KindPerson, Label: "John"},
			{NodeID: "person:a", Kind: KindPerson, Label: "Person A"},
		},
		Edges: []RawEdge{
			// Real edge: evidence rows from two signal families across 2 clips.
			{Kind: EdgeCoOccurrence, Source: "person:john", Target: "person:a",
				Confidence: 0.9, EvidenceRowIDs: []string{"c1.mp4:john", "c1.mp4:a", "c2.mp4:john", "c2.mp4:a"}},
			// Hallucinated edge: evidence row id does not exist -> must be dropped.
			{Kind: EdgeCoOccurrence, Source: "person:john", Target: "person:a",
				Confidence: 0.99, EvidenceRowIDs: []string{"does-not-exist"}},
		},
	}
	g := Build(in, Options{Engine: EngineOpenPlanter, Enricher: fakeEnricher{raw: raw}})
	if g.Degraded {
		t.Fatal("a successful enrich should not degrade")
	}
	if len(g.Edges) != 1 {
		t.Fatalf("want exactly 1 edge after hallucination drop, got %d: %+v", len(g.Edges), g.Edges)
	}
	e := g.Edges[0]
	if len(e.Provenance) == 0 {
		t.Error("edge provenance must be re-attached from resolved becky rows")
	}
	if e.Status != StatusDocumented {
		t.Errorf("status = %q, want documented (two independent families)", e.Status)
	}
}

func TestBuild_emptyCorpusDegradesGracefully(t *testing.T) {
	g := Build(Ingest{Notes: []string{"no becky evidence JSON found"}}, Options{})
	if len(g.Nodes) != 0 || len(g.Edges) != 0 {
		t.Error("empty corpus should yield an empty graph, not invent data")
	}
	if len(g.Summary.TopFindings) == 0 {
		t.Error("summary should still carry a plain-language no-findings line")
	}
}

func TestInputHash_orderIndependent(t *testing.T) {
	in := ingestTwoSharedClips()
	rev := make([]Observation, len(in.Observations))
	for i, o := range in.Observations {
		rev[len(in.Observations)-1-i] = o
	}
	if InputHash(in.Observations) != InputHash(rev) {
		t.Error("InputHash must be independent of observation order")
	}
}

func TestErrEnricherUnavailable_isUnwrappable(t *testing.T) {
	wrapped := fmt.Errorf("driver: %w", ErrEnricherUnavailable)
	if !errors.Is(wrapped, ErrEnricherUnavailable) {
		t.Error("ErrEnricherUnavailable must be unwrappable for the degrade path")
	}
}
