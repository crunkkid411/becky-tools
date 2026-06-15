// Package omni is the ONE place that knows how to drive a GOVERNED Omnigent session
// (omnigent-ai/omnigent) that runs a becky agent the harness defined. It is deliberately
// parallel to internal/pirun and internal/agentrun; omni wraps the `omni`/`omnigent`
// meta-harness. SPEC-OMNIGENT.md §5 mandates this exact package + its OmniSpec/OmniResult/
// Governance shape.
//
// THE RECONCILIATION (SPEC-OMNIGENT §2): becky-harness is the GENERATOR — it emits the
// agent definition + the deterministic tool MANIFEST (pirun.Manifest). becky-omni is the
// RUNTIME — it CONSUMES that manifest, generates the Omnigent POLICY file + the Omnibox
// SANDBOX config from the request's governance knobs, launches the session under that
// governance, and normalizes the result. So omni does not duplicate the harness; it runs
// the harness's manifest under policy.
//
// The deterministic boundary lives here: governance validation (the sensitive-master
// switch), the policy/sandbox generation (plain text — no YAML parser dependency), and the
// result normalization. The non-determinism (the agent loop) and everything Python
// (Omnigent itself) live behind the OmniRunner interface — STUBBED here so the whole Go
// layer is unit-tested with no Python, model, or network. WINDOWS NOTE: Omnigent's Omnibox
// hard sandbox is Linux/macOS-native (bubblewrap+seccomp / Seatbelt); on Windows GeneratePolicy
// DEGRADES to a clearly-logged no-sandbox mode rather than failing (SandboxStatus records it).
//
// Fact-Forcing-Gate self-certification:
//  1. Callers: cmd/omni/main.go calls LoadManifest/ParseRequest/ValidateGovernance/
//     GeneratePolicy/ResolveBin/Run; internal/omni/omni_test.go tests them.
//  2. No-dup: no existing internal/omni; pirun is a different runtime (Pi vs Omnigent).
//  3. Data shape: reads pirun.Manifest + a governance request; emits policy.yaml + sandbox
//     config (plain text) + an OmniResult. No data files with dates.
//  4. Verbatim instruction: "use subagents in parallel.. build everything".
//
// Invariants (CLAUDE.md §2): deterministic output; degrade-never-crash (no panic; %w wraps);
// offline by default (the network is the explicit, logged Omnigent call the local agent wires).
package omni

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strings"

	"becky-go/internal/pirun"
)

// SchemaRequest is the versioned governance-request contract id (SPEC-OMNIGENT §5.1).
const SchemaRequest = "becky-omnigent/request@1"

// Sandbox modes -> Omnibox network policy (SPEC-OMNIGENT §5.1/§5.3).
const (
	SandboxOffline  = "offline"  // default: no egress (the becky offline invariant, enforced by the OS)
	SandboxResearch = "research" // only the named allow_hosts may egress (logged)
	SandboxFull     = "full"     // unrestricted egress
)

// Filesystem-write modes -> Omnibox FS grants.
const (
	FSWritesDeny       = "deny"            // default: case dir read-only
	FSWritesOutputOnly = "output-dir-only" // exactly one writable output dir
	FSWritesFull       = "full"
)

// Share modes (SPEC-OMNIGENT §3.3/§5.1).
const (
	SharePrivate = "private" // default: localhost/LAN only
	SharePublic  = "public"  // opt-in, logged, FORBIDDEN when sensitive
)

// Governance is the block becky-omni adds on top of the harness request (SPEC §5.1).
type Governance struct {
	Sandbox       string    `json:"sandbox,omitempty"`     // offline (default) | research | full
	AllowHosts    []string  `json:"allow_hosts,omitempty"` // research-only egress allow-list (logged)
	FSWrites      string    `json:"fs_writes,omitempty"`   // deny (default) | output-dir-only | full
	OutputDir     string    `json:"output_dir,omitempty"`  // the one writable dir for output-dir-only
	AskOn         []string  `json:"ask_on,omitempty"`      // actions that PAUSE for approval (a tap)
	MaxCostUSD    float64   `json:"max_cost_usd,omitempty"`
	AskThresholds []float64 `json:"ask_thresholds_usd,omitempty"`
	Share         string    `json:"share,omitempty"`     // private (default) | public
	Sensitive     bool      `json:"sensitive,omitempty"` // master forensic switch (forces offline/local/private)
}

