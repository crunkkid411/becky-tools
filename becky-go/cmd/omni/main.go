// becky-omni — run a declared becky agent under Omnigent governance + observation.
//
//	becky-omni run --agent agent.yaml --manifest manifest.json --request omni.json [--json]
//	becky-omni run --manifest manifest.json --goal "..." [--sandbox offline] [--sensitive] [--json]
//	becky-omni --dry-run --manifest manifest.json --request omni.json   # build config, run NOTHING
//
// becky-omni is the governance/observation/composition tier ABOVE the harness: it CONSUMES
// the harness's tool manifest (the becky-harness --emit-omnigent artifact), generates the
// Omnigent POLICY file + the Omnibox SANDBOX config from the request's governance knobs,
// launches a sandboxed/policy-controlled/watchable session, prints a share URL Jordan can
// watch from his iPhone, and normalizes the result. It does ONE thing: run a declared agent
// under governance (SPEC-OMNIGENT.md).
//
// The deterministic boundary (request + manifest parse, governance validation, the
// policy/sandbox generation, the normalized result) is owned here + in internal/omni. The
// agent loop (non-deterministic) and everything Python (Omnigent) live behind an OmniRunner;
// in this cloud build it is a StubRunner. WINDOWS: Omnibox's hard sandbox is Linux/macOS-only;
// on Windows the run DEGRADES to a clearly-logged no-sandbox mode (policy gates still apply).
//
// Exit codes: 0 = ran (even if stopped at a limit/policy); 2 = request/usage error caught
// before launch (bad schema, sensitive+hosted, sensitive+public); 3 = omni/model unavailable.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"becky-go/internal/omni"
	"becky-go/internal/pirun"
)

const (
	exitOK      = 0
	exitUsage   = 2
	exitDegrade = 3
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run is main split out for testability; it returns an exit code and never panics.
func run(argv []string) int {
	fs := flag.NewFlagSet("becky-omni", flag.ContinueOnError)
	var (
		requestPath  = fs.String("request", "", "path to a becky-omni request JSON (governance block)")
		manifestPath = fs.String("manifest", "", "path to the harness manifest.json (from becky-harness --emit-omnigent)")
		agentPath    = fs.String("agent", "", "path to the Omnigent agent.yaml (from becky-harness)")
		goal         = fs.String("goal", "", "goal override / for the synthesized request")
		sandbox      = fs.String("sandbox", "", "offline (default) | research | full")
		share        = fs.String("share", "", "private (default) | public")
		sensitive    = fs.Bool("sensitive", false, "forensic master switch: forces local model, offline, private")
		maxCost      = fs.Float64("max-cost-usd", 0, "hard cost cap (a cost_budget policy)")
		backend      = fs.String("backend", "cli", "cli (default) | rest")
		serverURL    = fs.String("server-url", "", "Omnigent server URL for the rest backend (default http://localhost:6767)")
		runDir       = fs.String("run-dir", "", "where policy/sandbox/transcript are written")
		timeoutSec   = fs.Int("timeout-sec", 0, "wall-clock budget (0 = none)")
		asJSON       = fs.Bool("json", false, "emit the result as JSON (default plain report)")
		dryRun       = fs.Bool("dry-run", false, "build the policy/sandbox + plan, run NOTHING")
	)
	// Tolerate a leading "run" subcommand (SPEC-OMNIGENT §4 `omni run ...` shape).
	argv = stripRunSubcommand(argv)
	if err := fs.Parse(argv); err != nil {
		fmt.Fprintln(os.Stderr, "omni: bad flags:", err)
		return exitUsage
	}

	req, err := loadRequest(*requestPath, *goal, *sandbox, *share, *sensitive, *maxCost)
	if err != nil {
		fmt.Fprintln(os.Stderr, "omni: request error:", err)
		return exitUsage
	}
	if err := omni.ValidateGovernance(req); err != nil {
		fmt.Fprintln(os.Stderr, "omni: governance refused:", err)
		return exitUsage
	}

	manifest, err := loadManifest(*manifestPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "omni: manifest error:", err)
		return exitUsage
	}
	if req.Goal == "" {
		req.Goal = manifest.Goal // inherit the harness goal when not overridden
	}
	if req.Target == "" {
		req.Target = manifest.Target
	}

	dir := resolveRunDir(*runDir, req.Goal)
	policyPath, sandboxPath, err := omni.GeneratePolicy(dir, req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "omni: could not write governance config:", err)
		return exitUsage
	}
	spec := omni.BuildSpec(req, *agentPath, *manifestPath, policyPath, sandboxPath, dir, *backend, *serverURL)

	// --dry-run: everything generated, nothing executed (SPEC §7.7).
	if *dryRun {
		out := dryRunResult(req, manifest, spec)
		emit(out, *asJSON)
		fmt.Fprintln(os.Stderr, "Omnigent: DRY RUN —", spec.SandboxNote)
		return exitOK
	}

	// Real run. The OmniRunner is a stub in the cloud build; the local agent wires the real
	// one. If omni isn't on PATH, degrade (exit 3) with a one-line fix — never crash.
	if omni.ResolveBin() == "" {
		out := degradedResult(req, manifest, spec,
			"omnigent CLI not found — run: curl -fsSL https://omnigent.ai/install.sh | sh")
		emit(out, *asJSON)
		fmt.Fprintln(os.Stderr, "Omnigent: not installed. Run: curl -fsSL https://omnigent.ai/install.sh | sh")
		return exitDegrade
	}

	ctx, cancel := withTimeout(*timeoutSec)
	defer cancel()
	runner := omni.OmniRunner(&omni.StubRunner{Result: omni.OmniResult{
		Degraded:      true,
		DegradeReason: "omni runner not wired in this build (local agent supplies a real OmniRunner)",
		Stopped:       "error",
	}})
	res, runErr := omni.Run(ctx, runner, spec, nil)

	out := finalResult(req, manifest, spec, res)
	emit(out, *asJSON)
	if res.ShareURL != "" {
		fmt.Fprintln(os.Stderr, "Omnigent: watch/steer at", res.ShareURL)
	}
	if !spec.SandboxSupported {
		fmt.Fprintln(os.Stderr, "Omnigent:", spec.SandboxNote)
	}
	if res.Degraded || runErr != nil {
		fmt.Fprintln(os.Stderr, "Omnigent:", out.DegradeReason)
		return exitDegrade
	}
	return exitOK
}

