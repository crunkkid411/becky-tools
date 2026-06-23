// Package edittools is the deterministic TOOL layer for becky-edit: the
// fixed, default-deny set of verbs the embedded Gemma agent (internal/ctlagent)
// and the Becky dock can call to "actually affect the program" (Jordan). Each
// tool is a pure state transition over the SHARED editor state
// (internal/editmodel.Project): it validates its args, mutates a CLONE of the
// project, and emits one or more HostCommands — the abstract instructions the
// forked Shotcut dock translates into real host calls (MAIN.open / Player.seek /
// TimelineDock.append / AttachedFiltersModel.add — see research/shotcut-api.md).
//
// Why a tool layer and not "the model writes code": video-db/Director lets its
// LLM write Python that an `exec()` sandbox runs. On Jordan's forensic machine
// that is unacceptable. becky instead constrains the model to this allowlist of
// JSON tool-calls; an unknown verb or a missing/over-range arg is REJECTED, never
// executed, and the typed failure is fed back so the model can self-correct (the
// one Director concept worth stealing — the mechanism, not the exec()).
//
// Categories (the basic functionality Jordan named): timeline, controls, effects,
// audio, render, vision, search. Every verb dispatches to exactly one handler.
//
// Pure Go: no models, no ffmpeg, no host. The render/vision/search verbs only
// EMIT a HostCommand describing the work; the bridge (cmd/becky-edit) runs the
// real ffmpeg/avlm/search behind that command. So this whole package builds and
// unit-tests GREEN offline.
package edittools

import (
	"fmt"
	"sort"

	"becky-go/internal/editmodel"
)

// Verb is one allowlisted tool name. The model may emit only these.
type Verb string

// Args carries a tool call's parameters. Values arrive from JSON (float64 / string
// / bool) — the arg* helpers normalise them so handlers never type-assert raw.
type Args map[string]any

// ToolCall is one validated instruction the model (or the dock) emits.
type ToolCall struct {
	Verb Verb `json:"verb"`
	Args Args `json:"args,omitempty"`
}

