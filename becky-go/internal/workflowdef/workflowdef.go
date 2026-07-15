// Package workflowdef is becky-voice's DECLARATIVE, CONDITIONAL workflow engine: a
// named workflow is a file (workflows/<name>.json) describing an ORDERED list of steps,
// each optionally gated by a deterministic `when` condition over facts that earlier
// steps produced. This is the fix for Jordan's "I can't say 'run it like this when I
// ask for X', and diarize shouldn't run on a one-speaker video" (SPEC-BECKY-VOICE.md
// §3.3, HANDOFF-BECKY-VOICE.md Step 0.2).
//
// The logic SHAPE is ported from cmd/ask/workflow.go's runTranscribeWorkflow — the
// difference is the chain is now data (a recipe) instead of a hardcoded step string,
// and steps can be skipped by a no-model condition (e.g. "speakers > 1"). The engine
// here is deterministic and model-free; running a real tool is injected by the caller
// (so it is testable with faked tool outputs, no ffmpeg/models).
package workflowdef

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Step is one entry in a recipe. Exactly one of Tool / Verb / Merge / Agent is set:
//   - Tool:  shell an existing becky-*.exe tool (e.g. "becky-transcribe").
//   - Verb:  an orchestrator op / named action (e.g. "verify-with-gemma4").
//   - Merge:  a deterministic merge of prior outputs (e.g. "transcript").
//   - Agent: run a headless AI agent (e.g. "claude-code" or "qwen") over the prior
//     step outputs, with Prompt as its instruction. This is OPT-IN per recipe: a recipe
//     with no Agent step spends ZERO AI tokens. That is the whole point vs Archon —
//     the model runs only when a recipe file explicitly asks for it, never every run.
//
// Prompt is the instruction for an Agent (or Verb) step; tool/merge steps ignore it.
// When, if non-empty, is a deterministic condition over facts; the step runs only when
// it evaluates true. An empty When means "always run".
type Step struct {
	Tool   string `json:"tool,omitempty"`
	Verb   string `json:"verb,omitempty"`
	Merge  string `json:"merge,omitempty"`
	Agent  string `json:"agent,omitempty"`
	Prompt string `json:"prompt,omitempty"`
	When   string `json:"when,omitempty"`
}

// Kind reports which of tool/verb/merge this step is, for the executor + reporting.
func (s Step) Kind() string {
	switch {
	case s.Tool != "":
		return "tool"
	case s.Verb != "":
		return "verb"
	case s.Merge != "":
		return "merge"
	case s.Agent != "":
		return "agent"
	default:
		return ""
	}
}

// Name is the identifier of the step's action (the tool/verb/merge value), used for the
// executed-step list and audit.
func (s Step) Name() string {
	switch {
	case s.Tool != "":
		return s.Tool
	case s.Verb != "":
		return s.Verb
	case s.Merge != "":
		return s.Merge
	case s.Agent != "":
		return s.Agent
	default:
		return ""
	}
}

// Recipe is a named, phrase-triggered, ordered list of steps. Jordan reads AND edits
// this file; THIS is "run it like this when I ask for X".
type Recipe struct {
	Name    string   `json:"name"`
	Phrases []string `json:"phrases"`
	Steps   []Step   `json:"steps"`
}

// Load reads and validates a recipe file.
func Load(path string) (Recipe, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Recipe{}, fmt.Errorf("read recipe %s: %w", path, err)
	}
	return Parse(b)
}

// Parse decodes + validates a recipe from JSON bytes.
func Parse(b []byte) (Recipe, error) {
	var r Recipe
	if err := json.Unmarshal(b, &r); err != nil {
		return Recipe{}, fmt.Errorf("parse recipe: %w", err)
	}
	if err := r.Validate(); err != nil {
		return Recipe{}, err
	}
	return r, nil
}

// Validate rejects an empty name and any step that is not exactly one of tool/verb/merge.
func (r Recipe) Validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("recipe has no name")
	}
	if len(r.Steps) == 0 {
		return fmt.Errorf("recipe %q has no steps", r.Name)
	}
	for i, s := range r.Steps {
		n := 0
		if s.Tool != "" {
			n++
		}
		if s.Verb != "" {
			n++
		}
		if s.Merge != "" {
			n++
		}
		if s.Agent != "" {
			n++
		}
		if n != 1 {
			return fmt.Errorf("recipe %q step %d must set exactly one of tool/verb/merge/agent (got %d)", r.Name, i, n)
		}
		if s.When != "" {
			if _, err := parseCondition(s.When); err != nil {
				return fmt.Errorf("recipe %q step %d (%s): %w", r.Name, i, s.Name(), err)
			}
		}
	}
	return nil
}

