// Package pirun is the ONE place that knows how to drive a headless Pi agent
// (earendil-works/pi) over a DECLARED, default-deny allowlist of becky tools.
// It is deliberately parallel to internal/agentrun (which wraps headless
// `claude -p`); pirun wraps headless `pi`. SPEC-AGENT-HARNESS.md §5 mandates this
// exact package and its PiSpec/PiResult shape.
//
// The deterministic boundary lives here: request parsing, the per-run default-deny
// allowlist (only explicitly declared becky tools are permitted, everything else is
// denied), the deterministic agent-run MANIFEST the omni governance layer consumes
// (internal/omni), and the generated Pi extension. The non-determinism (the agent
// loop) is wholly contained behind the PiRunner interface — Pi execution is STUBBED
// here so the whole Go layer is unit-tested with no Node, no model, and no network.
// The local agent installs Pi and wires the real binary behind ResolveBin() / the
// real PiRunner; nothing in this package's contract should need to change.
//
// Fact-Forcing-Gate self-certification:
//  1. Callers: cmd/harness/main.go calls ParseRequest/BuildAllowlist/BuildManifest/
//     ResolveBin/Run with a StubRunner; internal/omni consumes the Manifest JSON;
//     internal/pirun/pirun_test.go exercises every deterministic function.
//  2. No-dup: internal/agentrun wraps `claude -p` (a different runtime); no existing
//     package drives Pi. SPEC-AGENT-HARNESS.md §5 mandates this exact package.
//  3. Data shape: reads a request JSON (goal, target, tools[]{tool,fixed_flags,allow},
//     builtin_tools, model{provider,id,base_url,thinking}, auth, skills[], system_append,
//     limits{max_turns,max_budget_usd,timeout_sec}, dry_run); writes a manifest JSON
//     (schema, goal, target, builtin_tools, tools[]{tool,fixed_flags}, model, auth, limits).
//  4. Verbatim instruction: "use subagents in parallel.. build everything".
//
// Invariants (CLAUDE.md §2): deterministic output (sorted, no map-order reliance);
// degrade-never-crash (never panic; errors wrapped with %w); offline (the only
// network is the explicit, logged Pi/model call the local agent wires later).
package pirun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"becky-go/internal/pathx"
)

// SchemaRequest and SchemaManifest are the versioned contract identifiers written
// into the JSON artifacts, so a consumer (becky-omni) can pin the shape it reads.
const (
	SchemaRequest  = "becky-harness/request@1"
	SchemaManifest = "becky-harness/manifest@1"
)

// Builtin-tools modes — controls Pi's own 4-tool core (read/write/edit/bash).
// "none" is the safest default: the agent has ONLY the declared becky tools.
const (
	BuiltinNone     = "none"
	BuiltinReadOnly = "read-only"
	BuiltinFull     = "full"
)

// ToolRequest is one entry in a request's tool allowlist. A tool is permitted only
// when it is listed AND Allow is not explicitly false (default-deny for the unlisted).
type ToolRequest struct {
	Tool       string   `json:"tool"`
	FixedFlags []string `json:"fixed_flags,omitempty"` // always-prepended flags (e.g. --kb kb-final)
	Allow      *bool    `json:"allow,omitempty"`       // nil = allow; explicit false = documented deny
}

// allowed reports whether this entry permits the tool (nil Allow means allowed).
func (t ToolRequest) allowed() bool { return t.Allow == nil || *t.Allow }

// ModelSpec selects the provider+model and (for local/offline) an OpenAI-compatible
// endpoint. Empty fields fall back to the Pi CLI defaults the local agent wires.
type ModelSpec struct {
	Provider string `json:"provider,omitempty"` // anthropic | openai | openai-compatible | ...
	ID       string `json:"id,omitempty"`       // e.g. "sonnet"
	BaseURL  string `json:"base_url,omitempty"` // for openai-compatible/local (offline)
	Thinking string `json:"thinking,omitempty"` // e.g. "low"
}

