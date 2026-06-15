// Package palantir builds a cross-evidence entity-and-link graph over Jordan's
// OWN becky evidence outputs (identify / events / osint / cluster). It answers
// the relational question no single tool answers: who co-occurs with whom, where,
// and when.
//
// The deterministic FLOOR is a pure-Go co-occurrence pass (engine
// "cooccur-only"): entities appearing in the same evidence item / time-window get
// weighted edges, weights stable and reproducible, NO network. An optional, opt-in
// enrichment step (OpenPlanter / web) sits behind the GraphEnricher interface
// (enrich.go) and is OFF by default and logged when used.
//
// Forensic ethos (FORENSIC-OUTPUT-PHILOSOPHY.md, CLAUDE.md §2): an edge is a
// DETECTION. A relationship is only stated plainly ("documented") when ≥N
// independent signals corroborate it; a lone signal is a "candidate" lead, never
// asserted. becky owns that verdict, not any model. Output JSON is fully
// deterministic (sorted nodes/edges; never relies on map order).
package palantir

// Closed node and edge kind sets — the schema is becky's, not the engine's.
// Anything outside these is mapped to the nearest kind or dropped with a note.
const (
	KindPerson = "person"
	KindPlace  = "place"
	KindDevice = "device"
	KindEvent  = "event"

	EdgeCoOccurrence = "co_occurrence"
	EdgeContact      = "contact"
	EdgeLocation     = "location"
	EdgeDevice       = "device"
	EdgeTimeline     = "timeline"

	// StatusDocumented marks a corroborated conclusion (≥ edge-conclude distinct
	// signal families). StatusCandidate marks a single-signal lead for review.
	StatusDocumented = "documented"
	StatusCandidate  = "candidate"
)

// validNodeKinds / validEdgeKinds are the closed sets the normalizer enforces.
var validNodeKinds = map[string]bool{
	KindPerson: true, KindPlace: true, KindDevice: true, KindEvent: true,
}

var validEdgeKinds = map[string]bool{
	EdgeCoOccurrence: true, EdgeContact: true, EdgeLocation: true,
	EdgeDevice: true, EdgeTimeline: true,
}

// IsNodeKind reports whether k is a member of the closed node-kind set.
func IsNodeKind(k string) bool { return validNodeKinds[k] }

// IsEdgeKind reports whether k is a member of the closed edge-kind set.
func IsEdgeKind(k string) bool { return validEdgeKinds[k] }

// Provenance ties a node or edge back to a specific becky observation. Every
// node and edge MUST carry at least one — an untraceable finding is a bug.
type Provenance struct {
	SourceFile   string  `json:"source_file"`
	SourceSHA256 string  `json:"source_sha256,omitempty"`
	Timestamp    float64 `json:"timestamp,omitempty"`
	Signal       string  `json:"signal"`
	Confidence   float64 `json:"confidence,omitempty"`
	From         string  `json:"from"` // identify.json | events.json | osint | cluster.json | web:<url>
	GpsLat       float64 `json:"gps_lat,omitempty"`
	GpsLon       float64 `json:"gps_lon,omitempty"`
}

// Node is one entity in the graph (person / place / device / event).
type Node struct {
	NodeID              string       `json:"node_id"`
	Kind                string       `json:"kind"`
	Label               string       `json:"label"`
	Status              string       `json:"status"` // documented | candidate
	Aliases             []string     `json:"aliases,omitempty"`
	Appearances         int          `json:"appearances"`
	DistinctSourceFiles int          `json:"distinct_source_files"`
	Provenance          []Provenance `json:"provenance"`
}

// Signal is one corroborating signal family behind an edge (the audit trail).
type Signal struct {
	Signal string `json:"signal"`
	Count  int    `json:"count"`
	From   string `json:"from"`
}

// Edge is one relationship between two nodes. status is the philosophy gate.
type Edge struct {
	EdgeID               string       `json:"edge_id"`
	Kind                 string       `json:"kind"`
	Source               string       `json:"source"`
	Target               string       `json:"target"`
	Directed             bool         `json:"directed"`
	Status               string       `json:"status"`
	Summary              string       `json:"summary"`
	Weight               int          `json:"weight"`
	Confidence           float64      `json:"confidence"`
	CorroboratingSignals []Signal     `json:"corroborating_signals"`
	Provenance           []Provenance `json:"provenance"`
}

// Enrichment records whether the network/agent enrichment step ran.
type Enrichment struct {
	WebSearch bool   `json:"web_search"`
	Engine    string `json:"engine,omitempty"`
	Note      string `json:"note"`
}

// CorpusInfo summarizes what was ingested.
type CorpusInfo struct {
	Root          string `json:"root"`
	FilesIngested int    `json:"files_ingested"`
	EvidenceRows  int    `json:"evidence_rows"`
}

// Determinism records how reproducible THIS run is. The OUTPUT format is always
// deterministic; the reasoning may not be when an LLM engine is used.
type Determinism struct {
	InputSHA256  string `json:"input_sha256"`
	OutputFormat string `json:"output_format"` // always "deterministic"
	Reasoning    string `json:"reasoning"`     // deterministic | non-deterministic | non-deterministic-cached
	Seed         int    `json:"seed"`
}

// Summary is the plain-language roll-up for a non-developer reader.
type Summary struct {
	DocumentedEdges int      `json:"documented_edges"`
	CandidateEdges  int      `json:"candidate_edges"`
	TopFindings     []string `json:"top_findings"`
}

// Graph is the deterministic becky entity-graph contract (SPEC §6).
type Graph struct {
	Tool        string            `json:"tool"`
	Engine      string            `json:"engine"`
	Enrichment  Enrichment        `json:"enrichment"`
	Corpus      CorpusInfo        `json:"corpus"`
	Determinism Determinism       `json:"determinism"`
	Nodes       []Node            `json:"nodes"`
	Edges       []Edge            `json:"edges"`
	Summary     Summary           `json:"summary"`
	Degraded    bool              `json:"degraded"`
	Notes       map[string]string `json:"notes"`
}
