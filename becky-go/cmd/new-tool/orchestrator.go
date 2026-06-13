// orchestrator.go — the deterministic state machine. This is PURE CODE: a fixed
// sequence of stages with explicit gates. No model ever decides what runs next; the
// orchestrator advances the cursor, runs the next stage (which MAY call a model
// internally), checks the gate policy, and either continues or pauses cleanly.
//
// Fixed sequence (SPEC §1):
//
//	S1 -> S2 -> S3 -> [GATE A] -> S4 -> [GATE B] -> S5 <-> S6 (bounded loop)
//	     -> S7 -> S9 -> S10 -> [GATE C]
//
// The Fact-Forcing Gate is NOT a node — it is enforced inside S5 (claude_stages.go).
//
// Fact-Forcing-Gate self-certification:
//  1. Callers: cmd/new-tool/main.go constructs an orchestrator and calls run(ctx).
//  2. No-dup: the factory's own state machine; cmd/becky is a verb-router and
//     cmd/pipeline chains tools — neither is this staged, resumable, gated factory.
//  3. Data shape: owns the orchestrator config struct + the fixed stage sequence +
//     gate logic; reads/writes state.json via the stage methods.
//  4. Verbatim instruction: "BUILD: a deterministic state-machine orchestrator
//     `cmd/new-tool/` ... LLM/agent runs ONLY inside specific stages; control flow is
//     pure code."
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// orchestrator holds the run configuration and the resolved cheap backend. It is the
// single object every stage method hangs off; constructing it is the only place
// config is read.
type orchestrator struct {
	// run inputs
	runDir       string
	buildRoot    string // X:\AI-2\becky-tools\becky-go
	binDir       string // where becky-*.exe live (for becky-eval)
	testAsset    string
	briefingPath string

	// stage/gate control
	from       string
	until      string
	forceStage string
	approve    map[string]bool // gate -> approved
	yes        bool            // auto-pass gates B/C for trusted runs
	resume     bool
	offline    bool

	// model/cost levers
	buildModel    string
	fallbackModel string
	specBackend   string // claude | cheap
	maxTurns      int
	perCallBudget float64
	runBudget     float64
	maxBuildIters int
	runBuildAll   bool

	// cheap backend (S1/S2/S3/S7) — model id is protocol-verified, never hardcoded stale
	cheap CheapBackend

	// timeouts
	specTimeout  time.Duration
	buildTimeout time.Duration

	// io
	verbose bool
	logFile *os.File
}

// stageStep is one node in the fixed sequence: an id, a runner, and an optional gate
// that follows it.
type stageStep struct {
	id   string
	run  func(context.Context, *State) error
	gate string // "" or "A"/"B"/"C": a gate that must pass AFTER this stage
}

// sequence returns the fixed stage list. It is a constant ordering — the heart of
// "control flow is pure code".
func (o *orchestrator) sequence() []stageStep {
	return []stageStep{
		{id: "S1", run: o.runS1Intake},
		{id: "S2", run: o.runS2Research},
		{id: "S3", run: o.runS3Redundancy, gate: "A"},
		{id: "S4", run: o.runS4Spec, gate: "B"},
		{id: "S5", run: o.runS5BuildTestLoop}, // S5<->S6 bounded loop lives here
		{id: "S7", run: o.runS7Review},
		{id: "S9", run: o.runS9Finetune},
		{id: "S10", run: o.runS10Integrate, gate: "C"},
	}
}

