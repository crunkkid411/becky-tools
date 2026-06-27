// Package main is becky-presence: the SELF-REGULATING "is subject X on screen, and WHEN?" tool —
// the SKILL.md corroboration chain COMPILED. One call (subject + file): becky gathers the cheap
// signals (transcript mention, motion burst) and the vision-model WATCH (becky-validate), groups
// them into time windows, and returns ONLY the tight intervals a model actually watched and >=2
// sources agree on. A mention or a motion burst NEVER becomes a stated on-screen interval. The
// tool->signal mapping lives in internal/forensic (one source); the rule in internal/orchestrate.
//
// The cheap signals are now actually GATHERED from the file (becky-transcribe + becky-motion via
// internal/forensicrun) when --file is given — the previous build only read them from --transcribe/
// --motion JSON flags despite documenting "else run it". The WATCH ladder is internal/forensicrun's
// single correct implementation: it escalates Gemma-4 E4B->12B via the BECKY_AVLM_VARIANT env (the
// old hand-rolled ladder passed a --variant flag becky-validate does not have, so it never escalated)
// and a presence watch is subject-aware. Model calls are local; degrade-never-crash.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"becky-go/internal/forensic"
	"becky-go/internal/forensicrun"
	"becky-go/internal/orchestrate"
)

// gatherTimeout bounds the optional sibling-tool runs (transcribe/motion + the watch ladder).
const gatherTimeout = 30 * time.Minute

func readBytes(path string) []byte {
	if path == "" {
		return nil
	}
	b, _ := os.ReadFile(path)
	return b
}

// gather returns a sibling tool's JSON: from an explicit --<tool> path if given, else by running
// the tool on the file (when --file is set), else nil. Degrade-never-crash: a tool error -> nil
// (that signal is simply absent, never a crash).
func gather(ctx context.Context, tool, explicitPath, file string) []byte {
	if explicitPath != "" {
		return readBytes(explicitPath)
	}
	if file == "" {
		return nil
	}
	b, err := forensicrun.RunTool(ctx, tool, file)
	if err != nil {
		return nil
	}
	return b
}

type resultDoc struct {
	Subject   string                `json:"subject"`
	OnScreen  []orchestrate.Verdict `json:"on_screen"`         // tight intervals becky will STATE
	Candidate []orchestrate.Verdict `json:"candidate_moments"` // go-look windows, never stated as presence
	Audit     []string              `json:"audit"`
}

func main() {
	subject := flag.String("subject", "", "who/what to locate on screen, e.g. \"cat\" or \"Shelby\"")
	file := flag.String("file", "", "the media file (gather the signals + watch locally)")
	trPath := flag.String("transcribe", "", "becky-transcribe JSON (else run it on --file)")
	moPath := flag.String("motion", "", "becky-motion JSON (else run it on --file)")
	vaPath := flag.String("validate", "", "becky-validate JSON (optional; else the model watches via --file)")
	gap := flag.Float64("merge-gap", 2.0, "seconds: signals within this gap are one window")
	flag.Parse()
	if *subject == "" {
		fmt.Fprintln(os.Stderr, "becky-presence: --subject is required")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), gatherTimeout)
	defer cancel()

	tr := gather(ctx, "becky-transcribe", *trPath, *file)
	mo := gather(ctx, "becky-motion", *moPath, *file)
	va := readBytes(*vaPath)

	sigs := forensic.PresenceSignals(*subject, tr, mo, va)
	claims := orchestrate.CorrelatePresence(*subject, sigs, *gap)

	// The forced WATCH ladder runs only with a real file. A presence window concludes ONLY where a
	// model actually watched the subject (forensicrun's subject-aware CROSS-FAMILY ladder:
	// Gemma-4 E4B -> Qwen3.5-4B -> Gemma-4 12B, depth 3).
	var ex orchestrate.Executor
	if *file != "" {
		ex = forensicrun.NewValidateLadder(*file)
	}
	res := orchestrate.Resolve(claims, orchestrate.DefaultRules(), ex, 3)

	doc := resultDoc{Subject: *subject, OnScreen: res.Concluded, Candidate: append(res.Candidates, res.Unknown...), Audit: res.Audit}
	b, _ := json.MarshalIndent(doc, "", "  ")
	fmt.Println(string(b))
	fmt.Fprintf(os.Stderr, "becky-presence: %q on screen in %d interval(s); %d candidate moment(s) to review\n",
		*subject, len(res.Concluded), len(res.Candidates)+len(res.Unknown))
}
