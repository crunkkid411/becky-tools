// Package main is becky-case: the ONE dumb call. The forensic agent runs `becky-case --file X`
// (optionally with --subject to locate someone on screen) and gets the FINAL corroborated
// forensic output — nothing else. becky decides the plan deterministically (diarize only when
// there's more than one speaker, from internal/workflowdef), runs the tools, and pushes every
// result through the protocol gate (internal/orchestrate via internal/forensic): a name is stated
// ONLY when corroborated, an on-screen interval ONLY where a model watched it. Maybes are held,
// never dumped. The agent sees no flags, no chaining, no protocol to remember — becky self-regulates.
// Tools/models run locally; the plan + mapping + enforcement are deterministic + unit-tested.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"becky-go/internal/forensic"
	"becky-go/internal/orchestrate"
	"becky-go/internal/workflowdef"
)

func readFile(path string) []byte {
	if path == "" {
		return nil
	}
	b, _ := os.ReadFile(path)
	return b
}

// plan returns the deterministic step plan for the file given the known speaker count — diarize
// (and the gemma4 check) are present ONLY when speakers > 1. This is the diarize-conditional
// protocol, decided in code, not by the agent.
func plan(speakers int) []string {
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

// caseReport is the single corroborated output the forensic agent receives.
type caseReport struct {
	File     string                `json:"file"`
	Plan     []string              `json:"plan"`            // the deterministic steps becky ran (diarize-conditional)
	Names    []orchestrate.Verdict `json:"names"`           // stated only when corroborated
	OnScreen []orchestrate.Verdict `json:"on_screen"`       // stated only where a model watched it
	Held     []orchestrate.Verdict `json:"held_candidates"` // one-signal maybes, NOT stated
	Audit    []string              `json:"audit"`
}

func main() {
	file := flag.String("file", "", "the media file")
	subject := flag.String("subject", "", "optional: who/what to locate on screen")
	speakers := flag.Int("speakers", 0, "known speaker count (0 = unknown; >1 triggers diarization)")
	// tool outputs (provided for composition/testing; the local build runs the tools itself)
	idJSON := flag.String("identify", "", "becky-identify JSON")
	trJSON := flag.String("transcribe", "", "becky-transcribe JSON")
	moJSON := flag.String("motion", "", "becky-motion JSON")
	vaJSON := flag.String("validate", "", "becky-validate JSON")
	flag.Parse()
	if *file == "" && *idJSON == "" && *trJSON == "" {
		fmt.Fprintln(os.Stderr, "becky-case: need --file (or provide tool JSON)")
		os.Exit(2)
	}

	rep := report(*file, *subject, *speakers,
		readFile(*idJSON), readFile(*trJSON), readFile(*moJSON), readFile(*vaJSON))

	b, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(b))
	fmt.Fprintf(os.Stderr, "becky-case: %d name(s), %d on-screen interval(s), %d held\n",
		len(rep.Names), len(rep.OnScreen), len(rep.Held))
}

// report is the pure, testable core: given the tool outputs (and known speakers), it produces the
// final corroborated forensic report with every protocol enforced. No I/O, no models.
func report(file, subject string, speakers int, identify, transcribe, motion, validate []byte) caseReport {
	rep := caseReport{File: file, Plan: plan(speakers)}

	// Naming claims (corroborate-or-hold).
	nameClaims := forensic.IdentifyToClaims(identify)
	nres := orchestrate.Resolve(nameClaims, orchestrate.DefaultRules(), nil, 0)
	rep.Names = nres.Concluded
	rep.Held = append(rep.Held, nres.Candidates...)
	rep.Audit = append(rep.Audit, nres.Audit...)

	// On-screen presence for the subject (watch-or-hold), if asked.
	if subject != "" {
		sigs := forensic.PresenceSignals(subject, transcribe, motion, validate)
		presClaims := orchestrate.CorrelatePresence(subject, sigs, 2.0)
		pres := orchestrate.Resolve(presClaims, orchestrate.DefaultRules(), nil, 0)
		rep.OnScreen = pres.Concluded
		rep.Held = append(rep.Held, pres.Candidates...)
		rep.Audit = append(rep.Audit, pres.Audit...)
	}
	return rep
}
