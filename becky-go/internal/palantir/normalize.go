// normalize.go — step C (NORMALIZE): becky owns the verdict. This is where the
// forensic philosophy is enforced on the graph:
//
//   - CLOSED kind set. Nodes/edges outside the schema (§6) are dropped with a note —
//     the schema is becky's, not the engine's.
//   - CORROBORATE, then CONCLUDE. An edge is "documented" only when ≥ concludeN
//     DISTINCT signal families support it; otherwise it is a "candidate" lead. A lone
//     weak signal is never asserted as fact (FORENSIC-OUTPUT-PHILOSOPHY top principle).
//   - PROVENANCE on every surviving node/edge — an untraceable finding is a bug.
//   - PLAIN LANGUAGE summaries ("appear together in 9 clips", not a count expression).
//
// Status is computed deterministically from becky provenance, NOT taken from any
// model's self-reported confidence. None of this calls a model or the network.
package palantir

import (
	"fmt"
	"sort"
)

// DefaultEdgeConclude is the default number of distinct signal families required to
// promote an edge from candidate to documented (CLAUDE.md §2: ≥2 independent signals).
const DefaultEdgeConclude = 2

// Normalize applies the schema + conclude rule to a raw graph, returning the
// cleaned nodes/edges and any plain-language notes about what was dropped. nodes/
// edges produced by the deterministic floor are already valid; this still runs so
// the SAME gate governs both the floor and any engine output.
func Normalize(nodes []Node, edges []Edge, concludeN int) ([]Node, []Edge, []string) {
	if concludeN < 1 {
		concludeN = DefaultEdgeConclude
	}
	var notes []string
	cleanNodes, validIDs, nodeNotes := normalizeNodes(nodes)
	notes = append(notes, nodeNotes...)
	labels := nodeLabels(cleanNodes)
	cleanEdges, edgeNotes := normalizeEdges(edges, validIDs, labels, concludeN)
	notes = append(notes, edgeNotes...)
	return cleanNodes, cleanEdges, notes
}

// nodeLabels maps node_id -> human label so edge summaries read naturally
// ("John Clancy and Shelby ...") rather than echoing raw ids.
func nodeLabels(nodes []Node) map[string]string {
	m := make(map[string]string, len(nodes))
	for _, n := range nodes {
		m[n.NodeID] = n.Label
	}
	return m
}

// label returns the human label for a node id, falling back to the id itself.
func label(labels map[string]string, id string) string {
	if l, ok := labels[id]; ok && l != "" {
		return l
	}
	return id
}

// normalizeNodes drops nodes outside the closed kind set or lacking provenance,
// returning the survivors, the set of valid node ids, and drop notes.
func normalizeNodes(nodes []Node) ([]Node, map[string]bool, []string) {
	var out []Node
	var notes []string
	valid := map[string]bool{}
	for _, n := range nodes {
		if !IsNodeKind(n.Kind) {
			notes = append(notes, fmt.Sprintf("dropped node %q: kind %q outside the closed schema", n.NodeID, n.Kind))
			continue
		}
		if len(n.Provenance) == 0 {
			notes = append(notes, fmt.Sprintf("dropped node %q: no provenance (untraceable)", n.NodeID))
			continue
		}
		valid[n.NodeID] = true
		out = append(out, n)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out, valid, notes
}

// normalizeEdges enforces the closed kind set, the hallucination guard (both
// endpoints must be real nodes), and the corroborate-then-conclude status rule.
func normalizeEdges(edges []Edge, validIDs map[string]bool, labels map[string]string, concludeN int) ([]Edge, []string) {
	var out []Edge
	var notes []string
	for _, e := range edges {
		if !IsEdgeKind(e.Kind) {
			notes = append(notes, fmt.Sprintf("dropped edge %q: kind %q outside the closed schema", e.EdgeID, e.Kind))
			continue
		}
		if !validIDs[e.Source] || !validIDs[e.Target] {
			notes = append(notes, fmt.Sprintf("dropped edge %q: endpoint missing from nodes (hallucination guard)", e.EdgeID))
			continue
		}
		if len(e.Provenance) == 0 {
			notes = append(notes, fmt.Sprintf("dropped edge %q: no provenance (untraceable)", e.EdgeID))
			continue
		}
		out = append(out, concludeEdge(e, labels, concludeN))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].EdgeID < out[j].EdgeID })
	return out, notes
}