// Matches reports whether one of the recipe's trigger phrases is contained in the
// (lower-cased) utterance — the deterministic phrase trigger.
func (r Recipe) Matches(utterance string) bool {
	u := strings.ToLower(utterance)
	for _, p := range r.Phrases {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" && strings.Contains(u, p) {
			return true
		}
	}
	return false
}

// --- deterministic condition evaluator (no model) ---

// Facts are the named integer/boolean facts a condition is evaluated against. They are
// produced by earlier steps (or a cheap probe) — e.g. "speakers" -> 3. Missing facts
// evaluate as 0 / false, so a condition over an unknown fact is simply not satisfied.
type Facts map[string]float64

// condition is a parsed "<key> <op> <number>" comparison. Only deterministic numeric
// comparisons are supported on purpose — no model, no arbitrary code.
type condition struct {
	key string
	op  string
	val float64
}

var condOps = []string{">=", "<=", "==", "!=", ">", "<"}

func parseCondition(expr string) (condition, error) {
	e := strings.TrimSpace(expr)
	for _, op := range condOps {
		if i := strings.Index(e, op); i >= 0 {
			key := strings.TrimSpace(e[:i])
			rhs := strings.TrimSpace(e[i+len(op):])
			if key == "" || rhs == "" {
				return condition{}, fmt.Errorf("malformed condition %q", expr)
			}
			v, err := strconv.ParseFloat(rhs, 64)
			if err != nil {
				return condition{}, fmt.Errorf("condition %q: right side %q is not a number", expr, rhs)
			}
			return condition{key: key, op: op, val: v}, nil
		}
	}
	return condition{}, fmt.Errorf("condition %q has no comparison operator", expr)
}

func (c condition) eval(f Facts) bool {
	lhs := f[c.key] // missing fact -> 0
	switch c.op {
	case ">":
		return lhs > c.val
	case "<":
		return lhs < c.val
	case ">=":
		return lhs >= c.val
	case "<=":
		return lhs <= c.val
	case "==":
		return lhs == c.val
	case "!=":
		return lhs != c.val
	}
	return false
}

// EvalWhen reports whether a step's `when` condition holds against the facts. An empty
// condition always holds. An unparseable condition (already rejected by Validate) is
// treated as false defensively.
func EvalWhen(when string, f Facts) bool {
	if strings.TrimSpace(when) == "" {
		return true
	}
	c, err := parseCondition(when)
	if err != nil {
		return false
	}
	return c.eval(f)
}

// --- execution ---

// StepResult records what happened for one step in the run, for the audit/value tests.
type StepResult struct {
	Step    Step
	Skipped bool   // true when its `when` condition was false
	Output  string // tool/verb/merge output (from RunStep), empty when skipped
	Err     error
}

// RunStep performs one step's actual work and returns its output text. The executor
// supplies it so the engine stays deterministic + model-free and so tests can fake tool
// outputs. It MAY mutate facts (e.g. a transcribe step that establishes "speakers").
type RunStep func(step Step, facts Facts) (output string, err error)

// Run executes the recipe in order, evaluating each step's `when` against the (live)
// facts and calling RunStep for the steps that pass. It returns the per-step results.
// Facts are updated live so a `when` can react to what an earlier step established.
func (r Recipe) Run(facts Facts, run RunStep) []StepResult {
	if facts == nil {
		facts = Facts{}
	}
	out := make([]StepResult, 0, len(r.Steps))
	for _, s := range r.Steps {
		if !EvalWhen(s.When, facts) {
			out = append(out, StepResult{Step: s, Skipped: true})
			continue
		}
		o, err := run(s, facts)
		out = append(out, StepResult{Step: s, Output: o, Err: err})
	}
	return out
}

// ExecutedNames returns the names of the steps that actually ran (not skipped) — the
// value the conditional tests assert against ("diarize present or not").
func ExecutedNames(results []StepResult) []string {
	var names []string
	for _, r := range results {
		if !r.Skipped {
			names = append(names, r.Step.Name())
		}
	}
	return names
}
