// cooccur.go — the deterministic, pure-Go, NO-NETWORK floor (engine
// "cooccur-only"). It is BOTH the degrade target when OpenPlanter is missing/broken
// AND a first-class, audit-clean engine Jordan can pick.
//
// Rule (SPEC §7.3): emit an edge for every pair of entities that share a clip
// (source_file). Person↔Person within a clip → co_occurrence; entity↔place sharing
// GPS/place → location; entity↔device sharing EXIF → device; entity↔event sharing
// a clip → timeline. Edge WEIGHT is the number of distinct clips supporting the
// pair (stable, reproducible). Status gating (corroborate-then-conclude) is applied
// later by the normalizer; here we only build the raw graph deterministically.
package palantir

import (
	"fmt"
	"sort"
)

// BuildCooccur builds the deterministic co-occurrence graph from ingested
// observations. Output nodes and edges are fully sorted so the JSON is byte-stable.
func BuildCooccur(in Ingest) ([]Node, []Edge) {
	nodes := buildNodes(in.Observations)
	edges := buildEdges(in.Observations)
	return nodes, edges
}

// nodeAccum collects the per-node facts needed to emit a Node deterministically.
type nodeAccum struct {
	node    Node
	files   map[string]bool
	aliases []string
}

// buildNodes collapses observations into one Node per node_id, counting
// appearances and distinct source files and collecting provenance + aliases.
func buildNodes(obs []Observation) []Node {
	acc := map[string]*nodeAccum{}
	order := []string{}
	for _, o := range obs {
		a := acc[o.NodeID]
		if a == nil {
			a = &nodeAccum{
				node:  Node{NodeID: o.NodeID, Kind: o.Kind, Label: o.Label, Status: o.Status},
				files: map[string]bool{},
			}
			acc[o.NodeID] = a
			order = append(order, o.NodeID)
		}
		a.node.Appearances++
		if o.SourceFile != "" {
			a.files[o.SourceFile] = true
		}
		a.aliases = append(a.aliases, o.Aliases...)
		// A documented sighting upgrades an entity that started as a candidate.
		if o.Status == StatusDocumented {
			a.node.Status = StatusDocumented
		}
		a.node.Provenance = append(a.node.Provenance, provFromObs(o))
	}
	out := make([]Node, 0, len(order))
	for _, id := range order {
		a := acc[id]
		a.node.DistinctSourceFiles = len(a.files)
		a.node.Aliases = dedupeSorted(a.aliases)
		out = append(out, a.node)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}

// edgeAccum collects the per-edge facts: which clips support it, per-signal-family
// counts, and the provenance rows.
type edgeAccum struct {
	kind       string
	source     string
	target     string
	directed   bool
	clips      map[string]bool
	signals    map[string]*Signal
	provenance []Provenance
}

// buildEdges produces one edge per related node pair, weighted by distinct
// supporting clips. Edges are grouped per source_file so a "clip" is the unit of
// co-occurrence (the cheapest, most defensible association signal).
func buildEdges(obs []Observation) []Edge {
	byClip := groupByClip(obs)
	acc := map[string]*edgeAccum{}
	clips := sortedKeys(byClip)
	for _, clip := range clips {
		addClipEdges(acc, clip, byClip[clip])
	}
	return finalizeEdges(acc)
}

// groupByClip buckets observations by source_file.
func groupByClip(obs []Observation) map[string][]Observation {
	out := map[string][]Observation{}
	for _, o := range obs {
		if o.SourceFile == "" {
			continue
		}
		out[o.SourceFile] = append(out[o.SourceFile], o)
	}
	return out
}

// addClipEdges adds, for one clip, the cross-kind edges the floor recognizes.
func addClipEdges(acc map[string]*edgeAccum, clip string, rows []Observation) {
	persons := filterKind(rows, KindPerson)
	places := filterKind(rows, KindPlace)
	devices := filterKind(rows, KindDevice)
	events := filterKind(rows, KindEvent)

	addPersonPairs(acc, clip, persons)
	addCross(acc, clip, persons, places, EdgeLocation, true)
	addCross(acc, clip, persons, devices, EdgeDevice, true)
	addCross(acc, clip, persons, events, EdgeTimeline, true)
}

// addPersonPairs adds an undirected co_occurrence edge for every distinct pair of
// persons sharing the clip.
func addPersonPairs(acc map[string]*edgeAccum, clip string, persons []Observation) {
	ids := distinctNodeOrder(persons)
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			bumpEdge(acc, EdgeCoOccurrence, ids[i], ids[j], false, clip,
				"same-clip co-appearance", pickProv(persons, ids[i], clip))
		}
	}
}

