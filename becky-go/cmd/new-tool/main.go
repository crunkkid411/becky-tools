// becky-new-tool — the deterministic tool-build FACTORY.
//
// becky-new-tool is a meta-tool: its input is a pain-point ("I need a tool that does
// ___") and its output is a NEW becky tool plus its spec, tests, eval result, and
// integration — produced by a fixed, resumable PIPELINE (a state machine over
// state.json). Control flow is pure Go; an LLM/agent runs ONLY inside specific stages,
// and the expensive headless Claude agent runs in as few stages as possible (S5 build,
// optionally S4 spec). See SPEC-BECKY-NEW-TOOL.md.
//
//	becky-new-tool --pain "<text>" [--run-dir <dir>] [--resume] [--from/--until/--force <stage>]
//	  [--approve gateA|gateB|gateC] [--yes] [--spec-backend claude|cheap]
//	  [--review-backend local|openrouter] [--model <build-model>] [--max-turns N]
//	  [--max-budget-usd D] [--claude-budget-usd D] [--max-build-iterations N]
//	  [--offline] [--bin <dir>] [--verbose]
//
// stdout: the run-state JSON. stderr: plain-English stage headlines. Exit 0 on a
// completed run OR a clean pause at a gate; non-zero only on a hard error.
//
// Fact-Forcing-Gate self-certification:
//  1. Callers: invoked as becky-new-tool / `go run ./cmd/new-tool`; package entry.
//  2. No-dup: the factory's CLI; no existing equivalent (becky/becky-eval/becky-ask
//     are different tools).
//  3. Data shape: parses CLI flags, sets up the run dir, constructs the orchestrator;
//     writes state.json + a run.log into the run dir.
//  4. Verbatim instruction: "BUILD: a deterministic state-machine orchestrator
//     `cmd/new-tool/` over a resumable `state.json`".
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

	"becky-go/internal/beckyio"
)

func main() {
	var (
		pain        = flag.String("pain", "", "the pain-point text (\"I need a tool that ...\")")
		intakeFile  = flag.String("intake-file", "", "JSON intake record (alternative to --pain)")
		runDir      = flag.String("run-dir", "", "run directory (default .factory/<slug>-<date>)")
		resume      = flag.Bool("resume", false, "continue an existing run from its cursor")
		from        = flag.String("from", "", "start at this stage (e.g. S4)")
		until       = flag.String("until", "", "stop after this stage")
		force       = flag.String("force", "", "re-run this one stage (clears its state)")
		approve     = flag.String("approve", "", "comma-separated gates to auto-approve: gateA,gateB,gateC")
		yes         = flag.Bool("yes", false, "auto-pass gates B and C (trusted runs)")
		specBackend = flag.String("spec-backend", "claude", "S4 spec author: claude|cheap")
		reviewBE    = flag.String("review-backend", "local", "S7 reviewer: local|openrouter")
		model       = flag.String("model", "sonnet", "S5 Claude build model (e.g. sonnet, opus, a full id)")
		fallback    = flag.String("fallback-model", "haiku", "Claude fallback model if primary is overloaded/retired")
		maxTurns    = flag.Int("max-turns", 60, "per Claude call: hard turn cap")
		perBudget   = flag.Float64("max-budget-usd", 5.0, "per Claude call: hard spend cap (USD)")
		runBudget   = flag.Float64("claude-budget-usd", 0, "whole-run Claude budget (0 = none)")
		maxIters    = flag.Int("max-build-iterations", 3, "S5<->S6 loop cap")
		offline     = flag.Bool("offline", false, "skip all web/remote; local models only")
		binDir      = flag.String("bin", "", "where the becky-*.exe live (default: becky-go/bin)")
		serverURL   = flag.String("server-url", "", "local llama-server chat endpoint for the cheap/local backend")
		runBuildAll = flag.Bool("run-build-all", false, "S10: run the FULL build-all-tools.bat (default: scoped go build only)")
		verbose     = flag.Bool("verbose", false, "progress to stderr")
	)
	flag.Parse()

	// Resolve the build root from this executable / cwd. becky-go is the module root.
	buildRoot := resolveBuildRoot()
	if buildRoot == "" {
		beckyio.Fatalf("cannot locate the becky-go build root (run from inside becky-tools or becky-go)")
	}

	// Resolve intake text/slug to fix the run dir.
	painText, preSlug, err := resolveIntake(*pain, *intakeFile)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}

	if !*resume && *from == "" && *force == "" && painText == "" {
		beckyio.Fatalf("need --pain \"<text>\" or --intake-file <json> (or --resume an existing --run-dir)")
	}

	rd := *runDir
	if rd == "" {
		if preSlug == "" {
			preSlug = deriveSlug(painText)
		}
		rd = filepath.Join(filepath.Dir(buildRoot), ".factory", preSlug+"-"+time.Now().Format("2006-01-02"))
	}
	if err := os.MkdirAll(rd, 0o755); err != nil {
		beckyio.Fatalf("create run dir %s: %v", rd, err)
	}

	// Open a run log inside the run dir.
	logF, _ := os.OpenFile(filepath.Join(rd, "run.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if logF != nil {
		defer logF.Close()
	}

	o := &orchestrator{
		runDir:        rd,
		buildRoot:     buildRoot,
		binDir:        resolveBinDir(*binDir, buildRoot),
		testAsset:     resolveTestAsset(buildRoot),
		briefingPath:  filepath.Join(filepath.Dir(buildRoot), "BUILD-AGENT-BRIEFING.md"),
		from:          strings.ToUpper(*from),
		until:         strings.ToUpper(*until),
		forceStage:    strings.ToUpper(*force),
		approve:       parseApprovals(*approve),
		yes:           *yes,
		resume:        *resume,
		offline:       *offline,
		buildModel:    *model,
		fallbackModel: *fallback,
		specBackend:   *specBackend,
		maxTurns:      *maxTurns,
		perCallBudget: *perBudget,
		runBudget:     *runBudget,
		maxBuildIters: *maxIters,
		runBuildAll:   *runBuildAll,
		cheap:         resolveCheapBackend(*reviewBE, *offline, *serverURL),
		specTimeout:   8 * time.Minute,
		buildTimeout:  40 * time.Minute,
		verbose:       *verbose,
		logFile:       logF,
	}

	// Seed meta on a fresh run so S1 has the pain text + slug.
	if err := seedFreshRun(o, painText, preSlug); err != nil {
		beckyio.Fatalf("%v", err)
	}

	ctx := context.Background()
	if err := o.run(ctx); err != nil {
		beckyio.Fatalf("%v", err)
	}
}

