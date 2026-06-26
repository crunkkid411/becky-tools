// Package main is becky-presence: the SELF-REGULATING "is subject X on screen, and WHEN?" tool —
// the SKILL.md corroboration chain COMPILED. One call (subject + file): becky gathers the cheap
// signals (transcript mention, motion burst) and the vision-model WATCH (becky-validate), groups
// them into time windows, and returns ONLY the tight intervals a model actually watched and >=2
// sources agree on. A mention or a motion burst NEVER becomes a stated on-screen interval. The
// tool→signal mapping lives in internal/forensic (one source); the rule in internal/orchestrate
// (deterministic, unit-tested). Model calls are local.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"becky-go/internal/forensic"
	"becky-go/internal/orchestrate"
)

func readBytes(path string) []byte {
	if path == "" {
		return nil
	}
	b, _ := os.ReadFile(path)
	return b
}

// validateLadder escalates an unconcluded presence window by having Gemma-4 WATCH the clip
// (becky-validate). Local; degrades to "not corroborated" off-machine — never a false conclusion.
type validateLadder struct{ file, subject string }

func (l validateLadder) Validate(c orchestrate.Claim, level int) (orchestrate.Signal, error) {
	variant := "e4b"
	if level >= 2 {
		variant = "12b"
	}
	out, err := exec.Command("becky-validate", l.file, "--backend", "gemma4-local", "--variant", variant).Output()
	if err != nil {
		return orchestrate.Signal{}, fmt.Errorf("gemma4-%s unavailable: %w", variant, err)
	}
	var va struct {
		Observations []struct {
			Visual     string  `json:"visual"`
			Finding    string  `json:"finding"`
			Confidence float64 `json:"confidence"`
		} `json:"observations"`
	}
	_ = json.Unmarshal(bytes.TrimSpace(out), &va)
	subj := strings.ToLower(l.subject)
	best := 0.0
	for _, o := range va.Observations {
		if strings.Contains(strings.ToLower(o.Visual+" "+o.Finding), subj) && o.Confidence > best {
			best = o.Confidence
		}
	}
	if best < 0.5 {
		return orchestrate.Signal{}, fmt.Errorf("gemma4-%s did not see %q", variant, l.subject)
	}
	return orchestrate.Signal{Source: "gemma4-" + variant, Kind: orchestrate.KindWatched, Confidence: best}, nil
}

type resultDoc struct {
	Subject   string                `json:"subject"`
	OnScreen  []orchestrate.Verdict `json:"on_screen"`         // tight intervals becky will STATE
	Candidate []orchestrate.Verdict `json:"candidate_moments"` // go-look windows, never stated as presence
	Audit     []string              `json:"audit"`
}

func main() {
	subject := flag.String("subject", "", "who/what to locate on screen, e.g. \"cat\" or \"Shelby\"")
	file := flag.String("file", "", "the media file (run the tools locally)")
	trPath := flag.String("transcribe", "", "becky-transcribe JSON (else run it)")
	moPath := flag.String("motion", "", "becky-motion JSON (else run it)")
	vaPath := flag.String("validate", "", "becky-validate JSON (else run it)")
	gap := flag.Float64("merge-gap", 2.0, "seconds: signals within this gap are one window")
	flag.Parse()
	if *subject == "" {
		fmt.Fprintln(os.Stderr, "becky-presence: --subject is required")
		os.Exit(2)
	}

	sigs := forensic.PresenceSignals(*subject, readBytes(*trPath), readBytes(*moPath), readBytes(*vaPath))
	claims := orchestrate.CorrelatePresence(*subject, sigs, *gap)

	var ex orchestrate.Executor
	if *file != "" {
		ex = validateLadder{file: *file, subject: *subject}
	}
	res := orchestrate.Resolve(claims, orchestrate.DefaultRules(), ex, 2)

	doc := resultDoc{Subject: *subject, OnScreen: res.Concluded, Candidate: append(res.Candidates, res.Unknown...), Audit: res.Audit}
	b, _ := json.MarshalIndent(doc, "", "  ")
	fmt.Println(string(b))
	fmt.Fprintf(os.Stderr, "becky-presence: %q on screen in %d interval(s); %d candidate moment(s) to review\n",
		*subject, len(res.Concluded), len(res.Candidates)+len(res.Unknown))
}
