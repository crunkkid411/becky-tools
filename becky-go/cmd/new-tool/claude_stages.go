// claude_stages.go — the two stages that drive a headless Claude Code agent through
// the shared internal/agentrun helper:
//
//	S4 SPEC  (default Claude; togglable to cheap) — writes SPEC-<NAME>.md in the run dir.
//	S5 BUILD (mandatory Claude, Ralph loop)       — the agent writes cmd/<name>/.
//
// S5 is the ONE irreducible Claude stage; the whole pipeline exists to protect the
// metered Agent-SDK credit FOR it. The Fact-Forcing Gate (SPEC §6) rides INSIDE the
// S5 agent: the agent runs the becky build, hits the project PreToolUse hook on its
// first new-file Write, and must state the 4 facts — exactly as a human build would.
// We pass --setting-sources project so the hook loads, and afterward parse the 4
// facts out of the transcript into state.build.fact_forcing for auditability.
//
// Fact-Forcing-Gate self-certification:
//  1. Callers: orchestrator.go's stage dispatch calls runS4Spec / runS5Build.
//  2. No-dup: uses the shared internal/agentrun (already built) for the claude -p
//     invocation — it does NOT reimplement the CLI call (that would duplicate
//     agentrun). No other spec/build stage exists.
//  3. Data shape: builds agentrun.AgentSpec, reads its AgentResult; reads/writes
//     state.spec / state.build (typed in state.go); writes SPEC-<NAME>.md + an
//     agent stream-json log into the run dir; parses the agent's 4 facts.
//  4. Verbatim instruction: "LLM/agent runs ONLY inside specific stages; control flow
//     is pure code. ... the Fact-Forcing Gate is enforced inside S5, not a node".
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"becky-go/internal/agentrun"
)

// ---------------------------------------------------------------------------
// S4 — SPEC
// ---------------------------------------------------------------------------

// runS4Spec writes the new tool's spec in the project's proven structure. Default is
// one Claude call (spec-writing is the first place real reasoning pays off); it is
// togglable to the cheap model via --spec-backend cheap for simple tools that a human
// approves at GATE B anyway.
func (o *orchestrator) runS4Spec(ctx context.Context, s *State) error {
	if s.Spec != nil {
		o.logf("S4 spec: already done (%s) — skipping", s.Spec.SpecPath)
		return nil
	}
	if s.Research == nil || s.Redundancy == nil {
		return fmt.Errorf("S4 spec: research/redundancy missing (run S2/S3 first)")
	}

	specPath := filepath.Join(s.Meta.RunDir, "SPEC-"+strings.ToUpper(s.Intake.Slug)+".md")
	sp := &Spec{SpecPath: specPath}

	prompt := o.buildSpecPrompt(s)

	if o.specBackend == "cheap" && o.cheap.Kind != "none" {
		if txt, ok, note := cheapComplete(ctx, o.cheap, specSystemPrompt, prompt); ok {
			body := strings.TrimSpace(txt)
			if err := os.WriteFile(specPath, []byte(body), 0o644); err != nil {
				return fmt.Errorf("write spec: %w", err)
			}
			sp.AuthoredBy = "cheap:" + o.cheap.Model
			o.fillSpecFromIntake(sp, s)
			s.Spec = sp
			o.logf("S4 spec: written by cheap model -> %s", specPath)
			return s.save()
		} else {
			o.logf("S4 spec: cheap backend unavailable (%s); falling back to Claude", note)
		}
	}

	// Default: Claude one-shot. We save the markdown body as the spec; the typed fields
	// are seeded from the intake (a human approves the spec at GATE B regardless).
	if agentrun.ResolveBin() == "" {
		// No claude CLI: degrade to a deterministic spec skeleton so the run can still
		// pause at GATE B for a human (never crash).
		body := o.deterministicSpecSkeleton(s)
		if err := os.WriteFile(specPath, []byte(body), 0o644); err != nil {
			return fmt.Errorf("write spec: %w", err)
		}
		sp.AuthoredBy = "deterministic-skeleton (claude CLI absent)"
		o.fillSpecFromIntake(sp, s)
		s.Spec = sp
		o.logf("S4 spec: claude CLI absent -> deterministic skeleton at %s", specPath)
		return s.save()
	}

	specCtx, cancel := context.WithTimeout(ctx, o.specTimeout)
	defer cancel()
	res, err := agentrun.Run(specCtx, agentrun.AgentSpec{
		PromptStdin:    prompt,
		SystemPrompt:   specSystemPrompt,
		Model:          o.buildModel,
		FallbackModel:  o.fallbackModel,
		MaxTurns:       8,
		MaxBudgetUSD:   o.perCallBudget,
		WorkDir:        os.TempDir(), // neutral dir: no project CLAUDE.md auto-discovery for a pure-authoring call
		SettingSources: "user",
	})
	if err != nil {
		// Degrade: write a skeleton + record the error so GATE B can show a human.
		o.logf("S4 spec: claude error (%v); writing deterministic skeleton", err)
		body := o.deterministicSpecSkeleton(s)
		_ = os.WriteFile(specPath, []byte(body), 0o644)
		sp.AuthoredBy = "deterministic-skeleton (claude error)"
		o.fillSpecFromIntake(sp, s)
		s.Spec = sp
		return s.save()
	}
	o.recordClaudeCheckResult(s, "S4 spec", res)
	s.addClaudeCost(res.CostUSD)
	sp.CostUSD = res.CostUSD
	sp.AgentSessionID = res.SessionID
	sp.AuthoredBy = "claude:" + o.buildModel

	body := strings.TrimSpace(res.Result)
	if body == "" {
		body = o.deterministicSpecSkeleton(s)
	}
	if err := os.WriteFile(specPath, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write spec: %w", err)
	}
	o.fillSpecFromIntake(sp, s)
	s.Spec = sp
	o.logf("S4 spec: written by claude (%.4f USD, session %s) -> %s", res.CostUSD, res.SessionID, specPath)
	return s.save()
}