// run executes the pipeline from the cursor, honoring --from/--until/--force and the
// gates. It returns nil on a completed run OR a clean pause at a gate; non-nil only on
// a hard error.
func (o *orchestrator) run(ctx context.Context) error {
	s, err := loadState(o.runDir)
	if err != nil {
		return err
	}
	o.seedMeta(s)
	if err := s.save(); err != nil {
		return err
	}

	seq := o.sequence()
	started := o.from == "" // if --from is set, skip until we reach it
	for _, st := range seq {
		if o.from != "" && st.id == o.from {
			started = true
		}
		if !started {
			continue
		}
		// --force <stage>: clear that stage's state so it re-runs.
		if o.forceStage == st.id {
			o.clearStage(s, st.id)
		}

		// Whole-run Claude budget guard: pause before a Claude-heavy stage if exceeded.
		if o.runBudget > 0 && s.Meta.ClaudeCostUSDTotal >= o.runBudget && (st.id == "S4" || st.id == "S5") {
			return o.pause(s, st.id, fmt.Sprintf("run Claude budget $%.2f reached ($%.4f spent); pausing before %s",
				o.runBudget, s.Meta.ClaudeCostUSDTotal, st.id))
		}

		s.Meta.StageCursor = st.id
		_ = s.save()
		o.headline("== %s ==", st.id)
		if err := st.run(ctx, s); err != nil {
			return fmt.Errorf("%s failed: %w", st.id, err)
		}

		// Hard stops that should pause rather than continue.
		if pauseMsg := o.stagePause(s, st.id); pauseMsg != "" {
			return o.pause(s, st.id, pauseMsg)
		}

		// Gate after this stage.
		if st.gate != "" {
			if ok, why := o.gateDecision(s, st.gate); !ok {
				return o.pause(s, st.id, fmt.Sprintf("GATE %s not passed: %s", st.gate, why))
			}
			s.passGate(st.gate)
			_ = s.save()
			o.headline("GATE %s: passed", st.gate)
		}

		if o.until != "" && st.id == o.until {
			o.headline("reached --until %s; stopping", o.until)
			break
		}
	}

	s.Meta.Done = s.gatePassed("C")
	s.Meta.StageCursor = "done"
	if err := s.save(); err != nil {
		return err
	}
	o.headline("run complete (done=%v, claude spend $%.4f)", s.Meta.Done, s.Meta.ClaudeCostUSDTotal)
	emitState(s)
	return nil
}

// runS5BuildTestLoop runs the bounded S5<->S6 loop: build, then test; on a test
// failure, loop back into S5 (resume) until S6 passes or the iteration cap is hit.
func (o *orchestrator) runS5BuildTestLoop(ctx context.Context, s *State) error {
	for i := 0; i < o.maxBuildIters+1; i++ {
		if err := o.runS5Build(ctx, s); err != nil {
			return err
		}
		if s.Build != nil && s.Build.Skipped {
			return nil // can't build (e.g. no claude CLI); S6 will record the skip
		}
		if err := o.runS6Test(ctx, s); err != nil {
			return err
		}
		if s.Test != nil && s.Test.Passed {
			return nil
		}
		// S6 wrote feedback into s.Build.Feedback; loop back into S5 (resume) unless capped.
		if s.Build == nil || s.Build.Iterations >= o.maxBuildIters {
			o.logf("S5<->S6: not passing after %d iteration(s); leaving for GATE C / human", o.maxBuildIters)
			return nil
		}
		o.headline("S6 failed; looping back to S5 (iteration %d/%d)", s.Build.Iterations+1, o.maxBuildIters)
	}
	return nil
}

// gateDecision applies the gate auto-pass policy (SPEC §4 / Q3). Returns (pass, why).
func (o *orchestrator) gateDecision(s *State, gate string) (bool, string) {
	if o.approve[gate] {
		return true, "human-approved (--approve)"
	}
	switch gate {
	case "A":
		// Auto-pass only on new_tool & confidence>=0.7 & research-ok.
		if s.Redundancy != nil && s.Redundancy.Verdict == "new_tool" &&
			s.Redundancy.Confidence >= 0.7 && s.Research != nil && s.Research.QualityOK {
			return true, "auto: new_tool & confidence>=0.7 & research-ok"
		}
		return false, fmt.Sprintf("needs human: verdict=%s conf=%.2f research_ok=%v (run with --approve gateA)",
			verdictOf(s), confidenceOf(s), researchOK(s))
	case "B":
		if o.yes {
			return true, "auto: --yes"
		}
		return false, "spec must be human-approved before code is written (--approve gateB or --yes)"
	case "C":
		if o.yes {
			return true, "auto: --yes"
		}
		return false, "final human review of spec + real output + findings before merge (--approve gateC or --yes)"
	}
	return false, "unknown gate"
}

