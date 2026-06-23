package edittools

import (
	"sort"
	"strings"
)

// ToolSpec is the public, model-facing description of one tool. internal/ctlagent
// turns the slice into the system prompt's tool list; the dock can render the same
// data as a palette. (toolDef stays private; ToolSpec exposes only metadata.)
type ToolSpec struct {
	Verb     string      `json:"verb"`
	Category string      `json:"category"`
	Mutating bool        `json:"mutating"`
	Summary  string      `json:"summary"`
	Params   []ParamSpec `json:"params"`
}

// Describe returns every tool's spec, sorted by category then verb, for the model
// prompt + the UI. Deterministic order so the prompt (and tests) are stable.
func Describe() []ToolSpec {
	out := make([]ToolSpec, 0, len(registry))
	for _, d := range registry {
		out = append(out, ToolSpec{
			Verb: string(d.verb), Category: d.category, Mutating: d.mutating,
			Summary: d.summary, Params: d.params,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Verb < out[j].Verb
	})
	return out
}

// ToolList renders the allowlist as a compact block for the model's system
// prompt: one line per tool, signature + summary, grouped by category. Required
// params are bare; optional ones are wrapped in [brackets].
//
//	# timeline
//	add_clip(source, in, out, [track], [pos], [label]) — Append a clip ...
func ToolList() string {
	specs := Describe()
	var b strings.Builder
	var cat string
	for _, s := range specs {
		if s.Category != cat {
			cat = s.Category
			b.WriteString("# " + cat + "\n")
		}
		b.WriteString(signature(s))
		b.WriteString(" — ")
		b.WriteString(s.Summary)
		b.WriteString("\n")
	}
	return b.String()
}

// signature renders "verb(req, [opt], …)".
func signature(s ToolSpec) string {
	parts := make([]string, 0, len(s.Params))
	for _, p := range s.Params {
		if p.Required {
			parts = append(parts, p.Name)
		} else {
			parts = append(parts, "["+p.Name+"]")
		}
	}
	return s.Verb + "(" + strings.Join(parts, ", ") + ")"
}
