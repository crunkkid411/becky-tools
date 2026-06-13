// state.go — the typed, resumable run-state for the becky-new-tool factory.
//
// state.json is the SINGLE SOURCE OF TRUTH for one factory run. It lives in the run
// directory (.factory/<slug>-<date>/) and carries one top-level key per stage plus a
// meta block. Every stage checks "am I already done?" by inspecting its key and skips
// if so (the becky-pipeline / becky-eval resume pattern), which is what makes the
// orchestrator deterministically resumable. The orchestrator — pure Go — owns the
// stage sequence; no model ever chooses the next stage.
//
// Fact-Forcing-Gate self-certification:
//  1. Callers: cmd/new-tool/main.go, orchestrator.go, and every stage file
//     (stages.go, claude_stages.go, eval_stage.go) load/save *State here.
//  2. No-dup: cmd/new-tool/ did not exist before this run (verified by ls); the only
//     similar types are cmd/eval's Report (eval OUTPUT) and cmd/pipeline's manifest,
//     neither a factory run-state. Not a duplicate.
//  3. Data shape: reads/writes state.json — meta{slug,started,stage_cursor,
//     gates_passed,claude_cost_usd_total} + per-stage keys (intake, research,
//     redundancy, spec, build, test, review, finetune, integrate); ISO date
//     "2026-06-08" / RFC3339 timestamps.
//  4. Verbatim instruction: "BUILD: a deterministic state-machine orchestrator
//     `cmd/new-tool/` over a resumable `state.json` (stages S1 intake -> S2 research
//     -> S3 redundancy -> gate A -> S4 spec -> gate B -> S5 build -> S6 test -> S7
//     second-AI -> S9 finetune -> S10 integrate -> gate C; the Fact-Forcing Gate is
//     enforced inside S5, not a node)."
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// stateFileName is the run-state file inside the run directory.
const stateFileName = "state.json"

// State is the whole run-state. Each stage field is a pointer so "nil == not yet
// run" is unambiguous and resume can tell a skipped stage from a zero-valued one.
type State struct {
	Meta Meta `json:"meta"`

	Intake     *Intake     `json:"intake,omitempty"`
	Research   *Research   `json:"research,omitempty"`
	Redundancy *Redundancy `json:"redundancy,omitempty"`
	Spec       *Spec       `json:"spec,omitempty"`
	Build      *Build      `json:"build,omitempty"`
	Test       *TestResult `json:"test,omitempty"`
	Review     *Review     `json:"review,omitempty"`
	Finetune   *Finetune   `json:"finetune,omitempty"`
	Integrate  *Integrate  `json:"integrate,omitempty"`

	// path is the on-disk location of this state.json (not serialized).
	path string
}

// Meta is the run-level bookkeeping block.
type Meta struct {
	Slug               string   `json:"slug"`
	Pain               string   `json:"pain"`                  // the original pain-point text
	Started            string   `json:"started"`               // RFC3339
	Updated            string   `json:"updated"`               // RFC3339, rewritten on every save
	RunDir             string   `json:"run_dir"`               // absolute path
	StageCursor        string   `json:"stage_cursor"`          // the stage id to run next (e.g. "S4")
	GatesPassed        []string `json:"gates_passed"`          // e.g. ["A","B"]
	ClaudeCostUSDTotal float64  `json:"claude_cost_usd_total"` // summed across all S4/S5 Claude calls
	Offline            bool     `json:"offline"`
	Done               bool     `json:"done"` // set true after gate C passes
}

// Intake (S1) — the normalized pain-point record.
type Intake struct {
	Slug             string   `json:"slug"`
	Capability       string   `json:"capability"`
	InputKind        string   `json:"input_kind"`  // video|json|text|url|...
	OutputKind       string   `json:"output_kind"` // video|json|text|video+json|...
	Constraints      []string `json:"constraints"`
	DefinitionOfDone []string `json:"definition_of_done"`
	CapturedAt       string   `json:"captured_at"`   // ISO date
	NormalizedBy     string   `json:"normalized_by"` // "deterministic" | "cheap:<model>"
}

