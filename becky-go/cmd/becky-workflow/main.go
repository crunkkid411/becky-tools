// Command becky-workflow RUNS a workflow recipe file (workflows/<name>.json) end to
// end: it executes each step in order — a becky-*.exe tool, a MERGE of prior outputs,
// or an OPT-IN AI agent step — and prints one JSON summary.
//
// This is the runnable front door for internal/workflowdef (the declarative recipe
// engine). A recipe is a small file Jordan reads and edits: a name, trigger phrases,
// and an ordered list of steps, any step optionally gated by a deterministic `when`
// (e.g. "speakers > 1"). The engine is deterministic and spends ZERO AI tokens unless
// a recipe explicitly contains an `agent` step — the whole point vs Archon, which
// burns a model call every run.
//
// Usage:
//
//	becky-workflow run <recipe.json|name> --target "<video>"   # run a recipe on a file
//	becky-workflow run watch-video --target "clip.mp4" --model claude-opus-4-8
//	becky-workflow list [--dir <workflows-dir>]                # list available recipes
//	becky-workflow --selftest                                  # offline proof, exits 0 on PASS
//
// A `tool` step shells the sibling becky tool on --target (forensicrun.RunTool). An
// `agent` step runs a headless AI agent (claude-code today) over the prior tool outputs
// using the step's `prompt`. A `merge` step bundles the tool outputs into one JSON blob.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"becky-go/internal/agentrun"
	"becky-go/internal/forensicrun"
	"becky-go/internal/workflowdef"
)

// runToolFn shells a sibling becky tool over the target; swapped in --selftest so the
// proof needs no real .exe. Returns the tool's stdout.
var runToolFn = func(ctx context.Context, tool, target string) (string, error) {
	var args []string
	if strings.TrimSpace(target) != "" {
		args = append(args, target)
	}
	b, err := forensicrun.RunTool(ctx, tool, args...)
	return string(b), err
}

// runAgentFn runs a headless AI agent over the assembled prompt; swapped in --selftest.
// Only "claude-code" is wired today (via internal/agentrun); "qwen" is a documented TODO.
var runAgentFn = func(ctx context.Context, agent, prompt, model string, budget float64) (string, error) {
	switch strings.ToLower(strings.TrimSpace(agent)) {
	case "claude-code", "claude", "":
		spec := agentrun.AgentSpec{
			PromptStdin:  prompt,
			Model:        model, // "" => the claude CLI default
			MaxBudgetUSD: budget,
			MaxTurns:     8,
			WorkDir:      os.TempDir(), // neutral dir: don't auto-load a project CLAUDE.md
		}
		res, err := agentrun.Run(ctx, spec)
		if err != nil {
			return "", err
		}
		if res.IsError {
			return res.Result, fmt.Errorf("agent returned an error (%s)", res.Subtype)
		}
		return res.Result, nil
	default:
		return "", fmt.Errorf("agent %q is not wired yet (only claude-code today)", agent)
	}
}

// namedOutput is one step's captured output, in run order, for merge + the agent prompt.
type namedOutput struct {
	name string
	kind string
	text string
}

// runner carries per-run config and the collected step outputs so an agent/merge step
// can see what the tools before it produced.
type runner struct {
	target  string
	model   string
	budget  float64
	outputs []namedOutput
}

