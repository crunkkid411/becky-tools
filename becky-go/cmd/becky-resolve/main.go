// Package main is becky-resolve: the SELF-REGULATING identity resolver. The forensic agent calls
// it with a file (one dumb call) and gets the PROTOCOL-ENFORCED final naming — never a half-named
// maybe. becky-identify already corroborates voice+face and DEMOTES a single weak match to a
// "candidate"; becky-resolve adds the missing self-regulation: every candidate runs the forced
// Gemma-4 E4B->12B ladder and is named ONLY if the model corroborates it (a 2nd independent source).
//
// The model call + KB resolution come from internal/forensicrun (the ONE correct implementation):
// the ladder escalates via the BECKY_AVLM_VARIANT env (becky-validate has NO --variant flag — the
// earlier hand-rolled ladder here passed one and silently never escalated), and becky-identify is
// run WITH its required --kb (without which naming always degraded). JSON in / JSON out, degrade-never-crash.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"becky-go/internal/forensic"
	"becky-go/internal/forensicrun"
	"becky-go/internal/orchestrate"
)

type resultDoc struct {
	File      string                `json:"file"`
	Concluded []orchestrate.Verdict `json:"concluded"`
	Candidate []orchestrate.Verdict `json:"candidates"`
	Unknown   []orchestrate.Verdict `json:"unknown"`
	Audit     []string              `json:"audit"`
	Degraded  string                `json:"degraded,omitempty"`
}

// loadIdentify returns the raw becky-identify JSON: from --identify <json>, else by running
// becky-identify on the file WITH its REQUIRED --kb (the fix — becky-identify exits non-zero
// without a --kb, so the old kb-less call always degraded). Returns the file label + degrade reason.
func loadIdentify(file, identifyJSON, kb string) (raw []byte, fileLabel, degraded string) {
	if identifyJSON != "" {
		b, err := os.ReadFile(identifyJSON)
		if err != nil {
			return nil, file, "could not read --identify file: " + err.Error()
		}
		raw = b
	} else {
		// RunTool resolves becky-identify on PATH OR next to this exe (bin/), and runs it windowless.
		b, err := forensicrun.RunTool(context.Background(), "becky-identify", file, "--kb", kb)
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
	kbFlag := flag.String("kb", "", "knowledge-base dir for naming (default: BECKY_KB env, else kb-final)")
	maxLevel := flag.Int("max-level", 3, "escalation ladder depth (1=Gemma-4 E4B, 2=+Qwen3.5-4B, 3=+Gemma-4 12B)")
	flag.Parse()
	if *file == "" && *identifyJSON == "" {
		fmt.Fprintln(os.Stderr, "becky-resolve: need --file (or --identify <json>)")
		os.Exit(2)
	}

	kb := forensicrun.ResolveKB(*kbFlag)
	raw, fileLabel, degraded := loadIdentify(*file, *identifyJSON, kb)
	claims := forensic.IdentifyToClaims(raw)

	// The forced ladder runs only when we have a real file to re-watch (not the --identify JSON path,
	// and not after a degrade). NewValidateLadder is the single correct implementation: a CROSS-FAMILY
	// ladder (Gemma-4 E4B -> Qwen3.5-4B -> Gemma-4 12B; the 12B via the BECKY_AVLM_VARIANT env).
	var ex orchestrate.Executor
	if *identifyJSON == "" && degraded == "" && *file != "" {
		ex = forensicrun.NewValidateLadder(*file)
	}
	res := orchestrate.Resolve(claims, orchestrate.DefaultRules(), ex, *maxLevel)

	doc := resultDoc{File: fileLabel, Concluded: res.Concluded, Candidate: res.Candidates, Unknown: res.Unknown, Audit: res.Audit, Degraded: degraded}
	b, _ := json.MarshalIndent(doc, "", "  ")
	fmt.Println(string(b))
	fmt.Fprintf(os.Stderr, "becky-resolve: %d named, %d candidate(s) held, %d unknown\n",
		len(res.Concluded), len(res.Candidates), len(res.Unknown))
}
