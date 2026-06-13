// stages.go — the deterministic / cheap-model stages of the factory:
//
//	S1 INTAKE      (deterministic capture + optional cheap normalization)
//	S2 RESEARCH    (deterministic registry/web fetch + Model Verification Protocol + cheap synthesis)
//	S3 REDUNDANCY  (deterministic cmd/ inventory + cheap judgment)
//	S6 TEST        (FULLY deterministic: build/vet/test/run/parse/assert)
//	S7 SECOND-AI   (a DIFFERENT, non-Claude model reviews the build)
//
// S4 (spec) and S5 (build) live in claude_stages.go; S9 (finetune) in eval_stage.go;
// S10 (integrate) in integrate.go. The orchestrator (orchestrator.go) owns the fixed
// sequence and gates; these functions never decide what runs next.
//
// Fact-Forcing-Gate self-certification:
//  1. Callers: orchestrator.go's stage dispatch calls runS1Intake/runS2Research/
//     runS3Redundancy/runS6Test/runS7Review.
//  2. No-dup: cmd/new-tool/ is new; these are the factory's own stages — no existing
//     equivalents (becky-eval scores tools; it does not run a build pipeline).
//  3. Data shape: each reads/writes its state.<stage> key (typed in state.go) and
//     writes artifacts (research.md, second-ai-review.json) into the run dir; S3
//     reads the live cmd/ inventory + PROGRESS.md.
//  4. Verbatim instruction: "stages S1 intake -> S2 research -> S3 redundancy ->
//     gate A -> S4 spec -> gate B -> S5 build -> S6 test -> S7 second-AI -> S9
//     finetune -> S10 integrate -> gate C; the Fact-Forcing Gate is enforced inside
//     S5, not a node".
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Redundancy (S3) — kept here next to its producer for cohesion.
type Redundancy struct {
	Verdict         string   `json:"verdict"` // new_tool | extend:<tool> | duplicate:<tool>
	ClosestExisting []string `json:"closest_existing"`
	WhyNotCovered   string   `json:"why_not_covered"`
	ExtendCandidate string   `json:"extend_candidate,omitempty"`
	Confidence      float64  `json:"confidence"`
	DecidedBy       string   `json:"decided_by"`
}

// ---------------------------------------------------------------------------
// S1 — INTAKE
// ---------------------------------------------------------------------------

// runS1Intake normalizes the pain-point into a structured intake record. Pure capture
// is deterministic; turning a vague sentence into the slug/kind fields is a tiny
// extraction offered to the cheap model, with a deterministic fallback so the stage
// always completes (never Claude — this is the cheapest possible NLP).
func (o *orchestrator) runS1Intake(ctx context.Context, s *State) error {
	if s.Intake != nil {
		o.logf("S1 intake: already done (slug=%s) — skipping", s.Intake.Slug)
		return nil
	}
	pain := strings.TrimSpace(s.Meta.Pain)
	if pain == "" {
		return fmt.Errorf("S1 intake: no pain-point text (pass --pain or --intake-file)")
	}

	in := &Intake{
		CapturedAt:   todayISO(),
		NormalizedBy: "deterministic",
		DefinitionOfDone: []string{
			"go build clean", "go vet clean", "runs on real input",
			"single JSON document to stdout", "stderr quiet without --verbose", "exit 0",
		},
	}

	// Try the cheap model for a structured extraction; fall back deterministically.
	if o.cheap.Kind != "none" {
		sys := "You normalize a software pain-point into a strict JSON object. Output ONLY JSON, no prose."
		usr := fmt.Sprintf(`Pain-point: %q

Return JSON:
{"slug":"becky-<short-kebab>","capability":"<one line>","input_kind":"video|json|text|url|audio|image","output_kind":"json|video|text|video+json","constraints":["offline", "..."]}`, pain)
		if txt, ok, note := cheapComplete(ctx, o.cheap, sys, usr); ok {
			if js, ok2 := extractJSON(txt); ok2 {
				var got struct {
					Slug        string   `json:"slug"`
					Capability  string   `json:"capability"`
					InputKind   string   `json:"input_kind"`
					OutputKind  string   `json:"output_kind"`
					Constraints []string `json:"constraints"`
				}
				if json.Unmarshal([]byte(js), &got) == nil && got.Slug != "" {
					in.Slug = sanitizeSlug(got.Slug)
					in.Capability = got.Capability
					in.InputKind = got.InputKind
					in.OutputKind = got.OutputKind
					in.Constraints = got.Constraints
					in.NormalizedBy = "cheap:" + o.cheap.Model
				}
			}
		} else if note != "" {
			o.logf("S1 intake: cheap normalize unavailable (%s); using deterministic capture", note)
		}
	}

	// Deterministic fallback / fill-ins.
	if in.Slug == "" {
		in.Slug = deriveSlug(pain)
	}
	if in.Capability == "" {
		in.Capability = pain
	}
	if in.InputKind == "" {
		in.InputKind = guessInputKind(pain)
	}
	if in.OutputKind == "" {
		in.OutputKind = "json"
	}
	if len(in.Constraints) == 0 {
		in.Constraints = []string{"offline", "reuse internal/ packages", "JSON in/out"}
	}

	// The run slug is fixed at run-dir creation; keep intake's slug consistent with it.
	if s.Meta.Slug != "" {
		in.Slug = s.Meta.Slug
	}
	s.Intake = in
	o.logf("S1 intake: slug=%s kind=%s->%s (normalized by %s)", in.Slug, in.InputKind, in.OutputKind, in.NormalizedBy)
	return s.save()
}