// concludeEdge sets the edge's status, confidence, and plain-language summary from
// its corroborating signals. ≥concludeN DISTINCT signal families → documented.
func concludeEdge(e Edge, labels map[string]string, concludeN int) Edge {
	families := distinctSignalFamilies(e.CorroboratingSignals)
	if families >= concludeN {
		e.Status = StatusDocumented
	} else {
		e.Status = StatusCandidate
	}
	if e.Weight == 0 {
		e.Weight = totalSignalCount(e.CorroboratingSignals)
	}
	e.Confidence = edgeConfidence(e.Weight, families)
	e.Summary = edgeSummary(e, labels, families)
	return e
}

// distinctSignalFamilies counts the distinct (non-empty) signal-family names with a
// positive count — these are the INDEPENDENT signals the conclude rule weighs.
func distinctSignalFamilies(sigs []Signal) int {
	seen := map[string]bool{}
	for _, s := range sigs {
		if s.Signal != "" && s.Count > 0 {
			seen[s.Signal] = true
		}
	}
	return len(seen)
}

func totalSignalCount(sigs []Signal) int {
	total := 0
	for _, s := range sigs {
		total += s.Count
	}
	return total
}

// edgeConfidence is a deterministic, bounded score: more supporting clips and more
// independent signal families both raise it, saturating below 1.0. It is an
// ordering aid for the analyst, NOT a probability — the status field is the verdict.
func edgeConfidence(weight, families int) float64 {
	if weight < 1 {
		weight = 1
	}
	base := 1.0 - 1.0/float64(weight+1) // 1 clip→0.50, 2→0.67, 4→0.80, 9→0.90
	bonus := 0.05 * float64(families-1) // each extra independent family nudges up
	c := base + bonus
	if c > 0.99 {
		c = 0.99
	}
	return round2(c)
}

// edgeSummary renders a plain-English description per edge kind (no count syntax),
// using human labels for the endpoints.
func edgeSummary(e Edge, labels map[string]string, families int) string {
	clips := pluralClips(e.Weight)
	a, b := label(labels, e.Source), label(labels, e.Target)
	switch e.Kind {
	case EdgeCoOccurrence, EdgeContact:
		if families >= 2 {
			return fmt.Sprintf("%s and %s appear together in %s — corroborated by %d independent signals.",
				a, b, clips, families)
		}
		return fmt.Sprintf("%s and %s appear together in %s (single signal — a lead, not a conclusion).",
			a, b, clips)
	case EdgeLocation:
		return fmt.Sprintf("%s is tied to %s across %s.", a, b, clips)
	case EdgeDevice:
		return fmt.Sprintf("%s is associated with %s across %s.", a, b, clips)
	case EdgeTimeline:
		return fmt.Sprintf("%s is linked to %s by shared timing across %s.", a, b, clips)
	}
	return fmt.Sprintf("%s relates to %s across %s.", a, b, clips)
}

func pluralClips(n int) string {
	if n == 1 {
		return "1 clip"
	}
	return fmt.Sprintf("%d clips", n)
}

// BuildSummary produces the plain-language roll-up: documented/candidate counts and
// the top documented findings (highest confidence first).
func BuildSummary(edges []Edge) Summary {
	s := Summary{}
	var documented []Edge
	for _, e := range edges {
		if e.Status == StatusDocumented {
			s.DocumentedEdges++
			documented = append(documented, e)
		} else {
			s.CandidateEdges++
		}
	}
	sort.SliceStable(documented, func(i, j int) bool {
		if documented[i].Confidence != documented[j].Confidence {
			return documented[i].Confidence > documented[j].Confidence
		}
		return documented[i].EdgeID < documented[j].EdgeID
	})
	for i, e := range documented {
		if i >= 5 {
			break
		}
		s.TopFindings = append(s.TopFindings, e.Summary)
	}
	return s
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }
