// enrich.go — the OpenPlanter / web-enrichment boundary, behind a small interface.
//
// becky-palantir's DEFAULT is the offline, deterministic cooccur-only floor. The
// OpenPlanter engine (an online, non-deterministic LLM agent) is OPT-IN and is
// reached ONLY through GraphEnricher, so the deterministic core never depends on a
// network or an external binary. The cloud agent CANNOT run OpenPlanter (no binary,
// no model, no network), so the real driver is a documented STUB that the LOCAL
// agent wires to the confirmed CLI call — see the `LOCAL AGENT` markers below.
//
// LICENSE / PIN (SPEC §11, open question for Jordan): OpenPlanter
// (github.com/ShinMegamiBoson/OpenPlanter) is MIT — permissive and compatible — and
// becky drives it as a SEPARATE SUBPROCESS (does not vendor its source), so there is
// no license entanglement. OPEN: pin one CLI release (reproducible) vs track latest
// (recommendation in the spec: PIN). Until that is settled and the §12 assumptions
// are verified on the real binary, this stub returns ErrEnricherUnavailable and the
// orchestrator degrades to the cooccur-only floor at exit 0.
package palantir

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrEnricherUnavailable signals that the enrichment engine could not run (binary
// missing, model missing, timeout, or — on the cloud side — not yet wired). The
// orchestrator treats it as a DEGRADE, never a crash: fall back to cooccur-only.
var ErrEnricherUnavailable = errors.New("enrichment engine unavailable")

// EnrichOptions carries everything the driver needs for one OpenPlanter run. The
// fields mirror the SPEC §8b call. WebSearch is the ONLY thing that lets the agent
// reach the network; it is false unless Jordan passed --enrich.
type EnrichOptions struct {
	Workspace string // OpenPlanter workspace dir holding evidence.jsonl + TASK.md
	Bin       string // openplanter-agent binary (else PATH)
	Provider  string // ollama (default, local) | openai | anthropic | ...
	Model     string // per provider (ollama: llama3.2)
	MaxDepth  int
	MaxSteps  int
	WebSearch bool // ALLOW Exa/fetch_url. false => no EXA_API_KEY in subprocess env.
	Seed      int
}

// RawGraph is the minimal shape the TASK.md brief forces OpenPlanter to write to
// <workspace>/graph.out.json (SPEC §8a(4)). It is intentionally a SUBSET of the
// becky schema: the normalizer re-attaches provenance and decides documented-vs-
// candidate. The LLM's confidence is advisory only.
type RawGraph struct {
	Nodes []RawNode `json:"nodes"`
	Edges []RawEdge `json:"edges"`
}

// RawNode is one engine-proposed entity (ids seeded from becky's entities_seed.csv).
type RawNode struct {
	NodeID  string   `json:"node_id"`
	Kind    string   `json:"kind"`
	Label   string   `json:"label"`
	Aliases []string `json:"aliases"`
}

// RawEdge is one engine-proposed link. evidence_row_ids MUST resolve to real
// evidence.jsonl rows or the edge is dropped by the hallucination guard.
type RawEdge struct {
	Kind           string   `json:"kind"`
	Source         string   `json:"source"`
	Target         string   `json:"target"`
	Confidence     float64  `json:"confidence"`
	EvidenceRowIDs []string `json:"evidence_row_ids"`
}

// GraphEnricher is the opt-in, online/agent boundary. The deterministic floor does
// NOT implement it. Implementations may touch the network ONLY when
// EnrichOptions.WebSearch is true, and every web-derived fact must be logged.
type GraphEnricher interface {
	// Enrich runs the engine over the prepared workspace and returns the raw graph
	// it wrote. It returns ErrEnricherUnavailable (wrapped) to request a clean
	// degrade rather than a failure.
	Enrich(opts EnrichOptions) (RawGraph, error)
	// Name identifies the engine for the output's engine field.
	Name() string
}

// OpenPlanterStub is the SPEC §8b driver, STUBBED for the cloud side. It builds the
// exact subprocess invocation and env the local agent will confirm, then returns
// ErrEnricherUnavailable so CI/cloud runs always degrade to the floor.
type OpenPlanterStub struct{}

// Name implements GraphEnricher.
func (OpenPlanterStub) Name() string { return "openplanter" }

// Enrich is the documented stub. It (1) names the workspace artifact the real
// driver reads, (2) leaves the exact command + env the LOCAL agent must make real
// in BuildOpenPlanterArgs/Env, and (3) returns ErrEnricherUnavailable. The cloud
// agent never executes OpenPlanter.
func (OpenPlanterStub) Enrich(opts EnrichOptions) (RawGraph, error) {
	graphOut := filepath.Join(opts.Workspace, "graph.out.json")

	// LOCAL AGENT: confirm these flags against the real `openplanter-agent` and run
	// it (SPEC §8b / §12). Replace the early return below with execution + read of
	// graphOut once the assumptions in §12 are verified on the actual binary:
	//
	//   args := BuildOpenPlanterArgs(opts)          // flags only (see below)
	//   env  := BuildOpenPlanterEnv(os.Environ(), opts)
	//   cmd  := exec.Command(opts.Bin, args...); cmd.Env = env; cmd.Dir = opts.Workspace
	//   if err := cmd.Run(); err != nil { return RawGraph{}, fmt.Errorf("openplanter: %w", err) }
	//   // If the agent ignored the output-path order, scan opts.Workspace for the
	//   // newest *.json it wrote (SPEC §12 #1 risk) before giving up.
	//   return ReadRawGraph(graphOut)
	_ = graphOut
	return RawGraph{}, fmt.Errorf("openplanter driver not wired on this host: %w", ErrEnricherUnavailable)
}

// BuildOpenPlanterArgs builds the CLI flags for the SPEC §8b call. Kept pure (no
// I/O) so it is unit-testable on the cloud side without the binary.
func BuildOpenPlanterArgs(opts EnrichOptions) []string {
	return []string{
		"--workspace", opts.Workspace,
		"--headless",
		"--provider", opts.Provider,
		"--model", opts.Model,
		"--max-depth", fmt.Sprintf("%d", opts.MaxDepth),
		"--max-steps", fmt.Sprintf("%d", opts.MaxSteps),
		// LOCAL AGENT: confirm the brief is accepted as "@TASK.md" vs stdin/inline.
		"--task", "@TASK.md",
	}
}

// BuildOpenPlanterEnv returns the subprocess environment. CRITICAL invariant: when
// WebSearch is false, EXA_API_KEY is STRIPPED so the agent cannot reach the web —
// this is what makes the default run offline. Provider keys are passed through as-is
// (none needed for the local Ollama default).
func BuildOpenPlanterEnv(parent []string, opts EnrichOptions) []string {
	out := make([]string, 0, len(parent))
	for _, kv := range parent {
		if !opts.WebSearch && hasPrefix(kv, "EXA_API_KEY=") {
			continue // no web search => no Exa key in the child env
		}
		out = append(out, kv)
	}
	return out
}

// ReadRawGraph reads and parses the engine-written graph.out.json. Used by the
// LOCAL agent's real driver (and by tests with a synthetic file).
func ReadRawGraph(path string) (RawGraph, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return RawGraph{}, fmt.Errorf("read engine graph %s: %w", path, err)
	}
	var g RawGraph
	if err := json.Unmarshal(raw, &g); err != nil {
		return RawGraph{}, fmt.Errorf("parse engine graph %s: %w", path, err)
	}
	return g, nil
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
