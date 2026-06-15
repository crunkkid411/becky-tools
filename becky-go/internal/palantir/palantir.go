// palantir.go — the orchestrator. It ties the three SPEC steps together:
//
//	A PREPARE   : Ingest becky outputs -> flat Observations (ingest.go)
//	B DRIVE     : optional, opt-in GraphEnricher (enrich.go) — OFF by default
//	C NORMALIZE : schema + corroborate-then-conclude + provenance (normalize.go)
//
// The DEFAULT engine is the deterministic, offline cooccur-only floor (cooccur.go).
// Selecting the openplanter engine attempts enrichment; if the enricher is
// unavailable (the cloud/CI case, or a missing binary), Build DEGRADES to the floor
// at exit 0 with a plain-language note — it never crashes and never emits half a graph.
package palantir

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Engine names (the --engine flag values).
const (
	EngineCooccurOnly = "cooccur-only"
	EngineOpenPlanter = "openplanter"
)

// ToolVersion is the version string stamped into every graph.
const ToolVersion = "becky-palantir v1.0.0"

// Options controls one Build. Zero values are safe: empty Engine => cooccur-only,
// EdgeConclude <1 => the default of 2.
type Options struct {
	Engine       string
	EdgeConclude int
	Enrich       bool // allow the enricher to web-search (logged); requires openplanter
	Enricher     GraphEnricher
	EnrichOpts   EnrichOptions
	CorpusRoot   string
	Seed         int
}

// Build runs the full pipeline over a prepared Ingest and returns the deterministic
// becky graph. It is the single entry point the CLI and tests share.
func Build(in Ingest, opts Options) Graph {
	in.Sort()
	g := newGraph(in, opts)

	nodes, edges, degraded, note := buildByEngine(in, opts)
	if degraded {
		g.Degraded = true
		g.Engine = EngineCooccurOnly
		g.Determinism.Reasoning = "deterministic"
		addNote(&g, "degrade", note)
	}

	nodes, edges, dropNotes := Normalize(nodes, edges, opts.EdgeConclude)
	g.Nodes = nodes
	g.Edges = edges
	g.Summary = BuildSummary(edges)
	g.Summary.TopFindings = ensureFindings(g.Summary.TopFindings)
	for i, n := range dropNotes {
		addNote(&g, fmt.Sprintf("dropped_%d", i), n)
	}
	for i, n := range in.Notes {
		addNote(&g, fmt.Sprintf("ingest_%d", i), n)
	}
	return g
}

// buildByEngine produces the raw nodes/edges for the selected engine, signalling a
// degrade (and the reason) when an opt-in engine could not run.
func buildByEngine(in Ingest, opts Options) (nodes []Node, edges []Edge, degraded bool, note string) {
	if opts.Engine != EngineOpenPlanter {
		n, e := BuildCooccur(in)
		return n, e, false, ""
	}
	if opts.Enricher == nil {
		n, e := BuildCooccur(in)
		return n, e, true, "no enrichment engine configured; used the deterministic cooccur-only floor."
	}
	raw, err := opts.Enricher.Enrich(opts.EnrichOpts)
	if err != nil {
		n, e := BuildCooccur(in)
		reason := "the enrichment engine could not run"
		if errors.Is(err, ErrEnricherUnavailable) {
			reason = "the OpenPlanter engine is not available on this machine"
		}
		return n, e, true, reason + "; used the deterministic cooccur-only floor instead."
	}
	n, e := RawToGraph(raw, in)
	return n, e, false, ""
}

// RawToGraph turns an engine's RawGraph into becky nodes/edges, re-attaching full
// becky provenance from the resolved evidence rows. An edge whose evidence_row_ids
// don't resolve to real observations is DROPPED (hallucination guard, SPEC §10) —
// the normalizer's note records it. becky, not the LLM, owns the final status.
func RawToGraph(raw RawGraph, in Ingest) ([]Node, []Edge) {
	byRow := indexByRow(in.Observations)
	nodes := rawNodes(raw, byRow)
	edges := rawEdges(raw, byRow)
	return nodes, edges
}

// indexByRow maps row_id -> Observation for provenance reattachment.
func indexByRow(obs []Observation) map[string]Observation {
	m := make(map[string]Observation, len(obs))
	for _, o := range obs {
		m[o.RowID] = o
	}
	return m
}