// fillSpecFromIntake seeds the machine-readable spec fields from the intake so S9 has
// a tunable surface + answer-key facts even when the author model returns prose only.
func (o *orchestrator) fillSpecFromIntake(sp *Spec, s *State) {
	if len(sp.DegradePlan) == 0 {
		sp.DegradePlan = []string{"missing model/dep -> emit JSON with skipped/reason, exit 0", "never half-JSON"}
	}
	if len(sp.Reuse) == 0 {
		sp.Reuse = []string{"internal/beckyio", "internal/config", "internal/mediainfo"}
	}
	if len(sp.TunableSurface) == 0 {
		sp.TunableSurface = []string{"(declare per tool: thresholds, prompt variants, flag combos)"}
	}
	if len(sp.AnswerKeyFacts) == 0 {
		sp.AnswerKeyFacts = []string{"(declare per tool: the facts a correct run must surface)"}
	}
}

// ---------------------------------------------------------------------------
// S5 — BUILD (mandatory Claude, Ralph loop, Fact-Forcing Gate inside)
// ---------------------------------------------------------------------------

// runS5Build delegates the coding to a headless Claude Code agent running the Ralph
// loop from BUILD-AGENT-BRIEFING.md. The briefing is the system layer; the spec + the
// concrete task are the stdin prompt. We stream events to the run log, enforce
// --max-turns / --max-budget-usd, fix the session id so an S6 failure resumes the
// SAME conversation, and grant tight tools + the build root.
func (o *orchestrator) runS5Build(ctx context.Context, s *State) error {
	if s.Build != nil && s.Build.PackageDir != "" && !s.Build.Skipped {
		// A prior S5 produced a package; only re-run if S6 fed back a failure.
		if s.Build.Feedback == "" {
			o.logf("S5 build: already done (%s) — skipping", s.Build.PackageDir)
			return nil
		}
		o.logf("S5 build: resuming session %s with S6 feedback", s.Build.AgentSessionID)
		return o.resumeBuild(ctx, s)
	}
	if s.Spec == nil {
		return fmt.Errorf("S5 build: spec missing (run S4 first)")
	}

	if agentrun.ResolveBin() == "" {
		s.Build = &Build{Skipped: true, SkipReason: "claude CLI not found on PATH; S5 build is mandatory-Claude and cannot run"}
		o.logf("S5 build: SKIPPED — claude CLI absent")
		return s.save()
	}

	sessionID := newSessionID(s.Meta.Slug)
	prompt := o.buildBuildPrompt(s, "")
	logPath := filepath.Join(s.Meta.RunDir, "agent-build.stream.jsonl")
	logF, _ := os.Create(logPath)
	if logF != nil {
		defer logF.Close()
	}

	buildCtx, cancel := context.WithTimeout(ctx, o.buildTimeout)
	defer cancel()

	o.logf("S5 build: invoking headless claude agent (model=%s, session=%s, max-turns=%d, budget=$%g)...",
		o.buildModel, sessionID, o.maxTurns, o.perCallBudget)
	res, err := agentrun.Stream(buildCtx, o.buildAgentSpec(s, prompt, sessionID, ""), func(ev json.RawMessage) {
		if logF != nil {
			logF.Write(ev)
			logF.Write([]byte("\n"))
		}
	})
	return o.recordBuild(s, res, err, sessionID, 1)
}