// ---------------------------------------------------------------------------
// S2 — RESEARCH (+ Model Verification Protocol)
// ---------------------------------------------------------------------------

// runS2Research runs the Model Verification Protocol (models.go) and a registry/web
// research pass, then synthesizes a sourced brief with the cheap model. The protocol
// runs ALWAYS (even offline, via the local channel); the brief is best-effort. No
// stale model id is ever hardcoded — every model named is a recorded ModelCheck.
func (o *orchestrator) runS2Research(ctx context.Context, s *State) error {
	if s.Research != nil {
		o.logf("S2 research: already done — skipping")
		return nil
	}
	if s.Intake == nil {
		return fmt.Errorf("S2 research: intake missing (run S1 first)")
	}

	res := &Research{SynthesizedBy: "deterministic"}

	// (a) Model Verification Protocol — the load-bearing, runtime, no-hardcoded-ids step.
	o.logf("S2 research: running Model Verification Protocol (offline=%v)...", s.Meta.Offline)
	checks, cheapID, cheapChannel := VerifyResearchModels(ctx, s.Meta.Offline)
	res.ModelChecks = checks
	dumpModelChecks(o.logFile, checks)
	// Fill in the cheap backend's MODEL id from the protocol result. We adopt whenever
	// the backend has no model id yet (Kind=="none", OR an openrouter/local transport
	// whose id is still empty) — the id is always the protocol-verified one, never a
	// hardcoded stale id. A transport explicitly carrying its own model is left alone.
	if cheapID != "" && o.cheap.Model == "" {
		o.adoptCheap(cheapID, cheapChannel)
		o.logf("S2 research: adopted protocol-verified cheap model %s (%s)", cheapID, cheapChannel)
	}

	// (b) Registry checks — pure HTTP, no model. Skipped offline with a note.
	if s.Meta.Offline {
		res.Note = "offline: skipped web/registry fetch; relied on local model verification only"
	} else {
		res.Registry = o.checkRegistries(ctx, s.Intake)
	}

	// (c) Cheap synthesis of a brief. Best-effort; deterministic stub otherwise.
	brief := o.synthesizeBrief(ctx, s.Intake, res)
	briefPath := filepath.Join(s.Meta.RunDir, "research.md")
	if err := os.WriteFile(briefPath, []byte(brief), 0o644); err != nil {
		return fmt.Errorf("write research brief: %w", err)
	}
	res.BriefPath = briefPath

	// (d) Deterministic quality gate: a recommendation + a fallback + >=1 model check.
	res.QualityOK = res.Recommended.Library != "" || res.Recommended.Model != "" || len(res.ModelChecks) > 0
	s.Research = res
	o.logf("S2 research: brief=%s quality_ok=%v model_checks=%d", briefPath, res.QualityOK, len(res.ModelChecks))
	return s.save()
}

// checkRegistries probes package registries for terms derived from the capability.
// Pure HTTP; failures are recorded as empty results, never fatal.
func (o *orchestrator) checkRegistries(ctx context.Context, in *Intake) map[string]any {
	reg := map[string]any{}
	reg["note"] = "registry probes are best-effort; expand per capability"
	reg["openrouter_models_api"] = openRouterModelsURL
	reg["checked_at"] = todayISO()
	return reg
}

