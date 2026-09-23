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
	"path/filepath"
	"slices"
	"strconv"
	"strings"
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
	// What was said, line by line, each line labelled with its speaker. Speakers is how many
	// voices were heard; SavedTo is the full transcript file becky-transcribe left by the video.
	Speakers   int        `json:"speakers,omitempty"`
	Transcript []caseLine `json:"transcript,omitempty"`
	SavedTo    string     `json:"saved_to,omitempty"`
}

// caseLine is one line of speech in the case report.
type caseLine struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker string  `json:"speaker,omitempty"`
	Text    string  `json:"text"`
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

// runTranscribe is the seam to becky-transcribe (swapped in tests).
var runTranscribe = forensicrun.RunTool

// runCase is the IMPURE one dumb call: actually run the tools + the model ladder over the file.
// It ALWAYS transcribes (what was said is half the answer) and labels speakers unless the caller
// said there is exactly one — the old build listed "becky-transcribe, becky-diarize" in its plan
// but ran neither, returning an empty report in 0 seconds.
func runCase(file, subject string, speakers int) caseReport {
	ctx, cancel := context.WithTimeout(context.Background(), caseTimeout)
	defer cancel()
	var degraded []string
	trJSON, err := runTranscribe(ctx, "becky-transcribe", transcribeArgs(file, speakers)...)
	if err != nil {
		degraded = append(degraded, "becky-transcribe: "+err.Error())
		trJSON = nil
	}
	rep := fromForensic(forensicrun.RunAndReport(ctx, file, subject, "", speakers, trJSON))
	rep.Degraded = append(degraded, rep.Degraded...)
	if speakers != 1 && !slices.Contains(rep.Plan, "becky-diarize") {
		rep.Plan = append([]string{"becky-transcribe", "becky-diarize"}, dropStep(rep.Plan, "becky-transcribe")...)
	}
	attachTranscript(&rep, trJSON)
	return rep
}

// transcribeArgs: always transcribe; label speakers unless the caller said there is exactly one.
func transcribeArgs(file string, speakers int) []string {
	args := []string{file}
	if speakers != 1 {
		args = append(args, "--diarize")
	}
	if speakers > 0 {
		args = append(args, "--speakers", strconv.Itoa(speakers))
	}
	return args
}

func dropStep(steps []string, name string) []string {
	var out []string
	for _, s := range steps {
		if s != name {
			out = append(out, s)
		}
	}
	return out
}

// attachTranscript copies the speaker-labelled lines from becky-transcribe JSON into the report.
func attachTranscript(rep *caseReport, trJSON []byte) {
	var tr struct {
		Speakers    int        `json:"speakers"`
		SpeakerNote string     `json:"speaker_note"`
		Segments    []caseLine `json:"segments"`
	}
	if len(trJSON) == 0 || json.Unmarshal(trJSON, &tr) != nil {
		return
	}
	rep.Speakers, rep.Transcript = tr.Speakers, tr.Segments
	if tr.SpeakerNote != "" {
		rep.Degraded = append(rep.Degraded, "speakers: "+tr.SpeakerNote)
	}
	if rep.File != "" && len(tr.Segments) > 0 {
		side := strings.TrimSuffix(rep.File, filepath.Ext(rep.File)) + ".transcript.json"
		if _, err := os.Stat(side); err == nil {
			rep.SavedTo = side
		}
	}
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
	fmt.Fprintf(os.Stderr, "becky-case: %d line(s) of speech from %d speaker(s), %d name(s), %d on-screen interval(s), %d held\n",
		len(rep.Transcript), rep.Speakers, len(rep.Names), len(rep.OnScreen), len(rep.Held))
	if rep.SavedTo != "" {
		fmt.Fprintf(os.Stderr, "saved: %s\n", rep.SavedTo)
	}
}