// stripRunSubcommand drops a leading "run" token so `becky-omni run --flags` and
// `becky-omni --flags` both parse (SPEC-OMNIGENT §4 `omni run agent.yaml`).
func stripRunSubcommand(argv []string) []string {
	if len(argv) > 0 && argv[0] == "run" {
		return argv[1:]
	}
	return argv
}

// withTimeout returns a context with the requested wall-clock budget (or none when <= 0).
func withTimeout(timeoutSec int) (context.Context, context.CancelFunc) {
	if timeoutSec > 0 {
		return context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	}
	return context.WithCancel(context.Background())
}

// resolveRunDir picks the run directory: the explicit flag, else a slugged temp dir.
func resolveRunDir(explicit, goal string) string {
	if explicit != "" {
		return explicit
	}
	slug := pirun.RunDirSlug(goal)
	stamp := time.Now().UTC().Format("20060102-150405")
	return filepath.Join(os.TempDir(), ".omni", slug+"-"+stamp)
}

// loadManifest reads + validates the harness manifest.json the harness emitted.
func loadManifest(path string) (pirun.Manifest, error) {
	if strings.TrimSpace(path) == "" {
		return pirun.Manifest{}, fmt.Errorf("--manifest is required (the becky-harness --emit-omnigent artifact)")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return pirun.Manifest{}, fmt.Errorf("read manifest: %w", err)
	}
	return omni.LoadManifest(data)
}

// loadRequest reads a becky-omni request from --request, else synthesizes one from flags.
func loadRequest(path, goal, sandbox, share string, sensitive bool, maxCost float64) (omni.Request, error) {
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return omni.Request{}, fmt.Errorf("read request file: %w", err)
		}
		req, err := omni.ParseRequest(data)
		if err != nil {
			return omni.Request{}, err
		}
		applyFlagOverrides(&req, goal, sandbox, share, sensitive, maxCost)
		return req, nil
	}
	req := omni.Request{Schema: omni.SchemaRequest, Goal: goal}
	applyFlagOverrides(&req, goal, sandbox, share, sensitive, maxCost)
	return req, nil
}

// applyFlagOverrides layers CLI flags onto a request (flags win when set).
func applyFlagOverrides(req *omni.Request, goal, sandbox, share string, sensitive bool, maxCost float64) {
	if goal != "" {
		req.Goal = goal
	}
	if sandbox != "" {
		req.Governance.Sandbox = sandbox
	}
	if share != "" {
		req.Governance.Share = share
	}
	if sensitive {
		req.Governance.Sensitive = true
	}
	if maxCost > 0 {
		req.Governance.MaxCostUSD = maxCost
	}
}
