// becky-wire — say a studio-setup move in plain English; becky wires it.
//
//	becky-wire --project project.json --instruction "sidechain the bass to the kick" \
//	           [--mix mix.json] [--output out.json] [--dry-run]
//
// It reads a becky-compose project.json, parses the plain-English instruction
// into a structured Intent (a deterministic keyword/grammar parser today; a fast
// background instruct model when one is installed — see internal/studio), applies
// the Intent as ROUTING/MIX edits to the project graph (immutably: the source is
// never mutated), and writes the patched project.json back out.
//
// The killer moves it handles — each 40 clicks in a normal DAW, pure data here:
//   - "sidechain the bass to the kick"    -> kick -> bus.808 sidechain edge
//   - "sidechain the 808 to the kick"
//   - "route the lead guitar to the guitar bus"
//   - "put my usual chain on the drum bus" / "set up the drum bus"
//   - "use Odin II on the lead"
//   - "duck the synths under the vocal"
//   - "gain stage the kick to -7"
//
// House rules (CLAUDE.md): file in -> JSON out -> exit code; pure Go + offline;
// deterministic (sorted edges, fixed ordering); degrade-never-crash (an
// unintelligible instruction prints a friendly "couldn't understand" line and
// exits 0, never a panic). --dry-run is the "show me, don't do it" preview: it
// prints the proposed change + the Intent JSON WITHOUT writing.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/habits"
	"becky-go/internal/music"
	"becky-go/internal/pathx"
	"becky-go/internal/studio"
)

func main() {
	projPath := flag.String("project", "", "path to the becky-compose project.json (required)")
	instruction := flag.String("instruction", "", "plain-English studio move, e.g. \"sidechain the bass to the kick\" (required)")
	mixPath := flag.String("mix", "", "optional mix.json (reserved; routing edits go on the project today)")
	output := flag.String("output", "", "where to write the patched project (default: alongside --project)")
	dryRun := flag.Bool("dry-run", false, "show the proposed change + Intent JSON WITHOUT writing")
	flag.Parse()

	if strings.TrimSpace(*projPath) == "" || strings.TrimSpace(*instruction) == "" {
		fmt.Fprintln(os.Stderr, "usage: becky-wire --project project.json --instruction \"sidechain the bass to the kick\" [--mix mix.json] [--output out.json] [--dry-run]")
		os.Exit(2)
	}

	os.Exit(run(*projPath, *instruction, *mixPath, *output, *dryRun))
}

// run is the testable entry point; it returns the process exit code.
func run(projPath, instruction, mixPath, output string, dryRun bool) int {
	raw, err := os.ReadFile(projPath)
	if err != nil {
		// degrade-never-crash: a missing/unreadable project can't be edited, but
		// say so plainly and exit non-fatally for a bad path (usage-ish) = 1.
		fmt.Fprintf(os.Stderr, "could not read project %s: %v\n", projPath, err)
		return 1
	}
	var proj music.Project
	if err := json.Unmarshal(raw, &proj); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %s is not valid project JSON (%v) — proceeding with an empty graph\n", pathx.Base(projPath), err)
		// proj stays zero-valued; resolution will likely degrade to Unknown.
	}

	// Pick the live parser (model when installed, else deterministic), wrapped so
	// any per-call model failure falls back to the deterministic grammar.
	parser := studio.FallbackParser{Primary: studio.PickParser(), Secondary: studio.DeterministicParser{}}
	intent, _ := parser.Parse(instruction, proj)

	if intent.Action == studio.ActionUnknown {
		// Friendly, never a crash. Show the Intent so a caller/agent can inspect.
		fmt.Println("🤔 couldn't understand that one.")
		if intent.Note != "" {
			fmt.Println("   " + intent.Note)
		}
		printIntentJSON(intent)
		return 0
	}

	patched, summary := studio.Apply(proj, intent)

	if dryRun {
		fmt.Println("👀 proposed change (dry run — nothing written):")
		fmt.Println("   " + summary)
		printIntentJSON(intent)
		return 0
	}

	dest := output
	if dest == "" {
		dest = defaultOutput(projPath)
	}
	out, err := marshalProject(patched)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: could not serialise patched project:", err)
		return 1
	}
	if err := os.WriteFile(dest, out, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: could not write %s: %v\n", dest, err)
		return 1
	}

	// Best-effort: log this as a preference so becky learns Jordan's habitual
	// setups. Failures are silent (degrade-never-crash) — the edit already landed.
	logPreference(dest, intent)

	fmt.Printf("✓ %s — wrote %s\n", summary, pathx.Base(dest))
	return 0
}

// printIntentJSON prints the structured Intent for inspection (the "show me"
// half of the preview philosophy).
func printIntentJSON(in studio.Intent) {
	b, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return
	}
	fmt.Println(string(b))
}

// marshalProject renders the patched project as deterministic, newline-terminated
// JSON (matches the becky-compose/mix emission style).
func marshalProject(p music.Project) ([]byte, error) {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// defaultOutput sits the patched project next to the source. pathx.Dir is
// separator-agnostic so a Windows path resolves correctly even on Linux CI.
func defaultOutput(projPath string) string {
	dir := pathx.Dir(projPath)
	base := pathx.Base(projPath)
	name := strings.TrimSuffix(base, ".json") + ".wired.json"
	if dir == "" {
		return name
	}
	return filepath.Join(dir, name)
}

// logPreference appends a corrections-log line so becky-habits can learn the
// producer's habitual setups. The "auto" value is empty (becky proposed nothing
// itself — the producer asked directly), "fixed" is what he wired. Best-effort.
func logPreference(dest string, in studio.Intent) {
	dir := pathx.Dir(dest)
	logPath := "wire.corrections.jsonl"
	if dir != "" {
		logPath = filepath.Join(dir, logPath)
	}
	scope, field, fixed := preferenceFields(in)
	if scope == "" {
		return
	}
	_ = habits.AppendCorrectionLog(logPath, "wire", scope, field, "", fixed)
}

// preferenceFields maps an Intent to a (scope, field, fixed) habit triple.
func preferenceFields(in studio.Intent) (scope, field, fixed string) {
	switch in.Action {
	case studio.ActionSidechain:
		return in.TargetWord, "sidechain.from", in.SourceWord
	case studio.ActionRoute:
		return in.SourceWord, "route.bus", in.Target
	case studio.ActionInsertChain:
		return in.TargetWord, "chain", "standard"
	case studio.ActionSetVST:
		return in.TargetWord, "vst", in.VST
	case studio.ActionSetGain:
		return in.TargetWord, "gain_db", formatGain(in.GainDB)
	default:
		return "", "", ""
	}
}

// formatGain renders a dB value without a trailing ".0" for whole numbers.
func formatGain(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%g", v)
}