// Request is becky-omni's request: the harness request fields plus the governance block.
// It embeds the same model/auth knobs the harness request carries so omni can refuse a
// sensitive+hosted combination before launch (SPEC §5.1).
type Request struct {
	Schema     string          `json:"schema,omitempty"`
	Goal       string          `json:"goal,omitempty"`
	Target     string          `json:"target,omitempty"`
	Model      pirun.ModelSpec `json:"model,omitempty"`
	Auth       string          `json:"auth,omitempty"` // subscription | api-key | local
	Governance Governance      `json:"governance,omitempty"`
}

// ParseRequest decodes a becky-omni request document (no network, no manifest yet).
func ParseRequest(data []byte) (Request, error) {
	var req Request
	if err := json.Unmarshal(data, &req); err != nil {
		return Request{}, fmt.Errorf("parse omni request JSON: %w", err)
	}
	return req, nil
}

// LoadManifest decodes a harness manifest.json (the contract that crosses the seam,
// SPEC-OMNIGENT §2.3). becky-omni consumes it WITHOUT re-parsing the harness request.
func LoadManifest(data []byte) (pirun.Manifest, error) {
	var m pirun.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return pirun.Manifest{}, fmt.Errorf("parse harness manifest: %w", err)
	}
	if m.Schema != pirun.SchemaManifest {
		return pirun.Manifest{}, fmt.Errorf("manifest schema = %q, want %q", m.Schema, pirun.SchemaManifest)
	}
	if len(m.Tools) == 0 {
		return pirun.Manifest{}, errors.New("manifest declares no tools (default-deny would permit nothing)")
	}
	return m, nil
}

// effectiveGovernance fills governance defaults and applies the sensitive master switch,
// returning the effective governance + effective auth. sensitive:true FORCES auth:local,
// sandbox>=offline, share:private (SPEC §5.1) — convenience is overridden by the floor.
func (req Request) effectiveGovernance() (Governance, string) {
	g := req.Governance
	if g.Sandbox == "" {
		g.Sandbox = SandboxOffline
	}
	if g.FSWrites == "" {
		g.FSWrites = FSWritesDeny
	}
	if g.Share == "" {
		g.Share = SharePrivate
	}
	auth := req.Auth
	if auth == "" {
		auth = "subscription"
	}
	if g.Sensitive {
		auth = "local" // no hosted model sees case text
		if g.Sandbox == SandboxFull {
			g.Sandbox = SandboxOffline // never loosen below offline for sensitive material
		}
		g.Share = SharePrivate
	}
	return g, auth
}

// ValidateGovernance enforces the becky safety rules BEFORE launch (a usage error, not a
// crash). It refuses a sensitive run paired with a hosted model or a public share, an
// output-dir-only mode with no output_dir, and research with no allow_hosts (SPEC §5.1).
func ValidateGovernance(req Request) error {
	g := req.Governance
	switch g.Sandbox {
	case "", SandboxOffline, SandboxResearch, SandboxFull:
	default:
		return fmt.Errorf("governance.sandbox must be offline|research|full, got %q", g.Sandbox)
	}
	switch g.FSWrites {
	case "", FSWritesDeny, FSWritesOutputOnly, FSWritesFull:
	default:
		return fmt.Errorf("governance.fs_writes must be deny|output-dir-only|full, got %q", g.FSWrites)
	}
	switch g.Share {
	case "", SharePrivate, SharePublic:
	default:
		return fmt.Errorf("governance.share must be private|public, got %q", g.Share)
	}
	if g.FSWrites == FSWritesOutputOnly && strings.TrimSpace(g.OutputDir) == "" {
		return errors.New("governance.fs_writes=output-dir-only requires governance.output_dir")
	}
	if g.Sandbox == SandboxResearch && len(g.AllowHosts) == 0 {
		return errors.New("governance.sandbox=research requires at least one allow_hosts entry")
	}
	if g.Sensitive {
		if isHostedModel(req.Auth) {
			return errors.New("sensitive:true forbids a hosted model — set auth:local (SPEC §5.1)")
		}
		if g.Share == SharePublic {
			return errors.New("sensitive:true forbids a public share — set share:private (SPEC §5.1)")
		}
	}
	return nil
}

// isHostedModel reports whether the auth implies a hosted (network) model. "local" and
// "" (defaulted later) are not hosted; "subscription"/"api-key" are hosted.
func isHostedModel(auth string) bool {
	switch auth {
	case "subscription", "api-key":
		return true
	default:
		return false
	}
}