// Limits are the hard stops the Go driver enforces (turns, budget, wall-clock).
type Limits struct {
	MaxTurns     int     `json:"max_turns,omitempty"`
	MaxBudgetUSD float64 `json:"max_budget_usd,omitempty"`
	TimeoutSec   int     `json:"timeout_sec,omitempty"`
}

// Request is the whole point of becky-harness: it declares, for this run only, which
// tools, which model, which skill/template, and what the goal is (SPEC §2.1).
type Request struct {
	Schema         string        `json:"schema,omitempty"`
	Goal           string        `json:"goal"`
	Target         string        `json:"target,omitempty"`
	Tools          []ToolRequest `json:"tools"`
	BuiltinTools   string        `json:"builtin_tools,omitempty"` // none | read-only | full (default none)
	Model          ModelSpec     `json:"model,omitempty"`
	Auth           string        `json:"auth,omitempty"` // subscription | api-key | local (default subscription)
	Skills         []string      `json:"skills,omitempty"`
	PromptTemplate string        `json:"prompt_template,omitempty"`
	SystemAppend   string        `json:"system_append,omitempty"`
	Limits         Limits        `json:"limits,omitempty"`
	DryRun         bool          `json:"dry_run,omitempty"`
}

// ParseRequest decodes and validates a request document. It rejects an empty goal and
// an unsupported builtin_tools value before any allowlist work (fail fast at the
// boundary). It does NOT touch the network or the catalog (that is the cmd layer).
func ParseRequest(data []byte) (Request, error) {
	var req Request
	if err := json.Unmarshal(data, &req); err != nil {
		return Request{}, fmt.Errorf("parse request JSON: %w", err)
	}
	if strings.TrimSpace(req.Goal) == "" {
		return Request{}, errors.New("request.goal is required")
	}
	if _, err := normalizeBuiltins(req.BuiltinTools); err != nil {
		return Request{}, err
	}
	return req, nil
}

// normalizeBuiltins maps an empty/invalid builtin_tools value to the safe default.
func normalizeBuiltins(v string) (string, error) {
	switch v {
	case "":
		return BuiltinNone, nil
	case BuiltinNone, BuiltinReadOnly, BuiltinFull:
		return v, nil
	default:
		return "", fmt.Errorf("builtin_tools must be none|read-only|full, got %q", v)
	}
}

// ErrUnknownTool is returned when the request lists a name not in becky's catalog.
var ErrUnknownTool = errors.New("unknown becky tool in allowlist")

// Allowlist is the per-run, default-deny set of permitted becky tools, keyed by name
// and carrying each tool's fixed flags. It is the load-bearing safety object: the
// agent can call ONLY a tool present here.
type Allowlist struct {
	tools map[string]ToolRequest
	names []string // sorted, for deterministic output
}

// BuildAllowlist enforces the default-deny rule against a request: only explicitly
// listed-and-allowed tools whose names are in catalog become permitted. A listed tool
// not in catalog is a hard error (no silent drop). catalog may be nil to skip the
// catalog check (validation happens at the cmd layer where the catalog lives).
func BuildAllowlist(req Request, catalog map[string]bool) (Allowlist, error) {
	al := Allowlist{tools: map[string]ToolRequest{}}
	for _, t := range req.Tools {
		name := strings.TrimSpace(t.Tool)
		if name == "" {
			return Allowlist{}, errors.New("tool entry with empty name")
		}
		if catalog != nil && !catalog[name] {
			return Allowlist{}, fmt.Errorf("%w: %q", ErrUnknownTool, name)
		}
		if !t.allowed() {
			continue // explicit, documented deny
		}
		al.tools[name] = t
	}
	al.names = make([]string, 0, len(al.tools))
	for n := range al.tools {
		al.names = append(al.names, n)
	}
	sort.Strings(al.names)
	return al, nil
}