// run executes one recipe step and returns its output text. It is the RunStep the
// workflowdef engine calls for each step whose `when` passed.
func (rn *runner) run(ctx context.Context, step workflowdef.Step, _ workflowdef.Facts) (string, error) {
	switch step.Kind() {
	case "tool":
		out, err := runToolFn(ctx, step.Tool, rn.target)
		if err == nil {
			rn.outputs = append(rn.outputs, namedOutput{step.Tool, "tool", out})
		}
		return out, err
	case "agent":
		out, err := runAgentFn(ctx, step.Agent, rn.buildAgentPrompt(step.Prompt), rn.model, rn.budget)
		if err == nil {
			rn.outputs = append(rn.outputs, namedOutput{step.Agent, "agent", out})
		}
		return out, err
	case "merge":
		out := rn.mergeOutputs()
		rn.outputs = append(rn.outputs, namedOutput{step.Merge, "merge", out})
		return out, nil
	case "verb":
		// A `verb` (e.g. "verify-with-gemma4") is an orchestrator op run by the forensic
		// runtime (becky-transcribe --forensic), not by this batch runner. Record it as a
		// note so a recipe containing one still runs cleanly instead of hard-failing.
		note := fmt.Sprintf("(verb %q runs via becky-transcribe --forensic, not executed here)", step.Verb)
		rn.outputs = append(rn.outputs, namedOutput{step.Verb, "verb", note})
		return note, nil
	default:
		return "", fmt.Errorf("step has no tool/verb/merge/agent")
	}
}

// buildAgentPrompt feeds the agent its instruction plus the prior TOOL outputs, so it
// reasons over what the local tools extracted instead of re-watching anything.
func (rn *runner) buildAgentPrompt(instruction string) string {
	var b strings.Builder
	b.WriteString(instruction)
	b.WriteString("\n\nAnswer ONLY from the tool output below. Do not browse or call tools.\n")
	if rn.target != "" {
		b.WriteString("Target: " + rn.target + "\n")
	}
	for _, o := range rn.outputs {
		if o.kind != "tool" {
			continue
		}
		b.WriteString("\n## " + o.name + "\n")
		b.WriteString(truncate(o.text, 40000))
		b.WriteString("\n")
	}
	return b.String()
}

// mergeOutputs bundles the tool outputs into one JSON object keyed by tool name (raw
// JSON preserved when the tool emitted JSON, else quoted as a string).
func (rn *runner) mergeOutputs() string {
	m := map[string]json.RawMessage{}
	for _, o := range rn.outputs {
		if o.kind != "tool" {
			continue
		}
		if json.Valid([]byte(o.text)) {
			m[o.name] = json.RawMessage(o.text)
		} else {
			q, _ := json.Marshal(o.text)
			m[o.name] = q
		}
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	return string(b)
}

// --- summary ---

type stepReport struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Ran     bool   `json:"ran"`
	Error   string `json:"error,omitempty"`
	Preview string `json:"preview,omitempty"`
}

func printSummary(recipe workflowdef.Recipe, target string, results []workflowdef.StepResult) {
	steps := make([]stepReport, 0, len(results))
	for _, r := range results {
		sr := stepReport{Name: r.Step.Name(), Kind: r.Step.Kind(), Ran: !r.Skipped}
		if r.Err != nil {
			sr.Error = r.Err.Error()
		}
		if !r.Skipped && r.Output != "" {
			sr.Preview = truncate(oneLine(r.Output), 240)
		}
		steps = append(steps, sr)
	}
	var result string
	if n := len(results); n > 0 && results[n-1].Err == nil {
		result = results[n-1].Output
	}
	out := map[string]any{"recipe": recipe.Name, "target": target, "steps": steps, "result": result}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
}

// --- recipe loading ---

func loadRecipe(nameOrPath, dir string) (workflowdef.Recipe, error) {
	// 1. An explicit .json path always wins.
	if strings.HasSuffix(strings.ToLower(nameOrPath), ".json") {
		if _, err := os.Stat(nameOrPath); err == nil {
			return workflowdef.Load(nameOrPath)
		}
	}
	// 2. A user recipe of that name in the workflows dir shadows a built-in.
	d := resolveDir(dir)
	cand := filepath.Join(d, nameOrPath)
	if !strings.HasSuffix(strings.ToLower(cand), ".json") {
		cand += ".json"
	}
	if _, err := os.Stat(cand); err == nil {
		return workflowdef.Load(cand)
	}
	// 3. A built-in standard recipe (always available, no folder needed).
	if r, ok := builtinRecipes()[nameOrPath]; ok {
		return r, nil
	}
	return workflowdef.Recipe{}, fmt.Errorf("recipe %q not found (built-ins: %v; or give a .json path; or a file in %s)", nameOrPath, builtinNames(), d)
}