// resolveIntake returns the pain text + an optional pre-slug from --pain or an intake
// file. Stdin is also accepted when --pain is "-".
func resolveIntake(pain, intakeFile string) (string, string, error) {
	if intakeFile != "" {
		data, err := os.ReadFile(intakeFile)
		if err != nil {
			return "", "", fmt.Errorf("read intake file: %w", err)
		}
		var in struct {
			Slug       string `json:"slug"`
			Capability string `json:"capability"`
			Pain       string `json:"pain"`
		}
		if err := json.Unmarshal(data, &in); err != nil {
			return "", "", fmt.Errorf("parse intake file: %w", err)
		}
		text := firstNonEmptyStr(in.Pain, in.Capability)
		return text, sanitizeSlug(in.Slug), nil
	}
	if pain == "-" {
		buf, _ := os.ReadFile(os.Stdin.Name())
		return strings.TrimSpace(string(buf)), "", nil
	}
	return strings.TrimSpace(pain), "", nil
}

// seedFreshRun writes the initial meta (pain + slug) if state.json doesn't have it yet.
func seedFreshRun(o *orchestrator, painText, preSlug string) error {
	s, err := loadState(o.runDir)
	if err != nil {
		return err
	}
	if s.Meta.Slug == "" {
		slug := preSlug
		if slug == "" {
			slug = deriveSlug(painText)
		}
		s.Meta.Slug = slug
	}
	if s.Meta.Pain == "" {
		s.Meta.Pain = painText
	}
	if s.Meta.RunDir == "" {
		s.Meta.RunDir = o.runDir
	}
	return s.save()
}

// resolveBuildRoot finds the becky-go module root from the executable dir or cwd.
func resolveBuildRoot() string {
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		// .../becky-go/bin/becky-new-tool.exe -> .../becky-go
		candidates = append(candidates, filepath.Dir(filepath.Dir(exe)))
		candidates = append(candidates, filepath.Dir(exe))
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, wd, filepath.Join(wd, "becky-go"))
	}
	// Known absolute fallback for this machine.
	candidates = append(candidates, `X:\AI-2\becky-tools\becky-go`)
	for _, c := range candidates {
		if fileExists(filepath.Join(c, "go.mod")) && dirExists(filepath.Join(c, "cmd")) {
			return c
		}
	}
	return ""
}

// resolveBinDir prefers --bin, else becky-go/bin.
func resolveBinDir(flagBin, buildRoot string) string {
	if flagBin != "" {
		return flagBin
	}
	return filepath.Join(buildRoot, "bin")
}

// resolveTestAsset returns the real test asset path (the 45.2s test.mp4).
func resolveTestAsset(buildRoot string) string {
	return filepath.Join(filepath.Dir(buildRoot), "test.mp4")
}

// resolveCheapBackend picks the cheap/local transport for S1/S2/S3/S7. The MODEL id
// is filled by the protocol in S2 (adoptCheap); here we only choose the transport.
//
// PRIVACY: openrouter (a remote vendor) is used ONLY when the user explicitly chooses
// --review-backend openrouter AND a key is set AND not --offline. The DEFAULT
// (--review-backend local) NEVER silently routes to a remote vendor: with a
// --server-url it uses the local llama-server; without one it degrades to the
// deterministic path (Kind="none"). This honors the spec's "default reviewer = local;
// remote is opt-in" rule so case context can't leak to a remote model by accident.
func resolveCheapBackend(reviewBE string, offline bool, serverURL string) CheapBackend {
	if reviewBE == "openrouter" && !offline && os.Getenv("OPENROUTER_API_KEY") != "" {
		return CheapBackend{Kind: "openrouter"}
	}
	// Local transport: only usable if a server URL is provided (else "none" -> det fallback).
	if serverURL != "" {
		return CheapBackend{Kind: "local", ServerURL: serverURL}
	}
	return CheapBackend{Kind: "none"}
}

// parseApprovals turns "gateA,gateB" into a map[A:true,B:true].
func parseApprovals(s string) map[string]bool {
	m := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(strings.ToUpper(strings.TrimPrefix(strings.ToLower(part), "gate")))
		if part != "" {
			m[part] = true
		}
	}
	return m
}