// Research (S2) — the sourced research brief + structured recommendation.
type Research struct {
	BriefPath     string         `json:"brief_path"`
	Recommended   ModelOrLib     `json:"recommended"`
	Fallback      ModelOrLib     `json:"fallback"`
	Sources       []Source       `json:"sources"`
	Registry      map[string]any `json:"registry,omitempty"`
	ModelChecks   []ModelCheck   `json:"model_checks"` // the Model Verification Protocol record
	SynthesizedBy string         `json:"synthesized_by"`
	QualityOK     bool           `json:"quality_ok"`
	Note          string         `json:"note,omitempty"`
}

// ModelOrLib is a recommended (or fallback) library/model/runtime.
type ModelOrLib struct {
	Library  string `json:"library,omitempty"`
	Model    string `json:"model,omitempty"`
	Runtime  string `json:"runtime,omitempty"`
	License  string `json:"license,omitempty"`
	VRAMNote string `json:"vram_note,omitempty"`
}

// Source is one dated, URL-carrying claim (the BECKY-VALIDATE-PROPOSAL standard).
type Source struct {
	URL   string `json:"url"`
	Date  string `json:"date"` // ISO date
	Claim string `json:"claim"`
}

// ModelCheck records ONE model decision made under the Model Verification Protocol.
// This is the auditable proof that no stale model id was hardcoded — every choice
// names its exact id, official source, the date it was checked at runtime, and why.
type ModelCheck struct {
	Purpose        string `json:"purpose"`    // e.g. "S2 synthesis", "build", "S7 reviewer"
	ModelID        string `json:"model_id"`   // exact, current id
	Channel        string `json:"channel"`    // local-hf | openrouter | hosted-api | claude
	SourceURL      string `json:"source_url"` // official source consulted
	CheckedAt      string `json:"checked_at"` // ISO date the runtime check ran
	InstructVsBase string `json:"instruct_vs_base,omitempty"`
	Rationale      string `json:"rationale"`
	Verified       bool   `json:"verified"` // did the runtime check confirm the id is live/on-disk?
}

// Spec (S4) — the written spec + machine-readable parts.
type Spec struct {
	SpecPath       string          `json:"spec_path"`
	CLIFlags       []FlagDef       `json:"cli_flags"`
	OutputSchema   json.RawMessage `json:"output_schema,omitempty"`
	DegradePlan    []string        `json:"degrade_plan"`
	Reuse          []string        `json:"reuse"`            // internal/... packages to reuse
	TunableSurface []string        `json:"tunable_surface"`  // S9 input
	AnswerKeyFacts []string        `json:"answer_key_facts"` // S9 input
	AuthoredBy     string          `json:"authored_by"`      // "claude:<model>" | "cheap:<model>"
	AgentSessionID string          `json:"agent_session_id,omitempty"`
	CostUSD        float64         `json:"cost_usd,omitempty"`
}

// FlagDef is one CLI flag in the spec's contract.
type FlagDef struct {
	Name string `json:"name"`
	Desc string `json:"desc"`
}

// Build (S5) — the headless-agent build outcome.
type Build struct {
	PackageDir     string      `json:"package_dir"`
	BinaryPath     string      `json:"binary_path"`
	AgentSessionID string      `json:"agent_session_id"`
	TurnsUsed      int         `json:"turns_used"`
	CostUSD        float64     `json:"cost_usd"`
	Degradations   []string    `json:"degradations,omitempty"`
	AgentReport    string      `json:"agent_final_report"`
	FactForcing    FactForcing `json:"fact_forcing"`
	Iterations     int         `json:"iterations"`
	Feedback       string      `json:"feedback,omitempty"` // S6 failure fed back to S5
	Skipped        bool        `json:"skipped,omitempty"`
	SkipReason     string      `json:"skip_reason,omitempty"`
}

