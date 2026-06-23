// Package ctlagent is the multi-step AGENT LOOP for becky-edit: the embedded
// Gemma model edits the forensic timeline by calling deterministic tools, seeing
// the updated state, and deciding the next step — the feedback loop Jordan asked
// for ("regardless of if I manually make edits or if it does things, that
// built-in model and the program need to share state … everytime something
// happens it needs to have updated state").
//
// Shape (validated against video-db/Director's reasoning engine; see
// research/director-videodb-mining.md): a capped loop where, each step, the model
// is shown the COMPACT state digest (editmodel.Digest) + the last tool result,
// emits ONE JSON tool call, and the result + new digest are fed back. A failed or
// unparseable step is fed back as a typed error so the model self-repairs — the
// one Director mechanism worth borrowing. Unlike Director we never exec() model
// code: the only control surface is edittools' default-deny allowlist.
//
// Propose-preview-apply: Run operates on a CLONE of the starting project and
// returns the proposed tool sequence + the resulting state + the accumulated host
// commands. It NEVER mutates the caller's project or the real Shotcut timeline;
// the bridge presents the proposal and commits only on the human's approval (the
// forensic non-negotiable). The loop is transport-agnostic: any Model (the warm
// in-process Gemma via internal/llmlocal in production, a scripted fake in tests).
package ctlagent

import (
	"context"
	"fmt"

	"becky-go/internal/editmodel"
	"becky-go/internal/edittools"
)

// Model is the embedded-LLM transport the loop drives. internal/llmlocal.Client
// satisfies this (one system+user exchange → text). Kept tiny so a fake Model in
// tests scripts deterministic replies.
type Model interface {
	Complete(ctx context.Context, system, user string) (string, error)
}

// Enricher lets the bridge run the REAL work behind a read/produce verb (search →
// transcript hits, vision → Gemma's answer, render → ffmpeg) and fold the output
// into the Result the model sees next turn. nil = identity (tests + the pure path
// just use the tool's emitted-command result). Only called for non-mutating verbs.
type Enricher func(ctx context.Context, call edittools.ToolCall, res edittools.Result, host []edittools.HostCommand) edittools.Result

// Options tune one Run. Zero values use safe defaults.
type Options struct {
	MaxSteps   int      // hard cap on loop iterations (default 8)
	MaxRepairs int      // consecutive parse/tool failures tolerated before aborting (default 2)
	Enrich     Enricher // optional real-execution hook for read verbs
	Log        func(format string, a ...any)
}

func (o Options) withDefaults() Options {
	if o.MaxSteps <= 0 {
		o.MaxSteps = 8
	}
	if o.MaxRepairs <= 0 {
		o.MaxRepairs = 2
	}
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	return o
}

// Step is one iteration of the loop, recorded for the transcript + the preview.
type Step struct {
	Thought string                  `json:"thought,omitempty"`
	Call    edittools.ToolCall      `json:"call,omitempty"`
	Result  edittools.Result        `json:"result"`
	Host    []edittools.HostCommand `json:"host,omitempty"`
	Error   string                  `json:"error,omitempty"` // parse/validate error fed back for self-repair
}

// Result is the whole loop outcome: the proposed sequence + the resulting (clone)
// project + the accumulated host commands the bridge sends on approval.
type Result struct {
	Goal    string                  `json:"goal"`
	Steps   []Step                  `json:"steps"`
	Applied []edittools.ToolCall    `json:"applied"`           // the successful calls, in order
	Host    []edittools.HostCommand `json:"host,omitempty"`    // accumulated host commands
	Final   *editmodel.Project      `json:"final"`             // working clone after the loop
	Message string                  `json:"message"`           // the model's "done" summary (or a fallback)
	Done    bool                    `json:"done"`              // model signalled completion
	Aborted string                  `json:"aborted,omitempty"` // why the loop stopped early, if it did
}

// Run drives the loop. It returns a Result describing the proposed edits; an error
// is returned ONLY when the model transport itself fails (the bridge then degrades
// to a deterministic path). A goal the model cannot satisfy is a normal Result
// with Done=false + an Aborted reason, not an error.
func Run(ctx context.Context, model Model, start *editmodel.Project, goal string, opts Options) (Result, error) {
	opts = opts.withDefaults()
	system := systemPrompt(edittools.ToolList())
	work := start.Clone()
	run := Result{Goal: goal, Final: work}

	lastResult := "" // fed into the next user turn; empty on the first step
	repairs := 0

	for step := 0; step < opts.MaxSteps; step++ {
		user := userPrompt(goal, work.Digest(), lastResult)
		out, err := model.Complete(ctx, system, user)
		if err != nil {
			return run, fmt.Errorf("agent model failed at step %d: %w", step+1, err)
		}

		act, perr := parseAction(out)
		if perr != nil {
			opts.Log("ctlagent: step %d parse error: %v", step+1, perr)
			run.Steps = append(run.Steps, Step{Error: perr.Error()})
			lastResult = "Your last reply was not a single valid JSON tool call (" + perr.Error() + "). Reply with exactly one JSON object."
			if repairs++; repairs > opts.MaxRepairs {
				run.Aborted = "gave up after repeated unparseable replies"
				break
			}
			continue
		}

		if act.Done {
			run.Done = true
			run.Message = act.Message
			run.Steps = append(run.Steps, Step{Thought: act.Thought, Result: edittools.Result{OK: true, Message: act.Message}})
			break
		}

		call := edittools.ToolCall{Verb: edittools.Verb(act.Tool), Args: act.Args}
		newWork, host, res := edittools.Apply(work, call)
		if !res.OK {
			opts.Log("ctlagent: step %d tool %q failed: %s", step+1, act.Tool, res.Message)
			run.Steps = append(run.Steps, Step{Thought: act.Thought, Call: call, Result: res})
			lastResult = "Tool " + act.Tool + " failed: " + res.Message + ". Fix the arguments and try again, or finish."
			if repairs++; repairs > opts.MaxRepairs {
				run.Aborted = "gave up after repeated tool failures"
				break
			}
			continue
		}
		repairs = 0

		// Read/produce verbs may be enriched with their real output by the bridge.
		if opts.Enrich != nil && !edittools.IsMutating(call.Verb) {
			res = opts.Enrich(ctx, call, res, host)
		}

		work = newWork
		run.Final = work
		run.Applied = append(run.Applied, call)
		run.Host = append(run.Host, host...)
		run.Steps = append(run.Steps, Step{Thought: act.Thought, Call: call, Result: res, Host: host})
		lastResult = resultLine(call, res)
	}

	if !run.Done && run.Aborted == "" && len(run.Applied) == 0 {
		run.Aborted = "reached the step limit without a usable edit"
	}
	if run.Message == "" {
		run.Message = fallbackMessage(run)
	}
	return run, nil
}

// resultLine is the compact "what just happened" line fed into the next turn.
func resultLine(call edittools.ToolCall, res edittools.Result) string {
	if ans, ok := res.Data["answer"].(string); ok && ans != "" {
		return string(call.Verb) + " -> " + ans
	}
	return string(call.Verb) + " -> " + res.Message
}

// fallbackMessage summarises the run when the model never sent an explicit "done".
func fallbackMessage(r Result) string {
	if len(r.Applied) == 0 {
		if r.Aborted != "" {
			return "No edits made (" + r.Aborted + ")."
		}
		return "No edits made."
	}
	return fmt.Sprintf("Proposed %d edit(s).", len(r.Applied))
}
