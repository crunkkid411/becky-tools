// plan.go — deterministic workflow planner for becky-ask.
//
// When a request names 2+ becky capabilities, workflowReply assembles an
// ordered, numbered plan of real becky commands — copy-pasteable, one step per
// tool — instead of an unordered capability list. The plan is deterministic
// (no model needed): it sorts matched capabilities into the canonical pipeline
// order and fills in the user's target where one is set.
//
// This implements SPEC-BECKY-ASK.md §3.3 (b) — "Assembling a workflow."
// Opt-in EXECUTION of the full plan (running all steps in sequence) is Phase 5;
// the TUI's pending model handles one command at a time.
package main

import (
	"fmt"
	"sort"
	"strings"
)

// planStep is one line of a workflow plan: a tool, its adapted command, and a
// plain-English note on what this step produces.
type planStep struct {
	Num     int    // 1-based step number
	Verb    string // tool or orchestrator-op name (from the catalog)
	Command string // adapted, copy-pasteable command
	Why     string // "→ what this step gives you"
}

// stepOrderMap assigns a canonical execution position to each tool or op.
// Lower values run earlier: setup / ingest before per-clip analysis before
// corpus search. Unrecognised verbs get a high default so they appear last.
var stepOrderMap = map[string]int{
	"this is <name>":   0, // KB teaching always first
	"enroll-wiki":      1, // build KB before anything uses it
	"index":            2, // ingest corpus before searching it
	"becky-transcribe": 3,
	"becky-diarize":    4,
	"becky-pipeline":   5, // full pass covers the above steps
	"becky-events":     6,
	"becky-osint":      7,
	"becky-ocr":        8,
	"becky-identify":   9,
	"becky-validate":   10,
	"profile":          11, // corpus-level ops after per-clip analysis
	"appearances":      12,
	"find":             13,
	"corroborate":      14,
	"becky-search":     15,
	"becky-framematch": 16,
	"becky-cut":        17,
	"becky-review":     18,
	"becky-export":     19,
	"becky-web2md":     20,
}

const stepOrderDefault = 99

// stepPos returns the canonical execution position for a verb.
func stepPos(verb string) int {
	if p, ok := stepOrderMap[verb]; ok {
		return p
	}
	return stepOrderDefault
}

// adaptCommand replaces the most common path placeholders in a catalog Example
// with the user's actual target path (when one is set). Placeholders that need
// user-supplied values (<query>, <claim>, <name>, <url>, etc.) are left intact
// so the user knows exactly what to fill in.
func adaptCommand(example string, t Target) string {
	if !t.HasTarget() {
		return example
	}
	p := t.Primary()
	q := `"` + p + `"`

	s := example
	if t.Kind == targetDir {
		// Folder target: fill directory-shaped placeholders.
		s = strings.ReplaceAll(s, `"<folder>"`, q)
		s = strings.ReplaceAll(s, `"<corpus-dir>"`, q)
		s = strings.ReplaceAll(s, `"<wiki-dir>"`, q)
		s = strings.ReplaceAll(s, `"<frames-dir>"`, q)
		s = strings.ReplaceAll(s, `"<video-or-folder>"`, q)
	} else {
		// Single file: fill video/clip-shaped placeholders.
		s = strings.ReplaceAll(s, `"<video>"`, q)
		s = strings.ReplaceAll(s, `"<clip.mp4>"`, q)
		s = strings.ReplaceAll(s, `"<video-or-folder>"`, q)
	}
	return s
}

// buildWorkflowPlan sorts the matched capabilities into canonical pipeline order
// and adapts each example command to the user's target.
func buildWorkflowPlan(hits []capability, t Target) []planStep {
	sorted := make([]capability, len(hits))
	copy(sorted, hits)
	sort.SliceStable(sorted, func(i, j int) bool {
		return stepPos(sorted[i].Verb) < stepPos(sorted[j].Verb)
	})

	steps := make([]planStep, 0, len(sorted))
	for i, c := range sorted {
		steps = append(steps, planStep{
			Num:     i + 1,
			Verb:    c.Verb,
			Command: adaptCommand(c.Example, t),
			Why:     c.Summary,
		})
	}
	return steps
}

// hasOpenPlaceholder reports whether the command still contains an
// angle-bracket placeholder that the user needs to fill in.
func hasOpenPlaceholder(cmd string) bool {
	return strings.Contains(cmd, "<") && strings.Contains(cmd, ">")
}

// workflowReply renders a numbered step plan for a multi-capability request.
// It replaces capabilityReply when 2+ tools match: a plan is more useful than
// a bulleted list because it shows the right execution ORDER (ingest before
// search, for example) and ready-to-run commands with the user's paths filled in.
func workflowReply(hits []capability, t Target) string {
	steps := buildWorkflowPlan(hits, t)
	n := len(steps)

	var b strings.Builder
	var intro string
	if t.HasTarget() {
		intro = fmt.Sprintf("Here's a %d-step plan for %s:", n, t.Label())
	} else {
		intro = fmt.Sprintf("Here's a %d-step plan:", n)
	}
	b.WriteString(beckyStyle.Render(intro))
	b.WriteString("\n")

	anyPlaceholder := false
	for _, s := range steps {
		b.WriteString("\n")
		b.WriteString(beckyStyle.Render(fmt.Sprintf("  %d. ", s.Num)))
		b.WriteString(systemStyle.Render(s.Command))
		b.WriteString("\n")
		b.WriteString("     " + beckyStyle.Render("→ ") + s.Why)
		if hasOpenPlaceholder(s.Command) {
			anyPlaceholder = true
		}
	}
	b.WriteString("\n\n")
	if anyPlaceholder {
		b.WriteString(systemStyle.Render("Commands with <angle brackets> need your actual file/folder paths."))
		b.WriteString("\n")
	}
	b.WriteString(systemStyle.Render("Drop a file or folder onto becky-ask for instant one-key actions."))
	return b.String()
}
