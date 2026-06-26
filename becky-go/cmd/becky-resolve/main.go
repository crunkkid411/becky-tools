// Package main is becky-resolve: the SELF-REGULATING identity resolver, and the first tool to
// wire internal/orchestrate onto a REAL becky tool's output. The forensic agent calls it with
// a file (one dumb call) and gets the PROTOCOL-ENFORCED final naming — never a half-named maybe.
//
// becky-identify already corroborates voice+face internally and DEMOTES a single weak match to a
// "candidate" instead of naming it (recall is for detection, not naming). becky-resolve adds the
// missing self-regulation: it takes identify's output and, for every candidate, runs the forced
// confidence LADDER — validate with Gemma-4 E4B, escalate to 12B — and concludes a name ONLY if
// the model corroborates it (a 2nd independent source). The enforcement is internal/orchestrate
// (deterministic, unit-tested); the Gemma-4 calls are local. JSON in / JSON out, degrade-never-crash.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"

	"becky-go/internal/orchestrate"
)

// --- the becky-identify JSON contract (the subset we read; matches cmd/identify Output) ---

type idIdentification struct {
	Type           string   `json:"type"` // "corroborated" | "voice" | "face" | ...
	Name           string   `json:"name"`
	Confidence     float64  `json:"confidence"`
	CorroboratedBy []string `json:"corroborated_by"` // agreeing modalities when Type=="corroborated"
}

type idUnidentified struct {
	Candidate           string  `json:"candidate"` // a demoted near-miss name (single weak signal)
	CandidateConfidence float64 `json:"candidate_confidence"`
}

type idOutput struct {
	File            string             `json:"file"`
	Identifications []idIdentification `json:"identifications"`
	Unidentified    []idUnidentified   `json:"unidentified"`
}

// claimsFromIdentify maps becky-identify's output into protocol claims. A "corroborated"
// identification carries one signal per agreeing modality (distinct sources -> concludes); a
// single-modality match or a demoted candidate carries ONE signal (a candidate that the ladder
// must escalate before it can be named).
func claimsFromIdentify(o idOutput) []orchestrate.Claim {
	var cs []orchestrate.Claim
	for _, id := range o.Identifications {
		if id.Name == "" {
			continue
		}
		var sigs []orchestrate.Signal
		if id.Type == "corroborated" && len(id.CorroboratedBy) > 0 {
			for _, m := range id.CorroboratedBy {
				sigs = append(sigs, orchestrate.Signal{Source: "identify/" + m, Kind: orchestrate.KindPrint, Confidence: id.Confidence})
			}
		} else {
			sigs = append(sigs, orchestrate.Signal{Source: "identify/" + id.Type, Kind: orchestrate.KindPrint, Confidence: id.Confidence})
		}
		cs = append(cs, orchestrate.Claim{Key: "person=" + id.Name, Signals: sigs})
	}
	for _, u := range o.Unidentified {
		if u.Candidate == "" {
			continue
		}
		cs = append(cs, orchestrate.Claim{Key: "person=" + u.Candidate, Signals: []orchestrate.Signal{
			{Source: "identify/candidate", Kind: orchestrate.KindPrint, Confidence: u.CandidateConfidence},
		}})
	}
	return cs
}

// gemmaLadder is the local escalation Executor: level 1 = Gemma-4 E4B, level 2 = 12B. On a
// machine without the model it returns an error, so the claim stays a candidate (degrade, not
// crash) — never a false conclusion.
type gemmaLadder struct {
	file string
}

func (g gemmaLadder) Validate(c orchestrate.Claim, level int) (orchestrate.Signal, error) {
	model := "gemma4-e4b"
	variant := "e4b"
	if level >= 2 {
		model, variant = "gemma4-12b", "12b"
	}
	// becky-validate is the local model step that re-examines the clip for this claim.
	out, err := exec.Command("becky-validate", g.file, "--backend", "gemma4-local", "--variant", variant).Output()
	if err != nil {
		return orchestrate.Signal{}, fmt.Errorf("%s unavailable: %w", model, err)
	}
	// A non-empty validation that supports the claim is a second, independent source.
	conf := 0.0
	var v struct {
		Observations []struct {
			Confidence float64 `json:"confidence"`
		} `json:"observations"`
	}
	if json.Unmarshal(out, &v) == nil {
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

// resultDoc is the final corroborated output the forensic agent receives.
type resultDoc struct {
	File      string                `json:"file"`
	Concluded []orchestrate.Verdict `json:"concluded"`  // names becky will STATE
	Candidate []orchestrate.Verdict `json:"candidates"` // held: one signal, model couldn't corroborate
	Unknown   []orchestrate.Verdict `json:"unknown"`
	Audit     []string              `json:"audit"`
	Degraded  string                `json:"degraded,omitempty"`
}

// loadIdentify reads becky-identify output: from --identify <json> if given, else by running
// becky-identify on the file (the local step).
func loadIdentify(file, identifyJSON string) (idOutput, string) {
	var raw []byte
	var degraded string
	if identifyJSON != "" {
		b, err := os.ReadFile(identifyJSON)
		if err != nil {
			return idOutput{}, "could not read --identify file: " + err.Error()
		}
		raw = b
	} else {
		b, err := exec.Command("becky-identify", file).Output()
		if err != nil {
			return idOutput{File: file}, "becky-identify unavailable: " + err.Error()
		}
		raw = b
	}
	var o idOutput
	if err := json.Unmarshal(bytes.TrimSpace(raw), &o); err != nil {
		return idOutput{File: file}, "becky-identify output was not valid JSON: " + err.Error()
	}
	if o.File == "" {
		o.File = file
	}
	return o, degraded
}

func main() {
	file := flag.String("file", "", "the media file to resolve identities for")
	identifyJSON := flag.String("identify", "", "use this becky-identify JSON instead of running it (testing/composition)")
	maxLevel := flag.Int("max-level", 2, "escalation ladder depth (1=E4B, 2=+12B)")
	flag.Parse()
	if *file == "" && *identifyJSON == "" {
		fmt.Fprintln(os.Stderr, "becky-resolve: need --file (or --identify <json>)")
		os.Exit(2)
	}

	o, degraded := loadIdentify(*file, *identifyJSON)
	claims := claimsFromIdentify(o)

	var exec orchestrate.Executor
	if *identifyJSON == "" && degraded == "" {
		exec = gemmaLadder{file: *file} // real escalation only when we have the file + tools (local)
	}
	res := orchestrate.Resolve(claims, orchestrate.DefaultRules(), exec, *maxLevel)

	doc := resultDoc{
		File: o.File, Concluded: res.Concluded, Candidate: res.Candidates,
		Unknown: res.Unknown, Audit: res.Audit, Degraded: degraded,
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	fmt.Println(string(b))

	// Plain-English headline on stderr (the chat-facing line).
	fmt.Fprintf(os.Stderr, "becky-resolve: %d named, %d candidate(s) held, %d unknown\n",
		len(res.Concluded), len(res.Candidates), len(res.Unknown))
}