// ResolveBin finds a runnable omni/omnigent entrypoint or "" so callers degrade. Mirrors
// pirun.ResolveBin()/agentrun.ResolveBin().
func ResolveBin() string {
	for _, name := range []string{"omni", "omnigent", "omni.cmd", "omnigent.cmd", "omni.exe", "omnigent.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// sandboxStatus reports whether the OS supports Omnibox's hard sandbox. Omnibox is
// Linux/macOS-native; on Windows we DEGRADE to no-sandbox with a clear log (SPEC §8 local
// step 3 — the single biggest Windows-verification risk).
func sandboxStatus() (supported bool, note string) {
	switch runtime.GOOS {
	case "linux", "darwin":
		return true, "Omnibox OS sandbox available (" + runtime.GOOS + ")"
	default:
		return false, "no OS sandbox on " + runtime.GOOS +
			" — Omnibox is Linux/macOS-native; running in NO-SANDBOX degrade mode (policy gates still apply)"
	}
}

// ToolCallRecord mirrors pirun's for a uniform result shape across the two runtimes.
type ToolCallRecord = pirun.ToolCallRecord

// OmniResult is the parsed outcome of a governed session (SPEC §5.4). A nil error from Run
// means the session RAN and produced a parseable result; success is read from Stopped/Degraded.
type OmniResult struct {
	FinalText     string            `json:"final_text,omitempty"`
	Structured    json.RawMessage   `json:"structured,omitempty"`
	ToolCalls     []ToolCallRecord  `json:"tool_calls,omitempty"`
	ShareURL      string            `json:"share_url,omitempty"`
	SessionID     string            `json:"session_id,omitempty"`
	CostUSD       float64           `json:"cost_usd"`
	Stopped       string            `json:"stopped,omitempty"` // "" | budget | timeout | policy_deny | error
	Events        []json.RawMessage `json:"-"`
	Degraded      bool              `json:"degraded"`
	DegradeReason string            `json:"degrade_reason,omitempty"`
}

// OmniSpec is the full description of one governed session (SPEC §5.4). The cmd layer
// assembles it from a validated request + the harness manifest + the generated config.
type OmniSpec struct {
	AgentYAMLPath string
	ManifestPath  string
	PolicyPath    string
	SandboxPath   string
	Goal          string
	Target        string
	Provider      string
	Model         string
	BaseURL       string
	Auth          string
	Gov           Governance
	Backend       string // "cli" | "rest"
	ServerURL     string
	RunDir        string
	// SandboxSupported records whether the OS provides Omnibox's hard sandbox; false on
	// Windows (no-sandbox degrade). The runner must surface this in logs.
	SandboxSupported bool
	SandboxNote      string
}

// OmniRunner is the STUB BOUNDARY: the one interface that drives a governed Omnigent
// session. The cloud agent ships StubRunner (deterministic, no Python/model); the local
// agent wires a real implementation (CLI subprocess `omni run`, or the REST backend).
type OmniRunner interface {
	Run(ctx context.Context, spec OmniSpec, onEvent func(json.RawMessage)) (OmniResult, error)
}

// StubRunner is the faked Omnigent the cloud agent ships so every deterministic path is
// unit-tested with no Python, model, or network.
type StubRunner struct {
	Result   OmniResult
	Err      error
	LastSpec OmniSpec
}

// Run implements OmniRunner deterministically: it never spawns a process or hits the
// network. It records the spec, replays canned events, and returns the configured result.
func (s *StubRunner) Run(ctx context.Context, spec OmniSpec, onEvent func(json.RawMessage)) (OmniResult, error) {
	s.LastSpec = spec
	if err := ctx.Err(); err != nil {
		return OmniResult{Degraded: true, DegradeReason: "context cancelled", Stopped: "timeout"},
			fmt.Errorf("omni run cancelled: %w", err)
	}
	if onEvent != nil {
		for _, ev := range s.Result.Events {
			onEvent(ev)
		}
	}
	return s.Result, s.Err
}

// Run drives a governed session via the supplied OmniRunner, degrading (never crashing)
// when no runner is wired. The Python/Omnigent boundary is the runner; this function owns
// only the deterministic degrade decisions around it.
func Run(ctx context.Context, runner OmniRunner, spec OmniSpec, onEvent func(json.RawMessage)) (OmniResult, error) {
	if runner == nil {
		return OmniResult{
			Degraded:      true,
			DegradeReason: "omni runner not wired (local agent installs Omnigent and supplies a real OmniRunner)",
			Stopped:       "error",
		}, nil
	}
	return runner.Run(ctx, spec, onEvent)
}

// sortedFloats returns a sorted copy so generated thresholds are deterministic.
func sortedFloats(in []float64) []float64 {
	out := make([]float64, len(in))
	copy(out, in)
	sort.Float64s(out)
	return out
}