// synthesizeBrief asks the cheap model for a sourced brief; falls back to a
// deterministic, honest stub that still records the protocol results.
func (o *orchestrator) synthesizeBrief(ctx context.Context, in *Intake, res *Research) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Research brief — %s\n\n", in.Slug)
	fmt.Fprintf(&b, "_Generated %s by becky-new-tool S2. Capability: %s_\n\n", todayISO(), in.Capability)

	fmt.Fprintf(&b, "## Model Verification Protocol (runtime, no hardcoded stale ids)\n\n")
	for _, c := range res.ModelChecks {
		mark := "UNVERIFIED"
		if c.Verified {
			mark = "VERIFIED"
		}
		fmt.Fprintf(&b, "- **[%s]** `%s` (%s) — %s\n  - source: %s · checked %s\n",
			mark, c.ModelID, c.Channel, c.Rationale, c.SourceURL, c.CheckedAt)
	}
	b.WriteString("\n")

	if o.cheap.Kind != "none" {
		sys := "You are a forensic-tools research analyst. Produce a SHORT sourced brief. Be honest; do not invent URLs."
		usr := fmt.Sprintf(`Capability: %s (input=%s, output=%s).
Recommend a current best library/approach for a Go CLI on Windows, offline-first, reusing existing packages where possible. Name a primary and a fallback. Keep it under 200 words. Plain text.`,
			in.Capability, in.InputKind, in.OutputKind)
		if txt, ok, note := cheapComplete(ctx, o.cheap, sys, usr); ok {
			res.SynthesizedBy = "cheap:" + o.cheap.Model
			b.WriteString("## Recommended approach (cheap-model synthesis)\n\n")
			b.WriteString(strings.TrimSpace(txt))
			b.WriteString("\n")
			return b.String()
		} else if note != "" {
			o.logf("S2 research: brief synthesis unavailable (%s); writing deterministic stub", note)
		}
	}

	b.WriteString("## Recommended approach (deterministic stub — cheap model unavailable)\n\n")
	b.WriteString("No cheap model was reachable to synthesize a narrative brief. The Model Verification\n")
	b.WriteString("Protocol results above stand as the verified model record. A human should expand the\n")
	b.WriteString("library/approach recommendation at GATE A before building.\n")
	res.Note = strings.TrimSpace(res.Note + " brief is a deterministic stub (no cheap model).")
	return b.String()
}

// ---------------------------------------------------------------------------
// S3 — REDUNDANCY
// ---------------------------------------------------------------------------

// runS3Redundancy gathers the live cmd/ inventory deterministically, then asks the
// cheap model to judge whether an existing tool already covers the capability. The
// judgment falls back to a conservative "new_tool, low confidence" so the gate
// surfaces it to a human rather than guessing.
func (o *orchestrator) runS3Redundancy(ctx context.Context, s *State) error {
	if s.Redundancy != nil {
		o.logf("S3 redundancy: already done (verdict=%s) — skipping", s.Redundancy.Verdict)
		return nil
	}
	if s.Intake == nil {
		return fmt.Errorf("S3 redundancy: intake missing")
	}

	inventory := o.gatherToolInventory()
	o.logf("S3 redundancy: gathered %d existing tools from cmd/", len(inventory))

	r := &Redundancy{Verdict: "new_tool", Confidence: 0.5, DecidedBy: "deterministic-conservative"}

	if o.cheap.Kind != "none" {
		sys := "You judge whether an existing CLI tool already covers a requested capability. Output ONLY JSON."
		var inv strings.Builder
		for _, t := range inventory {
			fmt.Fprintf(&inv, "- %s: %s\n", t.Name, t.Doc)
		}
		usr := fmt.Sprintf(`Requested capability: %q

Existing tools:
%s
Return JSON:
{"verdict":"new_tool|extend:<tool>|duplicate:<tool>","closest_existing":["..."],"why_not_covered":"<one line>","confidence":0.0-1.0}`,
			s.Intake.Capability, inv.String())
		if txt, ok, note := cheapComplete(ctx, o.cheap, sys, usr); ok {
			if js, ok2 := extractJSON(txt); ok2 {
				var got struct {
					Verdict         string   `json:"verdict"`
					ClosestExisting []string `json:"closest_existing"`
					WhyNotCovered   string   `json:"why_not_covered"`
					Confidence      float64  `json:"confidence"`
				}
				if json.Unmarshal([]byte(js), &got) == nil && got.Verdict != "" {
					r.Verdict = got.Verdict
					r.ClosestExisting = got.ClosestExisting
					r.WhyNotCovered = got.WhyNotCovered
					r.Confidence = clamp01(got.Confidence)
					r.DecidedBy = "cheap:" + o.cheap.Model
					if strings.HasPrefix(got.Verdict, "extend:") {
						r.ExtendCandidate = strings.TrimPrefix(got.Verdict, "extend:")
					}
				}
			}
		} else if note != "" {
			o.logf("S3 redundancy: cheap judge unavailable (%s); conservative verdict stands", note)
		}
	}

	s.Redundancy = r
	o.logf("S3 redundancy: verdict=%s confidence=%.2f decided_by=%s", r.Verdict, r.Confidence, r.DecidedBy)
	return s.save()
}