// resumeBuild re-invokes the SAME agent session with the S6 failure feedback (the
// bounded S5<->S6 loop). Iterations are capped by o.maxBuildIters.
func (o *orchestrator) resumeBuild(ctx context.Context, s *State) error {
	if s.Build.Iterations >= o.maxBuildIters {
		o.logf("S5 build: hit max-build-iterations (%d); stopping for a human", o.maxBuildIters)
		s.Build.SkipReason = fmt.Sprintf("reached max-build-iterations (%d) without passing S6", o.maxBuildIters)
		return s.save()
	}
	prompt := o.buildBuildPrompt(s, s.Build.Feedback)
	logPath := filepath.Join(s.Meta.RunDir, fmt.Sprintf("agent-build.iter%d.stream.jsonl", s.Build.Iterations+1))
	logF, _ := os.Create(logPath)
	if logF != nil {
		defer logF.Close()
	}
	buildCtx, cancel := context.WithTimeout(ctx, o.buildTimeout)
	defer cancel()

	res, err := agentrun.Stream(buildCtx, o.buildAgentSpec(s, prompt, "", s.Build.AgentSessionID), func(ev json.RawMessage) {
		if logF != nil {
			logF.Write(ev)
			logF.Write([]byte("\n"))
		}
	})
	return o.recordBuild(s, res, err, s.Build.AgentSessionID, s.Build.Iterations+1)
}

// buildAgentSpec assembles the S5 agent spec. resume!="" resumes an existing session;
// otherwise sessionID pins a fresh one.
func (o *orchestrator) buildAgentSpec(s *State, prompt, sessionID, resume string) agentrun.AgentSpec {
	return agentrun.AgentSpec{
		PromptStdin:      prompt,
		SystemPromptFile: o.briefingPath, // BUILD-AGENT-BRIEFING.md as the build system layer
		Model:            o.buildModel,
		FallbackModel:    o.fallbackModel,
		MaxTurns:         o.maxTurns,
		MaxBudgetUSD:     o.perCallBudget,
		AllowedTools: []string{
			"Read", "Edit", "Write", "Glob", "Grep",
			"Bash(go *)", "Bash(.\\bin\\becky-* *)", "Bash(ffmpeg *)", "Bash(ffprobe *)",
		},
		AddDirs:        []string{o.buildRoot},
		PermissionMode: "acceptEdits",
		SessionID:      sessionID,
		Resume:         resume,
		// Non-bare + project setting sources so the Fact-Forcing-Gate hook loads (SPEC §6).
		SettingSources: "project,user",
		WorkDir:        o.buildRoot,
	}
}

