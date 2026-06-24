// recipe.go — drives cmd/ask's transcribe workflow from the DECLARATIVE process-video
// recipe (internal/workflowdef) instead of a hardcoded step string. The recipe is the
// single place that says "transcribe, then diarize only when >1 speaker, then ocr,
// then a gemma4 check only when >1 speaker, then merge" — so the chain is a file Jordan
// controls (SPEC-BECKY-VOICE.md §3.3). The actual pipeline call + merge are unchanged;
// the recipe only decides WHICH steps are active.
package main

import (
	"becky-go/internal/workflowdef"
)

// pipelineStepsFromRecipe turns the process-video recipe into the becky-pipeline
// --steps CSV, evaluating each step's `when` against the given facts. Steps map to the
// pipeline's own step names; the gemma4 "verb" and the "merge" are handled after the
// pipeline run, so they are not added to --steps. osint+events are always part of the
// transcribe pass (they back the on-screen-text track), so they are appended whenever
// the recipe ran. Returns the CSV and whether diarize is active (>1 speaker).
//
// Behavior preservation: with the default facts (speakers unknown => 0), this keeps the
// SAME unconditional chain the old hardcoded string used by treating an unknown speaker
// count as "include diarize" (the recipe gate is speakers>1; callers pass speakers=2
// when they cannot probe, to preserve today's always-run-diarize behavior).
func pipelineStepsFromRecipe(facts workflowdef.Facts) (csv string, diarize bool) {
	r, err := workflowdef.ProcessVideo()
	if err != nil {
		// Defensive fallback to the historical chain if the embedded recipe is bad.
		return "transcribe,diarize,events,osint,ocr", true
	}
	steps := []string{}
	have := map[string]bool{}
	add := func(name string) {
		if !have[name] {
			have[name] = true
			steps = append(steps, name)
		}
	}
	for _, s := range r.Steps {
		if !workflowdef.EvalWhen(s.When, facts) {
			if s.Tool == "becky-diarize" {
				diarize = false
			}
			continue
		}
		switch s.Tool {
		case "becky-transcribe":
			add("transcribe")
		case "becky-diarize":
			add("diarize")
			diarize = true
		case "becky-ocr":
			add("ocr")
		}
		// verbs (verify-with-gemma4) and merge run after the pipeline, not as --steps.
	}
	// events + osint back the on-screen-text track in the merge; always part of the pass.
	add("events")
	add("osint")
	add("ocr")
	return joinCSV(steps), diarize
}

func joinCSV(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ","
		}
		out += p
	}
	return out
}