// Permits reports whether the agent may call the named tool. This is the enforcement
// point the third defense-in-depth gate (SPEC §7.1) re-checks before shelling.
func (a Allowlist) Permits(tool string) bool {
	_, ok := a.tools[tool]
	return ok
}

// Names returns the permitted tool names in deterministic (sorted) order.
func (a Allowlist) Names() []string {
	out := make([]string, len(a.names))
	copy(out, a.names)
	return out
}

// FixedFlags returns the always-prepended flags for a permitted tool (nil if none or
// the tool is not permitted).
func (a Allowlist) FixedFlags(tool string) []string {
	if t, ok := a.tools[tool]; ok {
		return t.FixedFlags
	}
	return nil
}

// ManifestTool is one allowlisted tool as it appears in the deterministic manifest
// the omni layer consumes (SPEC-OMNIGENT §2.3 manifest.json).
type ManifestTool struct {
	Tool       string   `json:"tool"`
	FixedFlags []string `json:"fixed_flags,omitempty"`
}

// Manifest is the deterministic agent-run manifest: the machine-readable list of
// permitted tools + the run's model/auth/limit knobs. becky-omni reads this to build
// its governance config WITHOUT re-parsing the request (SPEC-OMNIGENT §2.3).
type Manifest struct {
	Schema       string         `json:"schema"`
	Goal         string         `json:"goal"`
	Target       string         `json:"target,omitempty"`
	BuiltinTools string         `json:"builtin_tools"`
	Tools        []ManifestTool `json:"tools"`
	Model        ModelSpec      `json:"model,omitempty"`
	Auth         string         `json:"auth"`
	Limits       Limits         `json:"limits,omitempty"`
}

// BuildManifest assembles the deterministic manifest from a validated request and its
// allowlist. Tool order is sorted (Allowlist.Names) so the JSON is byte-stable for the
// same input — no map-order reliance.
func BuildManifest(req Request, al Allowlist) (Manifest, error) {
	builtins, err := normalizeBuiltins(req.BuiltinTools)
	if err != nil {
		return Manifest{}, err
	}
	auth := req.Auth
	if auth == "" {
		auth = "subscription"
	}
	tools := make([]ManifestTool, 0, len(al.names))
	for _, name := range al.Names() {
		tools = append(tools, ManifestTool{Tool: name, FixedFlags: al.FixedFlags(name)})
	}
	return Manifest{
		Schema:       SchemaManifest,
		Goal:         req.Goal,
		Target:       req.Target,
		BuiltinTools: builtins,
		Tools:        tools,
		Model:        req.Model,
		Auth:         auth,
		Limits:       req.Limits,
	}, nil
}

// MarshalManifest renders a manifest as deterministic, indented JSON.
func MarshalManifest(m Manifest) ([]byte, error) {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	return b, nil
}

