// becky-harness — run a declared Pi agent over a default-deny allowlist of becky tools.
//
//	becky-harness --request run.json [--json] [--dry-run]
//	becky-harness --goal "..." --tools becky-transcribe,becky-identify [--target DIR] [--json]
//	becky-harness --emit-omnigent --request run.json   # emit only the manifest for becky-omni
//
// becky-harness spins up a configured Pi agent (earendil-works/pi), hands it EXACTLY
// the becky tools the request declared (default-deny: nothing else), and lets the agent
// loop a repetitive multi-step workflow to completion — then returns one JSON result.
// It does ONE thing: run a declared agent over a declared tool allowlist (SPEC-AGENT-HARNESS.md).
//
// The deterministic boundary (request parse, the allowlist, the agent-run MANIFEST that
// becky-omni consumes, the generated Pi extension, the normalized result) is owned here +
// in internal/pirun. The agent loop (non-deterministic) is contained behind a PiRunner;
// in this cloud build it is a StubRunner, so the whole tool runs with no Node/model. The
// local agent installs Pi and wires the real runner. Offline + degrade-never-crash: if Pi
// is absent the tool emits a degraded JSON result and exits 3, never a panic.
//
// Exit codes: 0 = ran (even if stopped at a limit; `stopped` says which); 2 = request/
// usage error caught before launch; 3 = Pi/model unavailable (degrade).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"becky-go/internal/pirun"
)

// exit codes (becky house convention).
const (
	exitOK      = 0
	exitUsage   = 2
	exitDegrade = 3
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run is main split out so it is testable and returns an exit code instead of calling
// os.Exit directly. It never panics; every error path returns a code + JSON/plain output.
func run(argv []string) int {
	fs := flag.NewFlagSet("becky-harness", flag.ContinueOnError)
	var (
		requestPath  = fs.String("request", "", "path to a request JSON document")
		goal         = fs.String("goal", "", "the agent's goal (synthesizes a request with defaults)")
		target       = fs.String("target", "", "file/folder the workflow operates on")
		toolsCSV     = fs.String("tools", "", "comma-separated becky tool allowlist (default-deny)")
		model        = fs.String("model", "", "model id, e.g. sonnet (with --goal)")
		auth         = fs.String("auth", "", "subscription | api-key | local (default subscription)")
		builtin      = fs.String("builtin-tools", "", "none | read-only | full (default none)")
		runDir       = fs.String("run-dir", "", "where the manifest/extension/transcript are written")
		asJSON       = fs.Bool("json", false, "emit the result as JSON (default plain report)")
		dryRun       = fs.Bool("dry-run", false, "build the manifest + plan, run NOTHING")
		emitOmnigent = fs.Bool("emit-omnigent", false, "write only the manifest (for becky-omni) and exit")
	)
	if err := fs.Parse(argv); err != nil {
		fmt.Fprintln(os.Stderr, "harness: bad flags:", err)
		return exitUsage
	}

	req, err := loadRequest(*requestPath, *goal, *target, *toolsCSV, *model, *auth, *builtin, *dryRun)
	if err != nil {
		fmt.Fprintln(os.Stderr, "harness: request error:", err)
		return exitUsage
	}

	catalog := catalogSet()
	al, err := pirun.BuildAllowlist(req, catalog)
	if err != nil {
		fmt.Fprintln(os.Stderr, "harness: allowlist error:", err)
		return exitUsage
	}
	manifest, err := pirun.BuildManifest(req, al)
	if err != nil {
		fmt.Fprintln(os.Stderr, "harness: manifest error:", err)
		return exitUsage
	}

	dir := resolveRunDir(*runDir, req.Goal)
	manifestPath, extPath, err := writeArtifacts(dir, manifest, req, al)
	if err != nil {
		fmt.Fprintln(os.Stderr, "harness: could not write run artifacts:", err)
		return exitUsage
	}

	// --emit-omnigent: the generator half of the harness<->omni reconciliation. Write the
	// manifest (the contract becky-omni consumes) and stop — no agent run (SPEC-OMNIGENT §2.3).
	if *emitOmnigent {
		out := emitResult(req, manifest, manifestPath, extPath)
		emit(out, *asJSON)
		return exitOK
	}

	// --dry-run: build everything, print the plan, run NOTHING (SPEC §7.3).
	if req.DryRun {
		out := dryRunResult(req, manifest, manifestPath, extPath)
		emit(out, *asJSON)
		return exitOK
	}

	// Real run path. In this cloud build the PiRunner is a stub; the local agent wires the
	// real one. If Pi isn't on PATH, degrade (exit 3) with a one-line fix — never crash.
	if pirun.ResolveBin() == "" {
		out := degradedResultDoc(req, manifest, manifestPath, extPath,
			"pi CLI not found on PATH — install Pi then run: pi  (and /login once)")
		emit(out, *asJSON)
		fmt.Fprintln(os.Stderr, "Harness: Pi is not installed. Install it, then run `pi` and `/login` once.")
		return exitDegrade
	}

	spec := toPiSpec(req, al, dir)
	ctx, cancel := withTimeout(req.Limits.TimeoutSec)
	defer cancel()

	// runner is the stub in the cloud build; the local agent supplies a real PiRunner.
	runner := pirun.PiRunner(&pirun.StubRunner{Result: pirun.PiResult{
		Degraded:      true,
		DegradeReason: "pi runner not wired in this build (local agent supplies a real PiRunner)",
		Stopped:       "error",
	}})
	res, runErr := pirun.Run(ctx, runner, spec, nil)

	out := finalResult(req, manifest, res, manifestPath, extPath)
	emit(out, *asJSON)
	if res.Degraded || runErr != nil {
		fmt.Fprintln(os.Stderr, "Harness:", out.DegradeReason)
		return exitDegrade
	}
	return exitOK
}

// withTimeout returns a context with the requested wall-clock budget (or no deadline
// when timeoutSec <= 0).
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
	return filepath.Join(os.TempDir(), ".harness", slug+"-"+stamp)
}

