// query.go — the read-only "--query" verb (SPEC §5). The tool's ONE job is the
// graph; querying is just READING the graph it already built. A query runs a
// deterministic Go traversal over the in-memory Graph — it never invokes OpenPlanter
// and never touches the network.
//
// Supported shape: "who co-occurs with <entity>" / "who is connected to <entity>".
// The match is fuzzy by label or node_id substring (case-insensitive). Output is a
// small, deterministic answer object (sorted neighbours) so it stays scriptable.
package palantir

import (
	"sort"
	"strings"
)

// QueryAnswer is the deterministic result of a graph query.
type QueryAnswer struct {
	Query     string     `json:"query"`
	Matched   string     `json:"matched_node,omitempty"`
	Neighbors []Neighbor `json:"neighbors"`
	Note      string     `json:"note,omitempty"`
}

// Neighbor is one connected entity and the strongest edge to it.
type Neighbor struct {
	NodeID     string  `json:"node_id"`
	Label      string  `json:"label"`
	EdgeKind   string  `json:"edge_kind"`
	Status     string  `json:"status"`
	Weight     int     `json:"weight"`
	Confidence float64 `json:"confidence"`
	Summary    string  `json:"summary"`
}

// Query answers a single graph question against an already-built Graph. An empty or
// unrecognized query returns a plain-language note (degrade, not error).
func Query(g Graph, text string) QueryAnswer {
	ans := QueryAnswer{Query: text}
	term := extractTerm(text)
	if term == "" {
		ans.Note = "couldn't read an entity from the query; try \"who co-occurs with <name>\"."
		ans.Neighbors = []Neighbor{}
		return ans
	}
	node, ok := findNode(g.Nodes, term)
	if !ok {
		ans.Note = "no entity in the graph matched \"" + term + "\"."
		ans.Neighbors = []Neighbor{}
		return ans
	}
	ans.Matched = node.NodeID
	ans.Neighbors = neighborsOf(g, node.NodeID)
	if len(ans.Neighbors) == 0 {
		ans.Note = node.Label + " has no recorded connections in this graph."
	}
	return ans
}

// extractTerm pulls the entity term out of a "who ... with/to <term>" question. If
// no preposition is found it falls back to the last word.
func extractTerm(text string) string {
	t := strings.ToLower(strings.TrimSpace(text))
	for _, prep := range []string{" with ", " to ", " of ", " for "} {
		if i := strings.LastIndex(t, prep); i >= 0 {
			return strings.TrimSpace(text[i+len(prep):])
		}
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

// findNode returns the first node whose label or id contains term (case-insensitive,
// scanned in node_id order so the match is deterministic).
func findNode(nodes []Node, term string) (Node, bool) {
	needle := strings.ToLower(strings.TrimSpace(term))
	sorted := append([]Node{}, nodes...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].NodeID < sorted[j].NodeID })
	for _, n := range sorted {
		if strings.Contains(strings.ToLower(n.Label), needle) ||
			strings.Contains(strings.ToLower(n.NodeID), needle) {
			return n, true
		}
	}
	return Node{}, false
}

// neighborsOf returns every node connected to nodeID by an edge, keeping the
// strongest edge per neighbour, sorted by status (documented first) then confidence.
func neighborsOf(g Graph, nodeID string) []Neighbor {
	labels := labelIndex(g.Nodes)
	best := map[string]Neighbor{}
	for _, e := range g.Edges {
		other, ok := otherEnd(e, nodeID)
		if !ok {
			continue
		}
		cand := Neighbor{
			NodeID: other, Label: labels[other], EdgeKind: e.Kind, Status: e.Status,
			Weight: e.Weight, Confidence: e.Confidence, Summary: e.Summary,
		}
		if cur, seen := best[other]; !seen || edgeStronger(cand, cur) {
			best[other] = cand
		}
	}
	return sortNeighbors(best)
}

func otherEnd(e Edge, nodeID string) (string, bool) {
	switch nodeID {
	case e.Source:
		return e.Target, true
	case e.Target:
		return e.Source, true
	}
	return "", false
}

func edgeStronger(a, b Neighbor) bool {
	if (a.Status == StatusDocumented) != (b.Status == StatusDocumented) {
		return a.Status == StatusDocumented
	}
	return a.Confidence > b.Confidence
}

func labelIndex(nodes []Node) map[string]string {
	m := make(map[string]string, len(nodes))
	for _, n := range nodes {
		m[n.NodeID] = n.Label
	}
	return m
}

func sortNeighbors(m map[string]Neighbor) []Neighbor {
	out := make([]Neighbor, 0, len(m))
	for _, n := range m {
		out = append(out, n)
	}
	sort.SliceStable(out, func(i, j int) bool {
		di, dj := out[i].Status == StatusDocumented, out[j].Status == StatusDocumented
		if di != dj {
			return di
		}
		if out[i].Confidence != out[j].Confidence {
			return out[i].Confidence > out[j].Confidence
		}
		return out[i].NodeID < out[j].NodeID
	})
	return out
}