// rawNodes builds becky nodes from engine nodes, re-attaching provenance from any
// observation that shares the node id (engine ids are seeded from becky ids).
func rawNodes(raw RawGraph, byRow map[string]Observation) []Node {
	out := make([]Node, 0, len(raw.Nodes))
	for _, rn := range raw.Nodes {
		prov, files, appearances := nodeProvenance(rn.NodeID, byRow)
		out = append(out, Node{
			NodeID:              rn.NodeID,
			Kind:                rn.Kind,
			Label:               rn.Label,
			Status:              StatusCandidate,
			Aliases:             dedupeSorted(rn.Aliases),
			Appearances:         appearances,
			DistinctSourceFiles: len(files),
			Provenance:          prov,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}

// nodeProvenance gathers the becky provenance for a node id from any observation
// that shares it, so every node is traceable. Iteration over the row map is sorted
// at the end (sortProvenance), so the result is order-independent.
func nodeProvenance(nodeID string, byRow map[string]Observation) ([]Provenance, map[string]bool, int) {
	var prov []Provenance
	files := map[string]bool{}
	count := 0
	for _, o := range byRow {
		if o.NodeID == nodeID {
			prov = append(prov, provFromObs(o))
			if o.SourceFile != "" {
				files[o.SourceFile] = true
			}
			count++
		}
	}
	return sortProvenance(prov), files, count
}

// rawEdges builds becky edges from engine edges, resolving each edge's
// evidence_row_ids to real observations. Edges with no resolvable evidence are
// dropped (empty provenance => normalizer drops them with a note).
func rawEdges(raw RawGraph, byRow map[string]Observation) []Edge {
	out := make([]Edge, 0, len(raw.Edges))
	for _, re := range raw.Edges {
		prov, signals, weight := resolveEvidence(re.EvidenceRowIDs, byRow)
		if len(prov) == 0 {
			continue // hallucinated edge: no real evidence -> dropped
		}
		out = append(out, Edge{
			EdgeID:               edgeKey(re.Kind, re.Source, re.Target, isDirected(re.Kind)),
			Kind:                 re.Kind,
			Source:               re.Source,
			Target:               re.Target,
			Directed:             isDirected(re.Kind),
			Weight:               weight,
			Confidence:           re.Confidence, // advisory; normalizer recomputes
			CorroboratingSignals: signals,
			Provenance:           prov,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].EdgeID < out[j].EdgeID })
	return out
}

// resolveEvidence turns evidence_row_ids into provenance + per-family signal counts
// + a weight (distinct supporting clips). Unknown row ids are skipped.
func resolveEvidence(rowIDs []string, byRow map[string]Observation) ([]Provenance, []Signal, int) {
	var prov []Provenance
	clips := map[string]bool{}
	fams := map[string]*Signal{}
	for _, id := range rowIDs {
		o, ok := byRow[id]
		if !ok {
			continue
		}
		prov = append(prov, provFromObs(o))
		if o.SourceFile != "" {
			clips[o.SourceFile] = true
		}
		s := fams[o.Signal]
		if s == nil {
			s = &Signal{Signal: o.Signal, From: o.From}
			fams[o.Signal] = s
		}
		s.Count++
	}
	return sortProvenance(prov), sortedSignals(fams), len(clips)
}

func isDirected(kind string) bool {
	switch kind {
	case EdgeCoOccurrence, EdgeContact:
		return false
	default:
		return true
	}
}

// newGraph builds the graph envelope (header + determinism + scope notes) before
// nodes/edges are filled in.
func newGraph(in Ingest, opts Options) Graph {
	engine := opts.Engine
	if engine == "" {
		engine = EngineCooccurOnly
	}
	reasoning := "deterministic"
	if engine == EngineOpenPlanter {
		reasoning = "non-deterministic"
	}
	rows := len(in.Observations)
	return Graph{
		Tool:   ToolVersion,
		Engine: engine,
		Enrichment: Enrichment{
			WebSearch: opts.Enrich,
			Engine:    engine,
			Note:      enrichmentNote(opts.Enrich),
		},
		Corpus: CorpusInfo{Root: opts.CorpusRoot, FilesIngested: in.FilesIngested, EvidenceRows: rows},
		Determinism: Determinism{
			InputSHA256:  InputHash(in.Observations),
			OutputFormat: "deterministic",
			Reasoning:    reasoning,
			Seed:         opts.Seed,
		},
		Notes: map[string]string{
			"honesty": "documented edges are corroborated conclusions; candidate edges are single-signal leads for human review, never asserted as fact.",
			"scope":   scopeNote(opts.Enrich),
		},
	}
}

func enrichmentNote(web bool) string {
	if web {
		return "web enrichment ENABLED; web-derived facts carry a from: web:<url> provenance and are flagged."
	}
	return "local corpus only; no network used."
}

func scopeNote(web bool) string {
	base := "built only from Jordan's own becky evidence outputs"
	if web {
		return base + "; web enrichment was enabled (--enrich) and web-derived links are flagged separately."
	}
	return base + "; no third-party or network data (--enrich was not set)."
}

// InputHash is the SHA-256 of the canonical-JSON of the sorted observations. It
// makes a corpus reproducible: the same evidence yields the same hash, which keys
// the (local-agent) cache replay so an LLM run is reproducible in spirit.
func InputHash(obs []Observation) string {
	sorted := append([]Observation{}, obs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].RowID < sorted[j].RowID })
	b, err := json.Marshal(sorted)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func addNote(g *Graph, key, val string) {
	if g.Notes == nil {
		g.Notes = map[string]string{}
	}
	g.Notes[key] = val
}

func ensureFindings(f []string) []string {
	if f == nil {
		return []string{"No corroborated associations found — all links are single-signal candidates for review."}
	}
	return f
}
