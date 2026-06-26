// Package main is becky-resolve: the SELF-REGULATING identity resolver. The forensic agent calls
// it with a file (one dumb call) and gets the PROTOCOL-ENFORCED final naming — never a half-named
// maybe. becky-identify already corroborates voice+face and DEMOTES a single weak match to a
// "candidate"; becky-resolve adds the missing self-regulation: every candidate runs the forced
// Gemma-4 E4B→12B ladder and is named ONLY if the model corroborates it (a 2nd independent source).
// The tool→claim mapping lives in internal/forensic (one source); enforcement in internal/orchestrate
// (deterministic, unit-tested). Gemma-4 calls are local. JSON in / JSON out, degrade-never-crash.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"

	"becky-go/internal/forensic"
	"becky-go/internal/orchestrate"
)

// gemmaLadder is the local escalation Executor: level 1 = Gemma-4 E4B, level 2 = 12B. On a
// machine without the model it returns an error, so the claim stays a candidate (degrade, not
// crash) — never a false conclusion.
type gemmaLadder struct{ file string }

func (g gemmaLadder) Validate(c orchestrate.Claim, level int) (orchestrate.Signal, error) {
	model, variant := "gemma4-e4b", "e4b"
	if level >= 2 {
		model, variant = "gemma4-12b", "12b"
	}
	out, err := exec.Command("becky-validate", g.file, "--backend", "gemma4-local", "--variant", variant).Output()
	if err != nil {
		return orchestrate.Signal{}, fmt.Errorf("%s unavailable: %w", model, err)
	}
	var v struct {
		Observations []struct {
			Confidence float64 `json:"confidence"`
		} `json:"observations"`
	}
	conf := 0.0
	if json.Unmarshal(bytes.TrimSpace(out), &v) == nil {
		for _, o := range v.Observations {
			if o.Confidence > conf {
				conf = o.Confidence
			}
		}
	}
	if conf < 0.5 {
		return orchestrate.Signal{}, fmt.Errorf("%s did not corroborate", model)
	}
	return orchestrate.Signal{Source: model, Kind: orchestrate.KindPrint, Confidence: conf}, nil
}

type resultDoc struct {
	File      string                `json:"file"`
	Concluded []orchestrate.Verdict `json:"concluded"`
	Candidate []orchestrate.Verdict `json:"candidates"`
	Unknown   []orchestrate.Verdict `json:"unknown"`
	Audit     []string              `json:"audit"`
	Degraded  string                `json:"degraded,omitempty"`
}

// loadIdentify returns the raw becky-identify JSON: from --identify <json>, else by running
// becky-identify on the file (the local step), plus the file label and any degrade reason.
func loadIdentify(file, identifyJSON string) (raw []byte, fileLabel, degraded string) {
	if identifyJSON != "" {
		b, err := os.ReadFile(identifyJSON)
		if err != nil {
			return nil, file, "could not read --identify file: " + err.Error()
		}
		raw = b
	} else {
		b, err := exec.Command("becky-identify", file).Output()
		if err != nil {
			return nil, file, "becky-identify unavailable: " + err.Error()
		}
		raw = b
	}
	fileLabel = file
	if fileLabel == "" {
		var meta struct {
			File string `json:"file"`
		}
		_ = json.Unmarshal(bytes.TrimSpace(raw), &meta)
		fileLabel = meta.File
	}
	return raw, fileLabel, ""
}

func main() {
	file := flag.String("file", "", "the media file to resolve identities for")
	identifyJSON := flag.String("identify", "", "use this becky-identify JSON instead of running it")
	maxLevel := flag.Int("max-level", 2, "escalation ladder depth (1=E4B, 2=+12B)")
	flag.Parse()
	if *file == "" && *identifyJSON == "" {
		fmt.Fprintln(os.Stderr, "becky-resolve: need --file (or --identify <json>)")
		os.Exit(2)
	}

	raw, fileLabel, degraded := loadIdentify(*file, *identifyJSON)
	claims := forensic.IdentifyToClaims(raw)

	var ex orchestrate.Executor
	if *identifyJSON == "" && degraded == "" {
		ex = gemmaLadder{file: *file}
	}
	res := orchestrate.Resolve(claims, orchestrate.DefaultRules(), ex, *maxLevel)

	doc := resultDoc{File: fileLabel, Concluded: res.Concluded, Candidate: res.Candidates, Unknown: res.Unknown, Audit: res.Audit, Degraded: degraded}
	b, _ := json.MarshalIndent(doc, "", "  ")
	fmt.Println(string(b))
	fmt.Fprintf(os.Stderr, "becky-resolve: %d named, %d candidate(s) held, %d unknown\n",
		len(res.Concluded), len(res.Candidates), len(res.Unknown))
}