// Result is the typed envelope a tool returns. It is fed straight back into the
// agent loop as the tool's reply (Director's role=tool message), so the model can
// decide the next step or repair a failure. OK=false is a normal, recoverable
// outcome (bad arg, missing clip) — NOT a Go error.
type Result struct {
	OK      bool           `json:"ok"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

// HostCommand is an abstract instruction for the host (the forked Shotcut dock).
// Name is a stable verb the dock maps to a concrete Shotcut/MLT call; Args carry
// its parameters. becky supplies the WHAT; the host does the HOW.
type HostCommand struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

// ParamSpec describes one tool parameter for the model-facing tool list + for
// validation (Required is enforced before the handler runs).
type ParamSpec struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // "number" | "string" | "bool"
	Required bool   `json:"required"`
	Desc     string `json:"desc"`
}

// handler mutates the already-cloned project and returns the typed result + the
// host commands. It must NOT bump Rev (the dispatcher does, for mutating verbs).
type handler func(p *editmodel.Project, a Args) (Result, []HostCommand)

// toolDef is one registered tool: its metadata (for the model prompt + the UI)
// and its handler.
type toolDef struct {
	verb     Verb
	category string // timeline | controls | effects | audio | render | vision | search
	mutating bool   // true => changes Project state => Rev bumps + needs approval
	summary  string // one prose line the model reads to choose this tool
	params   []ParamSpec
	apply    handler
}

// registry is the assembled allowlist, built once from the per-category files.
var registry = buildRegistry()

// buildRegistry concatenates the per-file tool sets into the verb→def map and
// fails loudly (panic at init) on a duplicate verb — a programming error.
func buildRegistry() map[Verb]toolDef {
	all := []toolDef{}
	all = append(all, timelineTools()...)
	all = append(all, controlTools()...)
	all = append(all, effectTools()...)
	all = append(all, audioTools()...)
	all = append(all, readTools()...)
	m := make(map[Verb]toolDef, len(all))
	for _, t := range all {
		if _, dup := m[t.verb]; dup {
			panic("edittools: duplicate verb " + string(t.verb))
		}
		m[t.verb] = t
	}
	return m
}

// IsAllowed reports whether v is a registered tool.
func IsAllowed(v Verb) bool { _, ok := registry[v]; return ok }

// IsMutating reports whether v changes Project state (and so needs the
// propose-preview-apply approval gate). Unknown verbs report false.
func IsMutating(v Verb) bool { d, ok := registry[v]; return ok && d.mutating }

// Verbs returns the allowlisted verbs, sorted, for tests + the UI.
func Verbs() []Verb {
	out := make([]Verb, 0, len(registry))
	for v := range registry {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Apply is the ONE dispatch entry point. It validates the call against the
// allowlist + the tool's required args, then runs the handler on a CLONE of p so
// the original is never mutated on failure. On success of a mutating verb the
// clone's Rev is bumped. Returns the (possibly new) project, the host commands to
// forward on approval, and the typed Result. The original p is returned unchanged
// whenever the result is not OK.
func Apply(p *editmodel.Project, call ToolCall) (*editmodel.Project, []HostCommand, Result) {
	def, ok := registry[call.Verb]
	if !ok {
		return p, nil, fail("unknown tool %q (not in the allowlist)", call.Verb)
	}
	if err := validateArgs(def, call.Args); err != nil {
		return p, nil, fail("%s: %v", call.Verb, err)
	}
	work := p.Clone()
	res, host := def.apply(work, call.Args)
	if !res.OK {
		return p, nil, res // unchanged on failure
	}
	if def.mutating {
		work.BumpRev()
	}
	return work, host, res
}

// validateArgs enforces that every required param is present and of the declared
// kind. It is intentionally strict — the default-deny posture extends to args.
func validateArgs(def toolDef, a Args) error {
	for _, ps := range def.params {
		if !ps.Required {
			continue
		}
		v, present := a[ps.Name]
		if !present || v == nil {
			return fmt.Errorf("missing required arg %q", ps.Name)
		}
		switch ps.Type {
		case "number":
			if _, ok := toFloat(v); !ok {
				return fmt.Errorf("arg %q must be a number, got %T", ps.Name, v)
			}
		case "string":
			if s, ok := v.(string); !ok || s == "" {
				return fmt.Errorf("arg %q must be a non-empty string", ps.Name)
			}
		case "bool":
			if _, ok := toBool(v); !ok {
				return fmt.Errorf("arg %q must be a bool", ps.Name)
			}
		}
	}
	return nil
}

// --- result + arg helpers ---------------------------------------------------

func ok(msg string, data map[string]any) (Result, []HostCommand) {
	return Result{OK: true, Message: msg, Data: data}, nil
}

func okHost(msg string, data map[string]any, host ...HostCommand) (Result, []HostCommand) {
	return Result{OK: true, Message: msg, Data: data}, host
}

func failR(format string, a ...any) (Result, []HostCommand) {
	return fail(format, a...), nil
}

func fail(format string, a ...any) Result {
	return Result{OK: false, Message: fmt.Sprintf(format, a...)}
}

// argFloat returns a float arg and whether it was present + numeric.
func argFloat(a Args, key string) (float64, bool) {
	v, ok := a[key]
	if !ok {
		return 0, false
	}
	return toFloat(v)
}

// argString returns a string arg + presence.
func argString(a Args, key string) (string, bool) {
	v, ok := a[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// argInt returns an int arg + presence (coerced from a JSON number).
func argInt(a Args, key string) (int, bool) {
	f, ok := argFloat(a, key)
	if !ok {
		return 0, false
	}
	return int(f), true
}

// argBool returns a bool arg + presence.
func argBool(a Args, key string) (bool, bool) {
	v, ok := a[key]
	if !ok {
		return false, false
	}
	return toBool(v)
}

// toFloat coerces a JSON value (float64, int, or numeric string) to float64.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case string:
		var f float64
		if _, err := fmt.Sscanf(n, "%g", &f); err == nil {
			return f, true
		}
	}
	return 0, false
}

// toBool coerces a JSON bool or the strings true/false/on/off/1/0.
func toBool(v any) (bool, bool) {
	switch b := v.(type) {
	case bool:
		return b, true
	case string:
		switch b {
		case "true", "on", "1", "yes":
			return true, true
		case "false", "off", "0", "no":
			return false, true
		}
	case float64:
		return b != 0, true
	}
	return false, false
}