// stagePause returns a non-empty message when a stage's result should pause the run
// (e.g. a build that couldn't run, or blocking second-AI findings without --yes).
func (o *orchestrator) stagePause(s *State, id string) string {
	switch id {
	case "S5":
		if s.Build != nil && s.Build.Skipped {
			return "S5 build could not run/complete: " + s.Build.SkipReason
		}
		if s.Test != nil && !s.Test.Passed {
			return "tool did not pass S6 verification after the build loop: " + s.Test.FailDetail
		}
	case "S7":
		if s.Review != nil && s.Review.BlockingCount > 0 && !o.yes && !o.approve["C"] {
			return fmt.Sprintf("S7 second-AI raised %d blocking finding(s); review before continuing (--yes to override)", s.Review.BlockingCount)
		}
	}
	return ""
}

// pause records the cursor + reason, prints a clean headline, emits the state, and
// returns nil (a clean pause is exit 0, per the CLI contract).
func (o *orchestrator) pause(s *State, atStage, reason string) error {
	s.Meta.StageCursor = atStage
	_ = s.save()
	o.headline("PAUSED at %s: %s", atStage, reason)
	o.headline("resume with: becky-new-tool --run-dir %q --resume", o.runDir)
	emitState(s)
	return nil
}

// seedMeta fills the meta block on first run (idempotent on resume).
func (o *orchestrator) seedMeta(s *State) {
	if s.Meta.Started == "" {
		s.Meta.Started = time.Now().UTC().Format(time.RFC3339)
	}
	if s.Meta.RunDir == "" {
		s.Meta.RunDir = o.runDir
	}
	s.Meta.Offline = o.offline
}

// clearStage nils a stage's state so --force re-runs it.
func (o *orchestrator) clearStage(s *State, id string) {
	switch id {
	case "S1":
		s.Intake = nil
	case "S2":
		s.Research = nil
	case "S3":
		s.Redundancy = nil
	case "S4":
		s.Spec = nil
	case "S5":
		s.Build = nil
		s.Test = nil
	case "S7":
		s.Review = nil
	case "S9":
		s.Finetune = nil
	case "S10":
		s.Integrate = nil
	}
	o.logf("--force %s: cleared its state", id)
}

// adoptCheap fills the cheap backend's model id with the protocol-verified id. If the
// user already chose a transport (Kind != "none"), it ONLY sets the model (so a
// local-only run is not silently switched to a remote channel). If no transport was
// chosen, it adopts the protocol channel's transport too.
func (o *orchestrator) adoptCheap(modelID, channel string) {
	if o.cheap.Kind != "none" {
		o.cheap.Model = modelID // keep the chosen transport (+ ServerURL); just fill the id
		return
	}
	switch channel {
	case "openrouter":
		o.cheap = CheapBackend{Kind: "openrouter", Model: modelID}
	case "local-hf":
		o.cheap = CheapBackend{Kind: "local", Model: modelID, ServerURL: o.cheap.ServerURL}
	}
}

// ---- small shared helpers (orchestrator-scoped) ----

func (o *orchestrator) logf(format string, a ...any) {
	if o.logFile != nil {
		fmt.Fprintf(o.logFile, format+"\n", a...)
	}
	if o.verbose {
		fmt.Fprintf(os.Stderr, format+"\n", a...)
	}
}

// headline prints a plain-English stage headline to stderr (always) + the log.
func (o *orchestrator) headline(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	if o.logFile != nil {
		fmt.Fprintf(o.logFile, format+"\n", a...)
	}
}

// binPath resolves becky-<tool>(.exe) from the configured bin dir.
func (o *orchestrator) binPath(name string) string {
	candidate := filepath.Join(o.binDir, name)
	if runtime.GOOS == "windows" && !strings.HasSuffix(candidate, ".exe") {
		candidate += ".exe"
	}
	if fileExists(candidate) {
		return candidate
	}
	return ""
}

func verdictOf(s *State) string {
	if s.Redundancy != nil {
		return s.Redundancy.Verdict
	}
	return "?"
}
func confidenceOf(s *State) float64 {
	if s.Redundancy != nil {
		return s.Redundancy.Confidence
	}
	return 0
}
func researchOK(s *State) bool {
	return s.Research != nil && s.Research.QualityOK
}