// ResolveBin finds a runnable pi entrypoint (pi, pi.cmd, pi.exe) or "" so callers
// degrade gracefully instead of crashing. Mirrors agentrun.ResolveBin().
func ResolveBin() string {
	for _, name := range []string{"pi", "pi.cmd", "pi.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// ToolSpec is one allowlisted becky tool generated into the Pi extension (SPEC §5).
type ToolSpec struct {
	Name        string
	Label       string
	Description string
	Params      json.RawMessage // Typebox/JSON-schema for the tool's named params
	FixedFlags  []string
}

// PiSpec is the full description of one headless Pi run (SPEC §5). It is consumed by
// a PiRunner; the cmd layer assembles it from a validated Request.
type PiSpec struct {
	Goal           string
	Target         string
	Tools          []ToolSpec
	BuiltinTools   string
	Provider       string
	Model          string
	BaseURL        string
	Auth           string
	Skills         []string
	PromptTemplate string
	SystemAppend   string
	MaxTurns       int
	MaxBudgetUSD   float64
	TimeoutSec     int
	DryRun         bool
	RunDir         string
}

// ToolCallRecord is one becky-tool invocation the agent made during a run.
type ToolCallRecord struct {
	Tool          string          `json:"tool"`
	Args          json.RawMessage `json:"args,omitempty"`
	Exit          int             `json:"exit"`
	Degraded      bool            `json:"degraded"`
	ResultSnippet string          `json:"result_snippet,omitempty"`
}

// PiResult is the parsed outcome of a Pi run. A nil error from Run means Pi RAN and
// produced a parseable result; it does NOT mean the workflow succeeded — check
// Stopped/Degraded (same discipline as agentrun.Run).
type PiResult struct {
	FinalText        string            `json:"final_text,omitempty"`
	StructuredOutput json.RawMessage   `json:"structured_output,omitempty"`
	ToolCalls        []ToolCallRecord  `json:"tool_calls,omitempty"`
	Turns            int               `json:"turns"`
	CostUSD          float64           `json:"cost_usd"`
	Stopped          string            `json:"stopped,omitempty"` // "" | max_turns | budget | timeout | error
	Events           []json.RawMessage `json:"-"`
	Degraded         bool              `json:"degraded"`
	DegradeReason    string            `json:"degrade_reason,omitempty"`
}

// PiRunner is the STUB BOUNDARY: the one interface that actually drives a headless Pi
// agent. The cloud agent ships StubRunner (deterministic, no Node/model); the local
// agent wires a real implementation that launches `pi --mode rpc` and streams JSONL.
// Run's contract: a nil error means Pi produced a parseable result; success is read
// from PiResult.Stopped/Degraded, never from the error alone.
type PiRunner interface {
	Run(ctx context.Context, spec PiSpec, onEvent func(json.RawMessage)) (PiResult, error)
}

// StubRunner is the faked Pi the cloud agent ships so every deterministic path is
// unit-tested with no Node, model, or network. It records the spec it was given and
// returns a canned result. The local agent replaces it with a real runner.
type StubRunner struct {
	// Result is returned verbatim from Run (zero value = a trivial success).
	Result PiResult
	// Err, if set, is returned from Run (to exercise degrade paths).
	Err error
	// LastSpec captures the spec the most recent Run received (for assertions).
	LastSpec PiSpec
}

// Run implements PiRunner deterministically: it never spawns a process or touches the
// network. It records the spec, replays canned events through onEvent, and returns the
// configured result/error.
func (s *StubRunner) Run(ctx context.Context, spec PiSpec, onEvent func(json.RawMessage)) (PiResult, error) {
	s.LastSpec = spec
	if err := ctx.Err(); err != nil {
		return PiResult{Degraded: true, DegradeReason: "context cancelled", Stopped: "timeout"},
			fmt.Errorf("pi run cancelled: %w", err)
	}
	if onEvent != nil {
		for _, ev := range s.Result.Events {
			onEvent(ev)
		}
	}
	return s.Result, s.Err
}

// degradedResult builds a PiResult that records a degrade without crashing.
func degradedResult(reason string) PiResult {
	return PiResult{Degraded: true, DegradeReason: reason, Stopped: "error"}
}

// Run drives a Pi run via the supplied PiRunner, degrading (never crashing) when no
// runner is wired or the binary is absent. The model/Pi boundary is the runner; this
// function owns only the deterministic degrade decisions around it.
func Run(ctx context.Context, runner PiRunner, spec PiSpec, onEvent func(json.RawMessage)) (PiResult, error) {
	if runner == nil {
		return degradedResult("pi runner not wired (local agent installs Pi and supplies a real PiRunner)"), nil
	}
	return runner.Run(ctx, spec, onEvent)
}

// RunDirSlug derives a filesystem-safe slug from a goal string for a run directory.
// It uses pathx so a Windows-style base is handled correctly regardless of host OS.
func RunDirSlug(goal string) string {
	base := pathx.Base(strings.TrimSpace(goal))
	var b strings.Builder
	for _, r := range strings.ToLower(base) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if s == "" {
		return "run"
	}
	return s
}