// FactForcing records the 4 facts the agent stated (parsed from its transcript), so
// the Fact-Forcing Gate's satisfaction is provable after the run (SPEC §6).
type FactForcing struct {
	Callers   string `json:"callers"`
	NoDup     string `json:"no_dup"`
	DataShape string `json:"data_shape"`
	Verbatim  string `json:"verbatim"`
	Found     bool   `json:"found"` // were the 4 facts located in the transcript?
}

// TestResult (S6) — the deterministic verification of the built tool.
type TestResult struct {
	Built             bool   `json:"built"`
	Vet               bool   `json:"vet"`
	Tests             string `json:"tests"`
	RanOn             string `json:"ran_on"`
	JSONValid         bool   `json:"json_valid"`
	ExitCode          int    `json:"exit_code"`
	StderrQuiet       bool   `json:"stderr_quiet"`
	RealOutputSnippet string `json:"real_output_snippet"`
	Iterations        int    `json:"iterations"`
	Passed            bool   `json:"passed"`
	FailDetail        string `json:"fail_detail,omitempty"`
}

// Review (S7) — the second-AI feedback (a DIFFERENT, non-Claude model).
type Review struct {
	Reviewer      string    `json:"reviewer"`
	Findings      []Finding `json:"findings"`
	BlockingCount int       `json:"blocking_count"`
	Note          string    `json:"note,omitempty"`
	Skipped       bool      `json:"skipped,omitempty"`
}

// Finding is one issue from the second-AI reviewer.
type Finding struct {
	Severity   string `json:"severity"` // critical|high|medium|low
	Category   string `json:"category"`
	File       string `json:"file,omitempty"`
	Line       int    `json:"line,omitempty"`
	Issue      string `json:"issue"`
	Suggestion string `json:"suggestion,omitempty"`
}

// Finetune (S9) — the becky-eval-driven tune result.
type Finetune struct {
	ManifestPath         string          `json:"manifest_path"`
	EvalReportPath       string          `json:"eval_report_path"`
	BestConfig           json.RawMessage `json:"best_config,omitempty"`
	TrainRecall          float64         `json:"train_recall"`
	HoldoutRecall        float64         `json:"holdout_recall"`
	Applied              bool            `json:"applied"`
	GeneralizationCaveat string          `json:"generalization_caveat,omitempty"`
	Skipped              bool            `json:"skipped,omitempty"`
	SkipReason           string          `json:"skip_reason,omitempty"`
}

// Integrate (S10) — the deterministic integration outcome.
type Integrate struct {
	BuildAllGreen    bool   `json:"build_all_green"`
	ProgressRowAdded bool   `json:"progress_row_added"`
	ToolsLineUpdated bool   `json:"tools_line_updated"`
	PostIntegrateRun string `json:"post_integrate_run"`
	PRSummaryPath    string `json:"pr_summary_path"`
	Note             string `json:"note,omitempty"`
}

// loadState reads state.json from runDir, or returns a fresh State seeded with meta
// if the file does not exist yet.
func loadState(runDir string) (*State, error) {
	p := filepath.Join(runDir, stateFileName)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &State{path: p}, nil
		}
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	s.path = p
	return &s, nil
}

// save atomically writes the run-state back to disk, refreshing Meta.Updated.
func (s *State) save() error {
	if s.path == "" {
		return fmt.Errorf("state has no path (internal error)")
	}
	s.Meta.Updated = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename %s: %w", tmp, err)
	}
	return nil
}

// gatePassed reports whether a named gate (A/B/C) has been recorded as passed.
func (s *State) gatePassed(gate string) bool {
	for _, g := range s.Meta.GatesPassed {
		if g == gate {
			return true
		}
	}
	return false
}

// passGate records a gate as passed (idempotent).
func (s *State) passGate(gate string) {
	if !s.gatePassed(gate) {
		s.Meta.GatesPassed = append(s.Meta.GatesPassed, gate)
	}
}

// addClaudeCost accumulates a Claude call's cost into the run total.
func (s *State) addClaudeCost(usd float64) {
	s.Meta.ClaudeCostUSDTotal += usd
}

// todayISO is the ISO date stamp used throughout the run.
func todayISO() string { return time.Now().Format("2006-01-02") }