// writeArtifacts writes the manifest.json + the generated Pi extension into dir, returning
// their paths. A write failure is a usage-level error (the run cannot proceed safely).
func writeArtifacts(dir string, m pirun.Manifest, req pirun.Request, al pirun.Allowlist) (string, string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("create run dir: %w", err)
	}
	mb, err := pirun.MarshalManifest(m)
	if err != nil {
		return "", "", err
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(manifestPath, mb, 0o644); err != nil {
		return "", "", fmt.Errorf("write manifest: %w", err)
	}
	spec := toPiSpec(req, al, dir)
	extPath, err := pirun.GenerateExtension(spec)
	if err != nil {
		return "", "", err
	}
	return manifestPath, extPath, nil
}

// toPiSpec assembles a PiSpec from a validated request + its allowlist, generating one
// ToolSpec per permitted tool (sorted, from the catalog descriptions).
func toPiSpec(req pirun.Request, al pirun.Allowlist, dir string) pirun.PiSpec {
	descs := catalogDescriptions()
	var tools []pirun.ToolSpec
	for _, name := range al.Names() {
		tools = append(tools, pirun.ToolSpec{
			Name:        name,
			Label:       name,
			Description: descs[name],
			FixedFlags:  al.FixedFlags(name),
		})
	}
	builtin := req.BuiltinTools
	if builtin == "" {
		builtin = pirun.BuiltinNone
	}
	return pirun.PiSpec{
		Goal:           req.Goal,
		Target:         req.Target,
		Tools:          tools,
		BuiltinTools:   builtin,
		Provider:       req.Model.Provider,
		Model:          req.Model.ID,
		BaseURL:        req.Model.BaseURL,
		Auth:           req.Auth,
		Skills:         req.Skills,
		PromptTemplate: req.PromptTemplate,
		SystemAppend:   req.SystemAppend,
		MaxTurns:       req.Limits.MaxTurns,
		MaxBudgetUSD:   req.Limits.MaxBudgetUSD,
		TimeoutSec:     req.Limits.TimeoutSec,
		DryRun:         req.DryRun,
		RunDir:         dir,
	}
}

// loadRequest reads a request from --request, else synthesizes one from the flags. The
// synthesized form covers the common one-liner case (SPEC §2.2).
func loadRequest(path, goal, target, toolsCSV, model, auth, builtin string, dryRun bool) (pirun.Request, error) {
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return pirun.Request{}, fmt.Errorf("read request file: %w", err)
		}
		req, err := pirun.ParseRequest(data)
		if err != nil {
			return pirun.Request{}, err
		}
		if dryRun {
			req.DryRun = true
		}
		return req, nil
	}
	if strings.TrimSpace(goal) == "" {
		return pirun.Request{}, fmt.Errorf("either --request or --goal is required")
	}
	var tools []pirun.ToolRequest
	for _, t := range strings.Split(toolsCSV, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			tools = append(tools, pirun.ToolRequest{Tool: t})
		}
	}
	req := pirun.Request{
		Schema:       pirun.SchemaRequest,
		Goal:         goal,
		Target:       target,
		Tools:        tools,
		BuiltinTools: builtin,
		Model:        pirun.ModelSpec{ID: model},
		Auth:         auth,
		DryRun:       dryRun,
	}
	// Validate the synthesized request through the same gate as a file request.
	data, _ := json.Marshal(req)
	return pirun.ParseRequest(data)
}
