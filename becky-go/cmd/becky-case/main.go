// Package main is becky-case: the ONE dumb call. The forensic agent runs `becky-case --file X`
// (optionally with --subject to locate someone on screen) and gets the FINAL corroborated
// forensic output — nothing else. becky decides the plan deterministically (diarize only when
// there's more than one speaker), RUNS the tools + the Gemma-4 validate ladder, and pushes every
// result through the protocol gate: a name is stated ONLY when corroborated, an on-screen interval
// ONLY where a model watched it. Maybes are held, never dumped. The agent sees no flags, no
// chaining, no protocol to remember — becky self-regulates.
//
// All of that lives in internal/forensicrun (the single shared runtime): `--file` actually runs the
// tools (the previous build only read tool JSON from --identify/--transcribe/... flags and so did
// NOTHING on a bare `--file`); the JSON flags remain for composition/testing.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"becky-go/internal/forensicrun"
	"becky-go/internal/orchestrate"
)

// caseTimeout bounds the whole one-dumb-call run (identify + transcribe/motion + the validate ladder).
const caseTimeout = 30 * time.Minute

func readFile(path string) []byte {
	if path == "" {
		return nil
	}
	b, _ := os.ReadFile(path)
	return b
}

// plan returns the deterministic diarize-conditional step plan (one source: forensicrun).
func plan(speakers int) []string { return forensicrun.Plan(speakers) }

// caseReport is the single corroborated output the forensic agent receives.
type caseReport struct {
	File     string                `json:"file"`
	Plan     []string              `json:"plan"`            // the deterministic steps becky ran (diarize-conditional)
	Names    []orchestrate.Verdict `json:"names"`           // stated only when corroborated
	OnScreen []orchestrate.Verdict `json:"on_screen"`       // stated only where a model watched it
	Held     []orchestrate.Verdict `json:"held_candidates"` // one-signal maybes, NOT stated
	Audit    []string              `json:"audit"`
	Degraded []string              `json:"degraded,omitempty"` // tools/models that were absent (honest partial)
}

func fromForensic(fr forensicrun.ForensicReport) caseReport {
	return caseReport{File: fr.File, Plan: fr.Plan, Names: fr.Names, OnScreen: fr.OnScreen,
		Held: fr.Held, Audit: fr.Audit, Degraded: fr.Degraded}
}

// report is the PURE composition core: given already-gathered tool JSON, enforce the protocol (no
// I/O, no models). Used by the JSON-flag path and the unit tests.
func report(file, subject string, speakers int, identify, transcribe, motion, validate []byte) caseReport {
	return fromForensic(forensicrun.Report(file, subject, speakers,
		forensicrun.Inputs{Identify: identify, Transcribe: transcribe, Motion: motion, Validate: validate}, nil, 0))
}

// runCase is the IMPURE one dumb call: actually run the tools + the model ladder over the file.
func runCase(file, subject string, speakers int) caseReport {
	ctx, cancel := context.WithTimeout(context.Background(), caseTimeout)
	defer cancel()
	return fromForensic(forensicrun.RunAndReport(ctx, file, subject, "", speakers, nil))
}

func main() {
	file := flag.String("file", "", "the media file")
	subject := flag.String("subject", "", "optional: who/what to locate on screen")
	speakers := flag.Int("speakers", 0, "known speaker count (0 = unknown; >1 triggers diarization)")
	// tool outputs (provided for composition/testing; a bare --file runs the tools itself)
	idJSON := flag.String("identify", "", "becky-identify JSON")
	trJSON := flag.String("transcribe", "", "becky-transcribe JSON")
	moJSON := flag.String("motion", "", "becky-motion JSON")
	vaJSON := flag.String("validate", "", "becky-validate JSON")
	flag.Parse()
	if *file == "" && *idJSON == "" && *trJSON == "" {
		fmt.Fprintln(os.Stderr, "becky-case: need --file (or provide tool JSON)")
		os.Exit(2)
	}

	// A bare --file (no tool JSON) is the one dumb call: run everything. If any tool JSON is
	// supplied, use the composition path over exactly what was given.
	hasJSON := *idJSON != "" || *trJSON != "" || *moJSON != "" || *vaJSON != ""
	var rep caseReport
	if *file != "" && !hasJSON {
		rep = runCase(*file, *subject, *speakers)
	} else {
		rep = report(*file, *subject, *speakers, readFile(*idJSON), readFile(*trJSON), readFile(*moJSON), readFile(*vaJSON))
	}

	b, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(b))
	fmt.Fprintf(os.Stderr, "becky-case: %d name(s), %d on-screen interval(s), %d held\n",
		len(rep.Names), len(rep.OnScreen), len(rep.Held))
}