// recordBuild maps an agent result into state.build, parsing the 4 Fact-Forcing facts
// from the transcript and accumulating cost. err is the agentrun error (may be a
// budget/turn stop with a usable partial result).
func (o *orchestrator) recordBuild(s *State, res agentrun.AgentResult, err error, sessionID string, iter int) error {
	b := s.Build
	if b == nil {
		b = &Build{}
		s.Build = b
	}
	b.PackageDir = filepath.Join(o.buildRoot, "cmd", cmdDirName(s.Intake.Slug))
	b.BinaryPath = filepath.Join(o.buildRoot, "bin", binName(s.Intake.Slug))
	b.Iterations = iter
	b.Feedback = "" // consumed
	if sessionID != "" {
		b.AgentSessionID = sessionID
	}
	if res.SessionID != "" {
		b.AgentSessionID = res.SessionID
	}
	b.TurnsUsed += res.NumTurns
	b.CostUSD += res.CostUSD
	b.AgentReport = truncate(res.Result, 4000)
	s.addClaudeCost(res.CostUSD)
	o.recordClaudeCheckResult(s, "S5 build", res)

	// Parse the 4 facts from the streamed transcript (auditability of the gate).
	b.FactForcing = parseFactForcing(res.Events, res.Result)

	if err != nil {
		o.logf("S5 build: agent finished with note: %v (cost so far $%.4f, %d turns)", err, b.CostUSD, b.TurnsUsed)
	}
	// Whether the package actually exists is determined deterministically in S6.
	if !dirExists(b.PackageDir) {
		o.logf("S5 build: agent did not create %s (S6 will fail and may loop back)", b.PackageDir)
	} else {
		o.logf("S5 build: package present at %s (cost $%.4f, %d turns, facts_found=%v)",
			b.PackageDir, b.CostUSD, b.TurnsUsed, b.FactForcing.Found)
	}
	return s.save()
}

// recordClaudeCheckResult appends a ModelCheck for a Claude call so the run-state
// audit shows the build/spec model used (the claude CLI resolves the concrete model
// id; we record what we requested + the cost as evidence it ran).
func (o *orchestrator) recordClaudeCheckResult(s *State, purpose string, res agentrun.AgentResult) {
	if s.Research == nil {
		return
	}
	modelID := o.buildModel
	for id := range res.ModelCostUSD {
		modelID = id // the concrete id the CLI actually used (e.g. claude-opus-4-8)
		break
	}
	s.Research.ModelChecks = append(s.Research.ModelChecks, ModelCheck{
		Purpose:   purpose,
		ModelID:   modelID,
		Channel:   "claude",
		SourceURL: "resolved by the claude CLI at runtime",
		CheckedAt: todayISO(),
		Rationale: fmt.Sprintf("headless build/spec model; cost $%.4f, subtype=%s", res.CostUSD, res.Subtype),
		Verified:  res.SessionID != "" && !res.IsError,
	})
	_ = s.save()
}

// parseFactForcing scans the agent transcript for the 4 Fact-Forcing facts the gate
// requires. It is best-effort: a heuristic over the assistant text, recorded for audit
// (the gate's ENFORCEMENT is the hook inside the agent; this is the after-the-fact
// proof). Found is set when at least the Callers + Verbatim facts are present.
func parseFactForcing(events []json.RawMessage, finalResult string) FactForcing {
	hay := finalResult
	for _, ev := range events {
		hay += "\n" + string(ev)
	}
	low := strings.ToLower(hay)
	ff := FactForcing{}
	if i := strings.Index(low, "caller"); i >= 0 {
		ff.Callers = snippetAround(hay, i, 160)
	}
	if i := strings.Index(low, "no-dup"); i >= 0 {
		ff.NoDup = snippetAround(hay, i, 160)
	} else if i := strings.Index(low, "no dup"); i >= 0 {
		ff.NoDup = snippetAround(hay, i, 160)
	}
	if i := strings.Index(low, "data shape"); i >= 0 {
		ff.DataShape = snippetAround(hay, i, 160)
	}
	if i := strings.Index(low, "verbatim"); i >= 0 {
		ff.Verbatim = snippetAround(hay, i, 160)
	}
	ff.Found = ff.Callers != "" && ff.Verbatim != ""
	return ff
}

func snippetAround(s string, idx, n int) string {
	end := idx + n
	if end > len(s) {
		end = len(s)
	}
	return truncate(strings.TrimSpace(s[idx:end]), n)
}

// newSessionID builds a UUID-shaped session id for a run so a retry can resume it. It
// is derived from the slug + a timestamp; uniqueness per run is what matters (the
// claude CLI requires a UUID format).
func newSessionID(slug string) string {
	return fmt.Sprintf("%08x-%04x-4%03x-8%03x-%012x",
		hash32(slug), uint16(time.Now().Unix()), uint16(time.Now().UnixNano()&0xfff),
		uint16(time.Now().UnixNano()>>12&0xfff), uint64(time.Now().UnixNano())&0xffffffffffff)
}

func hash32(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}
