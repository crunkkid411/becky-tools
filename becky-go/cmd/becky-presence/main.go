// Package main is becky-presence: the SELF-REGULATING "is subject X on screen, and WHEN?" tool.
// It is the cross-tool corroboration chain from SKILL.md, COMPILED — the chain the forensic agent
// kept doing wrong by hand. The agent makes one call (subject + file); becky gathers the cheap
// signals (transcript mention, motion burst) and the vision-model WATCH (becky-validate), groups
// them into time windows, and returns ONLY the tight intervals a model actually watched and >=2
// sources agree on. A mention or a motion burst NEVER becomes a stated on-screen interval — that
// rule is enforced in internal/orchestrate (deterministic, unit-tested). Model calls are local.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"becky-go/internal/orchestrate"
)

// --- upstream JSON shapes (subsets, matching the real tool contracts) ---

type transcribeDoc struct {
	Segments []struct {
		Start float64 `json:"start"`
		End   float64 `json:"end"`
		Text  string  `json:"text"`
	} `json:"segments"`
}

type motionDoc struct {
	Bursts []struct {
		WindowStart float64 `json:"window_start"`
		WindowEnd   float64 `json:"window_end"`
	} `json:"motion_bursts"`
}

type validateDoc struct {
	Observations []struct {
		SegmentStart float64 `json:"segment_start"`
		SegmentEnd   float64 `json:"segment_end"`
		Visual       string  `json:"visual"`
		Finding      string  `json:"finding"`
		Content      string  `json:"content"`
		Confidence   float64 `json:"confidence"`
	} `json:"observations"`
}

func mentions(s string) bool { return strings.TrimSpace(s) != "" }

// signalsFor builds the TimedSignals for a subject from the three tools' outputs. Subject match
// is a deterministic case-insensitive substring (no model): a transcript mention OR a validate
// observation whose text names the subject. Motion bursts are subject-agnostic candidate moments.
func signalsFor(subject string, tr transcribeDoc, mo motionDoc, va validateDoc) []orchestrate.TimedSignal {
	subj := strings.ToLower(strings.TrimSpace(subject))
	var sigs []orchestrate.TimedSignal

	for _, s := range tr.Segments {
		if subj != "" && strings.Contains(strings.ToLower(s.Text), subj) {
			sigs = append(sigs, orchestrate.TimedSignal{
				Source: "becky-transcribe", Kind: orchestrate.KindMention, Confidence: 0.9, Start: s.Start, End: s.End,
			})
		}
	}
	for _, b := range mo.Bursts {
		sigs = append(sigs, orchestrate.TimedSignal{
			Source: "becky-motion", Kind: orchestrate.KindMotion, Confidence: 0.7, Start: b.WindowStart, End: b.WindowEnd,
		})
	}
	for _, o := range va.Observations {
		text := strings.ToLower(o.Visual + " " + o.Finding + " " + o.Content)
		// A validate observation is a WATCH only when the model actually reports the subject.
		if subj != "" && strings.Contains(text, subj) && mentions(text) {
			conf := o.Confidence
			if conf <= 0 {
				conf = 0.6
			}
			sigs = append(sigs, orchestrate.TimedSignal{
				Source: "becky-validate", Kind: orchestrate.KindWatched, Confidence: conf, Start: o.SegmentStart, End: o.SegmentEnd,
			})
		}
	}
	return sigs
}

func readJSON(path string, v any) bool {
	if path == "" {
		return false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Unmarshal(bytes.TrimSpace(b), v) == nil
}

// validateLadder escalates an unconcluded presence window by having Gemma-4 WATCH that exact
// window (becky-validate --motion-window). Local; degrades to "not corroborated" off-machine.
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
	var va validateDoc
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

	var tr transcribeDoc
	var mo motionDoc
	var va validateDoc
	readJSON(*trPath, &tr)
	readJSON(*moPath, &mo)
	readJSON(*vaPath, &va)

	sigs := signalsFor(*subject, tr, mo, va)
	claims := orchestrate.CorrelatePresence(*subject, sigs, *gap)

	var ex orchestrate.Executor
	if *file != "" {
		ex = validateLadder{file: *file, subject: *subject} // real escalation only with the file (local)
	}
	res := orchestrate.Resolve(claims, orchestrate.DefaultRules(), ex, 2)

	doc := resultDoc{Subject: *subject, OnScreen: res.Concluded, Candidate: append(res.Candidates, res.Unknown...), Audit: res.Audit}
	b, _ := json.MarshalIndent(doc, "", "  ")
	fmt.Println(string(b))
	fmt.Fprintf(os.Stderr, "becky-presence: %q on screen in %d interval(s); %d candidate moment(s) to review\n",
		*subject, len(res.Concluded), len(res.Candidates)+len(res.Unknown))
}