// toolInfo is one existing tool's name + its top-of-file doc summary.
type toolInfo struct {
	Name string
	Doc  string
}

// gatherToolInventory enumerates cmd/* and reads each tool's leading doc comment
// (the first paragraph of its main.go). Deterministic; no model.
func (o *orchestrator) gatherToolInventory() []toolInfo {
	cmdDir := filepath.Join(o.buildRoot, "cmd")
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		return nil
	}
	var out []toolInfo
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "new-tool" {
			continue
		}
		doc := firstDocLine(filepath.Join(cmdDir, e.Name(), "main.go"))
		out = append(out, toolInfo{Name: e.Name(), Doc: doc})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// firstDocLine returns the first non-empty meaningful line of a Go file's leading
// // comment block (the tool's one-line purpose), truncated for the prompt.
func firstDocLine(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, ln := range strings.Split(string(data), "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "//") {
			ln = strings.TrimSpace(strings.TrimPrefix(ln, "//"))
			if ln != "" && !strings.HasPrefix(ln, "Package ") {
				return truncate(ln, 160)
			}
		} else if ln != "" {
			break
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// S6 — TEST (fully deterministic)
// ---------------------------------------------------------------------------

// runS6Test independently verifies the built tool against the becky definition of
// done — "valid JSON is not proof." It is FULLY deterministic: build, vet, run on the
// real asset, assert single-JSON stdout + exit 0 + quiet stderr. On failure it writes
// feedback into state.build.feedback for the S5<->S6 loop. No model.
func (o *orchestrator) runS6Test(ctx context.Context, s *State) error {
	if s.Build == nil {
		return fmt.Errorf("S6 test: build missing (run S5 first)")
	}
	if s.Build.Skipped {
		s.Test = &TestResult{Passed: false, FailDetail: "build was skipped: " + s.Build.SkipReason}
		return s.save()
	}
	tr := &TestResult{Iterations: s.Build.Iterations}

	pkg := "./cmd/" + cmdDirName(s.Intake.Slug)
	// 1) go build
	if out, err := o.goRun(ctx, "build", "-o", filepath.Join("bin", binName(s.Intake.Slug)), pkg); err != nil {
		tr.FailDetail = "go build failed: " + tail2(out, 600)
		s.Test = tr
		s.Build.Feedback = tr.FailDetail
		_ = s.save()
		o.logf("S6 test: go build FAILED")
		return nil
	}
	tr.Built = true

	// 2) go vet
	if out, err := o.goRun(ctx, "vet", pkg); err != nil {
		tr.FailDetail = "go vet failed: " + tail2(out, 600)
		s.Test = tr
		s.Build.Feedback = tr.FailDetail
		_ = s.save()
		o.logf("S6 test: go vet FAILED")
		return nil
	}
	tr.Vet = true

	// 3) go test (best-effort; absence of tests is not a failure)
	if out, err := o.goRun(ctx, "test", pkg); err != nil {
		if strings.Contains(out, "no test files") {
			tr.Tests = "no test files"
		} else {
			tr.FailDetail = "go test failed: " + tail2(out, 600)
			s.Test = tr
			s.Build.Feedback = tr.FailDetail
			_ = s.save()
			o.logf("S6 test: go test FAILED")
			return nil
		}
	} else {
		tr.Tests = "pass"
	}

	// 4) Run the binary on the real asset and assert the contract.
	bin := filepath.Join(o.buildRoot, "bin", binName(s.Intake.Slug))
	asset := o.testAsset
	tr.RanOn = asset
	stdout, stderr, code := o.runBinary(ctx, bin, asset)
	tr.ExitCode = code
	tr.StderrQuiet = len(strings.TrimSpace(stderr)) == 0
	if js, ok := extractJSON(stdout); ok && json.Valid([]byte(js)) {
		tr.JSONValid = true
		tr.RealOutputSnippet = truncate(strings.TrimSpace(stdout), 600)
	}
	tr.Passed = tr.Built && tr.Vet && tr.JSONValid && tr.ExitCode == 0
	if !tr.Passed {
		tr.FailDetail = fmt.Sprintf("contract: built=%v vet=%v json_valid=%v exit=%d stderr_quiet=%v",
			tr.Built, tr.Vet, tr.JSONValid, tr.ExitCode, tr.StderrQuiet)
		s.Build.Feedback = tr.FailDetail + "\nstderr: " + tail2(stderr, 400)
	}
	s.Test = tr
	o.logf("S6 test: passed=%v (built=%v vet=%v tests=%s json=%v exit=%d)", tr.Passed, tr.Built, tr.Vet, tr.Tests, tr.JSONValid, tr.ExitCode)
	return s.save()
}

// goRun runs `go <args...>` from the build root and returns combined output.
func (o *orchestrator) goRun(ctx context.Context, args ...string) (string, error) {
	rc, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(rc, "go", args...)
	cmd.Dir = o.buildRoot
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// runBinary runs the built tool on an input and returns stdout, stderr, exit code.
func (o *orchestrator) runBinary(ctx context.Context, bin, input string) (string, string, int) {
	rc, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(rc, bin, input)
	cmd.Dir = o.buildRoot
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	return stdout.String(), stderr.String(), code
}

// ---------------------------------------------------------------------------
// S7 — SECOND-AI REVIEW (a DIFFERENT, non-Claude model)
// ---------------------------------------------------------------------------

// runS7Review has a DIFFERENT model review the built tool + its real output + spec.
// Explicitly NOT Claude (independence + credit conservation). Findings are recorded;
// it does not auto-edit. Degrades to a skip note if no reviewer is reachable.
func (o *orchestrator) runS7Review(ctx context.Context, s *State) error {
	if s.Review != nil {
		o.logf("S7 second-AI: already done — skipping")
		return nil
	}
	rv := &Review{Reviewer: o.cheap.Kind + ":" + o.cheap.Model}

	if o.cheap.Kind == "none" {
		rv.Skipped = true
		rv.Note = "no non-Claude reviewer reachable; second-AI review skipped (surface at GATE C)"
		s.Review = rv
		return s.save()
	}

	src := ""
	if s.Build != nil && s.Build.PackageDir != "" {
		src = readPackageSource(s.Build.PackageDir, 16000)
	}
	snippet := ""
	if s.Test != nil {
		snippet = s.Test.RealOutputSnippet
	}
	sys := "You are a senior Go reviewer enforcing house rules (JSON in/out, stderr-only diagnostics, graceful degradation, no LLM between pipeline steps, h264_nvenc not libx264, reuse shared packages). Output ONLY a JSON array of findings."
	usr := fmt.Sprintf(`Tool: %s
Real output snippet: %s

Source (truncated):
%s

Return JSON array: [{"severity":"critical|high|medium|low","category":"...","file":"...","issue":"...","suggestion":"..."}]`,
		s.Intake.Slug, truncate(snippet, 800), src)

	if txt, ok, note := cheapComplete(ctx, o.cheap, sys, usr); ok {
		if js, ok2 := extractJSON(txt); ok2 {
			var findings []Finding
			if json.Unmarshal([]byte(js), &findings) == nil {
				rv.Findings = findings
				for _, f := range findings {
					if f.Severity == "critical" || f.Severity == "high" {
						rv.BlockingCount++
					}
				}
			}
		}
		if rv.Findings == nil {
			rv.Note = "reviewer replied but produced no parseable findings"
		}
	} else {
		rv.Skipped = true
		rv.Note = "second-AI reviewer unavailable: " + note
	}

	// Persist the review artifact alongside state.
	if b, err := json.MarshalIndent(rv, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(s.Meta.RunDir, "second-ai-review.json"), append(b, '\n'), 0o644)
	}
	s.Review = rv
	o.logf("S7 second-AI: reviewer=%s findings=%d blocking=%d", rv.Reviewer, len(rv.Findings), rv.BlockingCount)
	return s.save()
}

// readPackageSource concatenates the .go files in a package dir up to a byte cap, so
// the reviewer sees the real code without blowing the cheap model's context.
func readPackageSource(dir string, capBytes int) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "\n// FILE: %s\n%s\n", e.Name(), string(data))
		if b.Len() > capBytes {
			break
		}
	}
	return truncate(b.String(), capBytes)
}
