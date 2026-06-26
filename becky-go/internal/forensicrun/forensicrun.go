// Package forensicrun is the RUNTIME layer that makes becky's entry tools self-regulate:
// it gathers the sibling becky-*.exe outputs, drives the live Gemma-4 validate ladder, and
// runs every claim through the deterministic protocol engine (internal/orchestrate) via the
// single tool->claim mapping (internal/forensic). It is the ONE place becky-transcribe,
// becky-ask (and case/resolve) converge so a single dumb call returns the FINAL corroborated
// answer with the diarize/validate/escalate decisions made internally — never left to an agent.
//
// Split by testability:
//   - Report + Plan are PURE (already-gathered tool JSON in -> corroborated report out): no I/O,
//     no models, fully unit-tested with value assertions and a fake Executor.
//   - RunAndReport is IMPURE: it shells the sibling tools + the Gemma-4 ladder (injected
//     ToolRunner, so even that is testable) and degrades-never-crashes — a missing tool/model is
//     recorded in Degraded and its signal is simply absent, never a false conclusion.
package forensicrun

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"becky-go/internal/forensic"
	"becky-go/internal/orchestrate"
	"becky-go/internal/proc"
	"becky-go/internal/workflowdef"
)

// presenceMergeGap is the seconds within which a subject's timed signals merge into one
// on-screen window (matches becky-case's correlation window).
const presenceMergeGap = 2.0

// maxLadderLevel is the depth of the forced validate ladder: level 1 = Gemma-4 E4B, level 2 = 12B.
const maxLadderLevel = 2

// ForensicReport is the single corroborated forensic output a self-regulating entry returns.
// Only Names/OnScreen are STATED facts (each is a Concluded verdict); Held holds the
// candidates/unknowns the protocol refuses to state (no flood of maybes); Degraded records any
// model/tool that was absent so the partial result is honest.
type ForensicReport struct {
	File     string                `json:"file"`
	Subject  string                `json:"subject,omitempty"`
	Plan     []string              `json:"plan"`      // deterministic steps run (diarize-conditional)
	Names    []orchestrate.Verdict `json:"names"`     // stated only when corroborated
	OnScreen []orchestrate.Verdict `json:"on_screen"` // stated only where a model watched it
	Held     []orchestrate.Verdict `json:"held"`      // one-signal maybes / unknowns, NOT stated
	Audit    []string              `json:"audit"`
	Degraded []string              `json:"degraded,omitempty"`
}

// Inputs are the already-gathered raw tool JSON outputs — the pure inputs to Report.
type Inputs struct {
	Identify   []byte // becky-identify JSON   -> naming claims
	Transcribe []byte // becky-transcribe JSON -> mention signals (presence)
	Motion     []byte // becky-motion JSON     -> motion-burst candidate moments
	Validate   []byte // becky-validate JSON   -> watched signals (presence), optional
}

// Plan returns the diarize-conditional executed-step plan for a known speaker count, from the
// embedded process-video recipe: diarize + the gemma4 check run ONLY when speakers > 1. A
// one-speaker clip skips diarize — the protocol decided in code, not by the caller.
func Plan(speakers int) []string {
	r, err := workflowdef.ProcessVideo()
	if err != nil {
		return nil
	}
	facts := workflowdef.Facts{"speakers": float64(speakers)}
	var steps []string
	for _, s := range r.Steps {
		if workflowdef.EvalWhen(s.When, facts) {
			steps = append(steps, s.Name())
		}
	}
	return steps
}

// Report is the PURE protocol enforcement: given already-gathered tool JSON (and an optional
// validate-ladder Executor), it returns the corroborated forensic report. No I/O, no models.
// Naming and presence both run through orchestrate.Resolve so the protocol is identical.
func Report(file, subject string, speakers int, in Inputs, exec orchestrate.Executor, maxLevel int) ForensicReport {
	rep := ForensicReport{File: file, Subject: strings.TrimSpace(subject), Plan: Plan(speakers)}

	// Naming (corroborate-or-hold). A candidate identity escalates up the ladder.
	nameClaims := forensic.IdentifyToClaims(in.Identify)
	nres := orchestrate.Resolve(nameClaims, orchestrate.DefaultRules(), exec, maxLevel)
	rep.Names = nres.Concluded
	rep.Held = appendVerdicts(rep.Held, nres.Candidates, nres.Unknown)
	rep.Audit = append(rep.Audit, nres.Audit...)

	// Presence (watch-or-hold), only when a subject is named. The ladder supplies the WATCH a
	// presence claim needs (rule 3): a mention + motion can never conclude until a model watches.
	if rep.Subject != "" {
		sigs := forensic.PresenceSignals(rep.Subject, in.Transcribe, in.Motion, in.Validate)
		presClaims := orchestrate.CorrelatePresence(rep.Subject, sigs, presenceMergeGap)
		pres := orchestrate.Resolve(presClaims, orchestrate.DefaultRules(), exec, maxLevel)
		rep.OnScreen = pres.Concluded
		rep.Held = appendVerdicts(rep.Held, pres.Candidates, pres.Unknown)
		rep.Audit = append(rep.Audit, pres.Audit...)
	}
	return rep
}