// addCross adds a directed edge from each person to each entity of the other kind
// sharing the clip (person→place, person→device, person→event).
func addCross(acc map[string]*edgeAccum, clip string, persons, others []Observation, kind string, directed bool) {
	if len(persons) == 0 || len(others) == 0 {
		return
	}
	pIDs := distinctNodeOrder(persons)
	oIDs := distinctNodeOrder(others)
	signal := crossSignal(kind)
	for _, p := range pIDs {
		for _, o := range oIDs {
			bumpEdge(acc, kind, p, o, directed, clip, signal, pickProv(others, o, clip))
		}
	}
}

// bumpEdge records one supporting clip for the (kind, a, b) edge, accumulating the
// signal-family count and provenance. Undirected edges share an order-independent
// id; directed edges keep source→target as given.
func bumpEdge(acc map[string]*edgeAccum, kind, a, b string, directed bool, clip, signal string, prov Provenance) {
	id := edgeKey(kind, a, b, directed)
	e := acc[id]
	if e == nil {
		src, tgt := a, b
		if !directed && b < a {
			src, tgt = b, a
		}
		e = &edgeAccum{kind: kind, source: src, target: tgt, directed: directed,
			clips: map[string]bool{}, signals: map[string]*Signal{}}
		acc[id] = e
	}
	if !e.clips[clip] {
		e.clips[clip] = true
		e.provenance = append(e.provenance, prov)
	}
	sig := e.signals[signal]
	if sig == nil {
		sig = &Signal{Signal: signal, From: prov.From}
		e.signals[signal] = sig
	}
	sig.Count++
}

// finalizeEdges turns the accumulators into sorted Edges with stable weights. The
// normalizer sets status/summary/confidence afterward.
func finalizeEdges(acc map[string]*edgeAccum) []Edge {
	out := make([]Edge, 0, len(acc))
	for _, e := range acc {
		out = append(out, Edge{
			EdgeID:               edgeKey(e.kind, e.source, e.target, e.directed),
			Kind:                 e.kind,
			Source:               e.source,
			Target:               e.target,
			Directed:             e.directed,
			Weight:               len(e.clips),
			CorroboratingSignals: sortedSignals(e.signals),
			Provenance:           sortProvenance(e.provenance),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].EdgeID < out[j].EdgeID })
	return out
}

// edgeKey returns the canonical id for an edge (order-independent when undirected).
func edgeKey(kind, a, b string, directed bool) string {
	if directed {
		return fmt.Sprintf("%s:%s->%s", kind, a, b)
	}
	return pairEdgeID(kind, a, b)
}

func crossSignal(kind string) string {
	switch kind {
	case EdgeLocation:
		return "shared-clip-place"
	case EdgeDevice:
		return "shared-clip-device"
	case EdgeTimeline:
		return "shared-clip-event"
	}
	return "shared-clip"
}

func filterKind(rows []Observation, kind string) []Observation {
	var out []Observation
	for _, r := range rows {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

// distinctNodeOrder returns the distinct node ids present, in sorted order, so
// pair enumeration is deterministic.
func distinctNodeOrder(rows []Observation) []string {
	seen := map[string]bool{}
	var ids []string
	for _, r := range rows {
		if !seen[r.NodeID] {
			seen[r.NodeID] = true
			ids = append(ids, r.NodeID)
		}
	}
	sort.Strings(ids)
	return ids
}

// pickProv returns a representative provenance row for nodeID within the clip.
func pickProv(rows []Observation, nodeID, clip string) Provenance {
	for _, r := range rows {
		if r.NodeID == nodeID && r.SourceFile == clip {
			return provFromObs(r)
		}
	}
	return Provenance{SourceFile: clip, From: "palantir"}
}

func provFromObs(o Observation) Provenance {
	return Provenance{
		SourceFile: o.SourceFile, SourceSHA256: o.SourceSHA256, Timestamp: o.Timestamp,
		Signal: o.Signal, Confidence: o.Confidence, From: o.From,
		GpsLat: o.GpsLat, GpsLon: o.GpsLon,
	}
}

func sortedSignals(m map[string]*Signal) []Signal {
	out := make([]Signal, 0, len(m))
	for _, s := range m {
		out = append(out, *s)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Signal < out[j].Signal })
	return out
}

func sortProvenance(p []Provenance) []Provenance {
	out := append([]Provenance{}, p...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SourceFile != out[j].SourceFile {
			return out[i].SourceFile < out[j].SourceFile
		}
		return out[i].Timestamp < out[j].Timestamp
	})
	return out
}

func sortedKeys(m map[string][]Observation) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