// resolveDir picks the workflows directory: --dir, else ./workflows, else <exe>/workflows.
func resolveDir(dir string) string {
	if dir != "" {
		return dir
	}
	if fi, err := os.Stat("workflows"); err == nil && fi.IsDir() {
		return "workflows"
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), "workflows")
		if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
			return cand
		}
	}
	return "workflows"
}

// --- subcommands ---

func runRecipe(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	target := fs.String("target", "", "the file/folder the tools run on")
	model := fs.String("model", "", "agent model for agent steps (e.g. claude-opus-4-8); empty = CLI default")
	budget := fs.Float64("budget", 0.50, "max USD per agent step (safety cap)")
	dir := fs.String("dir", "", "workflows directory (default: ./workflows or next to the exe)")
	timeout := fs.Duration("timeout", 30*time.Minute, "overall run timeout")
	var facts multiFlag
	fs.Var(&facts, "fact", "a key=value fact for `when` conditions, repeatable (e.g. --fact speakers=2)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) < 1 {
		fmt.Fprintln(os.Stderr, "usage: becky-workflow run <recipe.json|name> --target \"<path>\"")
		return 2
	}
	recipe, err := loadRecipe(rest[0], *dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	f := workflowdef.Facts{}
	for _, kv := range facts {
		if k, v, ok := parseFact(kv); ok {
			f[k] = v
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	rn := &runner{target: *target, model: *model, budget: *budget}
	results := recipe.Run(f, func(step workflowdef.Step, ff workflowdef.Facts) (string, error) {
		return rn.run(ctx, step, ff)
	})
	printSummary(recipe, *target, results)
	for _, r := range results {
		if !r.Skipped && r.Err != nil {
			return 1
		}
	}
	return 0
}

func runListCmd(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	dir := fs.String("dir", "", "workflows directory (default: ./workflows or next to the exe)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	seen := map[string]bool{}
	fmt.Println("built-in (always available):")
	for _, n := range builtinNames() {
		r := builtinRecipes()[n]
		fmt.Printf("  %-24s  %s\n", r.Name, strings.Join(r.Phrases, " / "))
		seen[r.Name] = true
	}
	d := resolveDir(*dir)
	entries, err := os.ReadDir(d)
	if err != nil {
		return 0 // no user workflows dir is fine — the built-ins stand alone
	}
	header := false
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		r, err := workflowdef.Load(filepath.Join(d, e.Name()))
		if err != nil || seen[r.Name] {
			continue
		}
		if !header {
			fmt.Printf("from %s:\n", d)
			header = true
		}
		fmt.Printf("  %-24s  %s\n", r.Name, strings.Join(r.Phrases, " / "))
	}
	return 0
}

// --- small helpers ---

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(s string) error {
	*m = append(*m, s)
	return nil
}

func parseFact(kv string) (string, float64, bool) {
	i := strings.IndexByte(kv, '=')
	if i <= 0 {
		return "", 0, false
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(kv[i+1:]), 64)
	if err != nil {
		return "", 0, false
	}
	return strings.TrimSpace(kv[:i]), v, true
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "...(truncated)"
	}
	return s
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

func usage() {
	fmt.Fprintln(os.Stderr, `becky-workflow — run a workflow recipe file

  becky-workflow run <recipe.json|name> --target "<video>"   run a recipe on a file
  becky-workflow list [--dir <dir>]                          list available recipes
  becky-workflow --selftest                                  offline proof

Run flags: --target <path>  --model <name>  --budget <usd>  --fact key=val  --dir <dir>  --timeout <dur>`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "--selftest", "-selftest":
		os.Exit(runSelfTest())
	case "list":
		os.Exit(runListCmd(os.Args[2:]))
	case "run":
		os.Exit(runRecipe(os.Args[2:]))
	default:
		usage()
		os.Exit(2)
	}
}