func appendVerdicts(dst []orchestrate.Verdict, groups ...[]orchestrate.Verdict) []orchestrate.Verdict {
	for _, g := range groups {
		dst = append(dst, g...)
	}
	return dst
}

// ---- the impure runtime: shell the sibling tools + the Gemma-4 ladder ----

// ToolRunner runs a sibling becky tool with extra environment and returns its stdout. It is
// injected so RunAndReport and the ladder are fully testable without the real binaries/models.
type ToolRunner func(ctx context.Context, tool string, args, extraEnv []string) ([]byte, error)

// defaultKB is the conventional knowledge-base dir the becky-ask layer already uses for
// becky-identify (cmd/ask/actions.go). becky-identify REQUIRES a --kb (the enrolled people for a
// case); without it naming can never resolve, so the runtime must always supply one.
const defaultKB = "kb-final"

// ResolveKB picks the knowledge-base dir for becky-identify: an explicit value wins, else the
// BECKY_KB env (so the forensic agent sets a case's KB once, not per call), else the convention.
// Exported so the entry tools (becky-resolve etc.) resolve the KB identically.
func ResolveKB(explicit string) string {
	if s := strings.TrimSpace(explicit); s != "" {
		return s
	}
	if s := strings.TrimSpace(os.Getenv("BECKY_KB")); s != "" {
		return s
	}
	return defaultKB
}

// RunAndReport is the IMPURE self-regulating entry: it gathers becky-identify (always, with the
// resolved --kb) and, when a subject is given, becky-transcribe (for mentions, unless
// transcribeJSON is supplied by a caller that already transcribed) + becky-motion, builds the live
// Gemma-4 ladder, and runs Report. kb is an explicit knowledge base ("" => BECKY_KB env or the
// kb-final convention). Degrade-never-crash: a missing tool/KB is recorded in Degraded, never panicked on.
func RunAndReport(ctx context.Context, file, subject, kb string, speakers int, transcribeJSON []byte) ForensicReport {
	return runAndReport(ctx, file, subject, ResolveKB(kb), speakers, transcribeJSON, realRunner)
}

func runAndReport(ctx context.Context, file, subject, kb string, speakers int, transcribeJSON []byte, run ToolRunner) ForensicReport {
	in := Inputs{Transcribe: transcribeJSON}
	var degraded []string
	gather := func(tool string, args ...string) []byte {
		b, err := run(ctx, tool, append([]string{file}, args...), nil)
		if err != nil {
			degraded = append(degraded, tool+": "+oneLine(err.Error()))
			return nil
		}
		return b
	}

	var idArgs []string
	if strings.TrimSpace(kb) != "" {
		idArgs = []string{"--kb", kb}
	}
	in.Identify = gather("becky-identify", idArgs...)
	if strings.TrimSpace(subject) != "" {
		if in.Transcribe == nil {
			in.Transcribe = gather("becky-transcribe")
		}
		in.Motion = gather("becky-motion")
	}

	rep := Report(file, subject, speakers, in, gemmaLadder{file: file, run: run}, maxLadderLevel)
	rep.Degraded = append(rep.Degraded, degraded...)
	return rep
}

// gemmaLadder is the real validate Executor (rule 4): level 1 = Gemma-4 E4B (default), level 2 =
// 12B selected via the BECKY_AVLM_VARIANT=12b env (NOT a --variant flag — becky-validate has
// none; the model is chosen by config). It WATCHES the clip with becky-validate; for a presence
// claim it returns KindWatched (the proof rule 3 requires), for an identity claim KindPrint (a
// corroborating re-check). becky-validate never crashes (it degrades to JSON + a note on a
// missing model), so a no-corroboration result keeps the claim a candidate, never a false name.
type gemmaLadder struct {
	file string
	run  ToolRunner
}

