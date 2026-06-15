// becky-palantir — build and query a cross-evidence entity & link graph over
// Jordan's OWN becky evidence outputs.
//
//	becky-palantir --corpus <dir> [--engine cooccur-only|openplanter] [--enrich]
//	   [--edge-conclude 2] [--cluster <cluster.json>] [--output <file>]
//	   [--query "who co-occurs with John"] [--json] [--verbose]
//
// becky already answers, per clip: WHO (identify), WHAT-happens (events), WHERE
// (osint EXIF/GPS), and groups recurring unknowns (cluster). The missing layer is
// the GRAPH — who co-occurs with whom, where, and when. This tool consumes those
// outputs and emits a deterministic becky graph (nodes[]/edges[]) where an edge is a
// DETECTION and a relationship is only stated plainly ("documented") when ≥N
// independent signals corroborate it; a lone signal is a "candidate" lead.
//
// DEFAULT is the offline, deterministic cooccur-only floor — NO network, NO model.
// The openplanter engine is opt-in; if it can't run on this machine the tool
// DEGRADES to the floor with a plain note (exit 0), never crashing. Web enrichment
// is OFF unless --enrich is set, and is logged when used.
//
// Exit codes: 0 = a completed graph OR a clean degrade-with-note; 1 = hard error
// (e.g. can't write --output); 2 = usage error.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"becky-go/internal/palantir"
)

func main() {
	cfg := parseFlags()

	in := palantir.Harvest(palantir.HarvestOptions{Root: cfg.corpus, ClusterPath: cfg.cluster})
	graph := palantir.Build(in, buildOptions(cfg))

	if cfg.query != "" {
		emitQuery(graph, cfg)
		return
	}
	emitGraph(graph, cfg)
}

// config holds the parsed CLI flags.
type config struct {
	corpus       string
	cluster      string
	engine       string
	provider     string
	model        string
	maxDepth     int
	maxSteps     int
	edgeConclude int
	enrich       bool
	seed         int
	output       string
	query        string
	asJSON       bool
	verbose      bool
}

func parseFlags() config {
	var c config
	flag.StringVar(&c.corpus, "corpus", "", "root dir holding becky outputs (identify/events/osint/cluster *.json)")
	flag.StringVar(&c.cluster, "cluster", "", "explicit becky-cluster JSON to fold in (corpus-wide groupings)")
	flag.StringVar(&c.engine, "engine", palantir.EngineCooccurOnly, "cooccur-only (default, offline) | openplanter")
	flag.StringVar(&c.provider, "provider", "ollama", "OpenPlanter provider (ollama=local/offline by default)")
	flag.StringVar(&c.model, "model", "llama3.2", "OpenPlanter model")
	flag.IntVar(&c.maxDepth, "max-depth", 4, "OpenPlanter recursion depth")
	flag.IntVar(&c.maxSteps, "max-steps", 100, "OpenPlanter step budget")
	flag.IntVar(&c.edgeConclude, "edge-conclude", palantir.DefaultEdgeConclude, "independent signals to CONCLUDE an edge")
	flag.BoolVar(&c.enrich, "enrich", false, "ALLOW OpenPlanter web search (Exa). OFF by default; logged when used")
	flag.IntVar(&c.seed, "seed", 0, "recorded; passed to the provider if it honors it")
	flag.StringVar(&c.output, "output", "", "write graph JSON to this file instead of stdout")
	flag.StringVar(&c.query, "query", "", "after building, answer one graph query and exit (read-only)")
	flag.BoolVar(&c.asJSON, "json", false, "for --query: emit JSON instead of a plain-language answer")
	flag.BoolVar(&c.verbose, "verbose", false, "print progress/headlines to stderr")
	flag.Parse()

	if strings.TrimSpace(c.corpus) == "" && strings.TrimSpace(c.cluster) == "" {
		fmt.Fprintln(os.Stderr, "usage: becky-palantir --corpus <dir> [--engine ...] [--query ...]")
		os.Exit(2)
	}
	return c
}

// buildOptions maps the CLI config to the engine Options. The OpenPlanter driver is
// the documented stub on this build; if --engine openplanter is chosen it attempts
// the stub and degrades to the floor (the local agent wires the real binary).
func buildOptions(c config) palantir.Options {
	opts := palantir.Options{
		Engine:       c.engine,
		EdgeConclude: c.edgeConclude,
		Enrich:       c.enrich,
		CorpusRoot:   c.corpus,
		Seed:         c.seed,
	}
	if c.engine == palantir.EngineOpenPlanter {
		opts.Enricher = palantir.OpenPlanterStub{}
		opts.EnrichOpts = palantir.EnrichOptions{
			Provider: c.provider, Model: c.model,
			MaxDepth: c.maxDepth, MaxSteps: c.maxSteps,
			WebSearch: c.enrich, Seed: c.seed,
		}
	}
	return opts
}

// emitGraph writes the deterministic graph JSON to stdout or --output, and a short
// plain-language headline to stderr under --verbose.
func emitGraph(g palantir.Graph, c config) {
	b, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "encode graph:", err)
		os.Exit(1)
	}
	b = append(b, '\n')
	if c.output != "" {
		if err := os.WriteFile(c.output, b, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "write --output:", err)
			os.Exit(1)
		}
	} else {
		os.Stdout.Write(b)
	}
	if c.verbose {
		printHeadline(g)
	}
}

// emitQuery answers one read-only graph query (no engine, no network).
func emitQuery(g palantir.Graph, c config) {
	ans := palantir.Query(g, c.query)
	if c.asJSON {
		b, err := json.MarshalIndent(ans, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "encode answer:", err)
			os.Exit(1)
		}
		os.Stdout.Write(append(b, '\n'))
		return
	}
	printAnswer(ans)
}

// printHeadline writes a one-paragraph, non-developer summary to stderr.
func printHeadline(g palantir.Graph) {
	fmt.Fprintf(os.Stderr, "becky-palantir — engine %s over %d evidence rows from %d file(s)\n",
		g.Engine, g.Corpus.EvidenceRows, g.Corpus.FilesIngested)
	if g.Degraded {
		fmt.Fprintln(os.Stderr, "  note:", g.Notes["degrade"])
	}
	fmt.Fprintf(os.Stderr, "  %d corroborated (documented) edge(s), %d candidate lead(s).\n",
		g.Summary.DocumentedEdges, g.Summary.CandidateEdges)
	for _, f := range g.Summary.TopFindings {
		fmt.Fprintln(os.Stderr, "  -", f)
	}
}

// printAnswer writes a plain-language query answer to stdout.
func printAnswer(a palantir.QueryAnswer) {
	fmt.Printf("Query: %s\n", a.Query)
	if a.Note != "" && len(a.Neighbors) == 0 {
		fmt.Println(a.Note)
		return
	}
	fmt.Printf("Matched: %s\n", a.Matched)
	fmt.Println("Connected to:")
	for _, n := range a.Neighbors {
		fmt.Printf("  - %s [%s, %s] %s\n", n.Label, n.EdgeKind, n.Status, n.Summary)
	}
}