func (g gemmaLadder) Validate(c orchestrate.Claim, level int) (orchestrate.Signal, error) {
	model := "gemma4-e4b"
	var env []string
	if level >= 2 {
		model, env = "gemma4-12b", []string{"BECKY_AVLM_VARIANT=12b"}
	}
	out, err := g.run(context.Background(), "becky-validate", []string{g.file, "--backend", "gemma4-local"}, env)
	if err != nil {
		// The binary itself failed (absent/crashed) — return an error so the ladder STOPS (it
		// would fail identically at the next level). A model that RAN but found nothing is handled
		// below: it yields a sub-floor signal so the ladder ESCALATES (E4B -> 12B) instead.
		return orchestrate.Signal{}, fmt.Errorf("%s unavailable: %w", model, err)
	}
	// A presence watch only corroborates the SUBJECT when the model actually saw the subject (not
	// just "something" on screen), so for a presence claim we score only the observations that name
	// it. An identity claim is a generic re-check (top confidence).
	kind := orchestrate.KindPrint
	conf := topObservationConfidence(out)
	if c.IsPresence {
		kind = orchestrate.KindWatched
		conf = topObservationConfidenceMatching(out, presenceSubjectFromKey(c.Key))
	}
	// Return the watch's actual confidence (possibly below the floor). orchestrate.Corroborate
	// drops a sub-floor signal, so a weak E4B watch leaves the claim a candidate and the loop
	// escalates to 12B; only a >=floor watch counts toward corroboration / satisfies the watch rule.
	return orchestrate.Signal{Source: model, Kind: kind, Confidence: conf}, nil
}

// NewGemmaLadder returns the real validate-ladder Executor for a clip — the SINGLE correct model
// call entry tools share (becky-transcribe/ask/case/resolve/presence) instead of each re-deriving
// it (and mis-flagging it): it escalates E4B->12B via the BECKY_AVLM_VARIANT env, and a presence
// claim's watch is subject-aware. Degrade-never-crash (a missing binary errors -> claim stays held).
func NewGemmaLadder(file string) orchestrate.Executor {
	return gemmaLadder{file: file, run: realRunner}
}

// presenceSubjectFromKey pulls "<subject>" out of an "onscreen=<subject>@[t0-t1]" claim key.
func presenceSubjectFromKey(key string) string {
	s := strings.TrimPrefix(key, "onscreen=")
	if at := strings.IndexByte(s, '@'); at >= 0 {
		s = s[:at]
	}
	return strings.TrimSpace(s)
}

// topObservationConfidenceMatching returns the highest confidence among observations whose text
// NAMES the subject (case-insensitive over visual/finding/content) — a watch of the subject, not of
// "something". An empty subject falls back to the overall top confidence.
func topObservationConfidenceMatching(raw []byte, subject string) float64 {
	subj := strings.ToLower(strings.TrimSpace(subject))
	if subj == "" {
		return topObservationConfidence(raw)
	}
	var v struct {
		Observations []struct {
			Visual     string  `json:"visual"`
			Finding    string  `json:"finding"`
			Content    string  `json:"content"`
			Confidence float64 `json:"confidence"`
		} `json:"observations"`
	}
	conf := 0.0
	if json.Unmarshal(bytes.TrimSpace(raw), &v) == nil {
		for _, o := range v.Observations {
			if strings.Contains(strings.ToLower(o.Visual+" "+o.Finding+" "+o.Content), subj) && o.Confidence > conf {
				conf = o.Confidence
			}
		}
	}
	return conf
}

// topObservationConfidence returns the highest observation confidence in a becky-validate JSON
// (0 when none / unparseable), the strength of the model's watch.
func topObservationConfidence(raw []byte) float64 {
	var v struct {
		Observations []struct {
			Confidence float64 `json:"confidence"`
		} `json:"observations"`
	}
	conf := 0.0
	if json.Unmarshal(bytes.TrimSpace(raw), &v) == nil {
		for _, o := range v.Observations {
			if o.Confidence > conf {
				conf = o.Confidence
			}
		}
	}
	return conf
}

// RunTool shells a sibling becky tool (resolved on PATH or next to this exe), windowless, and
// returns its stdout — the shared, single way entry tools gather a sibling's JSON, so a tool that
// needs just one sibling's output (e.g. becky-presence) doesn't re-roll exec + NoWindow + lookup.
func RunTool(ctx context.Context, tool string, args ...string) ([]byte, error) {
	return realRunner(ctx, tool, args, nil)
}

// realRunner shells a sibling becky tool (resolved on PATH or next to this exe), windowless (no
// console flash when spawned from a GUI), with extra env appended. A non-zero exit returns an
// error so callers degrade. Most becky tools exit 0 even on a missing model (degrade-to-JSON),
// so this errors mainly when the binary itself is absent.
func realRunner(ctx context.Context, tool string, args, extraEnv []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, resolveTool(tool), args...)
	proc.NoWindow(cmd)
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%v: %s", err, oneLine(tailStr(errb.String())))
	}
	return out.Bytes(), nil
}

// resolveTool finds a sibling becky binary: PATH first, then the directory of the running exe
// (becky tools install side-by-side in bin/). Falls back to the bare name so exec errors cleanly.
func resolveTool(tool string) string {
	if p, err := exec.LookPath(tool); err == nil {
		return p
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), tool)
		if runtime.GOOS == "windows" {
			cand += ".exe"
		}
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	return tool
}

func oneLine(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func tailStr(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 400 {
		return s[len(s)-400:]
	}
	return s
}
