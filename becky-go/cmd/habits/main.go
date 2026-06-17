// becky-habits — the preference-learning loop's brain. becky's tools already log
// every time an AUTO value is overridden by Jordan's MANUAL fix; this tool turns a
// REPEATED correction into a learned DEFAULT ("you always pull the kick to -7 →
// that becomes the default"). It is a Go port of dawbase's MIT HabitStore.
//
//	becky-habits observe <corrections.json> [--store <path>] [--json]
//	becky-habits learn --logs <dir>           [--store <path>] [--json]
//	becky-habits show [--store <path>] [--json]
//
// `observe` ingests one corrections file and updates the store. `learn` walks a
// directory of JSONL corrections logs (written by hum/vox/daw/canvas) and ingests
// all of them in one pass. `show` reports, in plain language, what becky has
// LEARNED (corroborated >= the threshold) and what is still a CANDIDATE (seen
// once). Conservative by design: a one-off fix never becomes a default —
// corroborate, then conclude (CLAUDE.md §2).
//
// Deterministic + offline + degrade-never-crash. The store is a tiny, human-
// readable habits.json (default under the per-user config dir; override with
// --store). Exit codes: 0 ok, 1 runtime error (bad/missing file, write failure),
// 2 usage error.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"becky-go/internal/habits"
)

const (
	exitOK    = 0
	exitErr   = 1
	exitUsage = 2
)

func main() { os.Exit(run(os.Args[1:])) }

// run is the testable entry point: it returns the process exit code instead of
// calling os.Exit, so the dispatch can be unit-tested without spawning a binary.
func run(args []string) int {
	if len(args) < 1 {
		usage()
		return exitUsage
	}
	switch args[0] {
	case "observe":
		return runObserve(args[1:])
	case "learn":
		return runLearn(args[1:])
	case "show":
		return runShow(args[1:])
	case "usual":
		return runUsual(args[1:])
	case "-h", "--help", "help":
		usage()
		return exitOK
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		usage()
		return exitUsage
	}
}

// runObserve ingests one corrections file into the store and persists it.
func runObserve(args []string) int {
	positional, flags := splitArgs(args)
	fs := newFlags("observe")
	storePath := fs.String("store", habits.DefaultStorePath(), "path to habits.json")
	asJSON := fs.Bool("json", false, "emit a JSON summary instead of plain text")
	if err := fs.Parse(flags); err != nil {
		return exitUsage
	}
	positional = append(positional, fs.Args()...)
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, "observe needs exactly one <corrections.json> argument")
		return exitUsage
	}

	body, err := os.ReadFile(positional[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "can't read corrections:", err)
		return exitErr
	}
	records, err := habits.ParseRecords(body)
	if err != nil {
		fmt.Fprintln(os.Stderr, "corrections file is not valid:", err)
		return exitErr
	}
	store, err := habits.Load(*storePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "can't load the habit store:", err)
		return exitErr
	}

	learnable := store.ObserveAll(records)
	if err := store.Save(*storePath); err != nil {
		fmt.Fprintln(os.Stderr, "can't save the habit store:", err)
		return exitErr
	}
	return emitObserve(store, *storePath, len(records), learnable, *asJSON)
}

// emitObserve prints the post-ingest summary (plain or JSON).
func emitObserve(store *habits.Store, path string, total, learnable int, asJSON bool) int {
	if asJSON {
		rep := store.BuildReport()
		rep.Note = fmt.Sprintf("ingested %d record(s), %d learnable, into %s", total, learnable, path)
		return encode(rep)
	}
	skipped := total - learnable
	fmt.Printf("ingested %d correction(s) into %s\n", total, path)
	if skipped > 0 {
		fmt.Printf("  (%d skipped — missing scope/field/fixed, nothing to learn)\n", skipped)
	}
	fmt.Println()
	fmt.Print(store.Describe())
	return exitOK
}

// runLearn walks a directory of JSONL corrections logs (written by the tools
// hum/vox/daw/canvas) and ingests all of them into the habit store in one pass.
// It is the multi-file counterpart to runObserve (which takes a single file).
//
// Usage: becky-habits learn --logs <dir> [--store <path>] [--json]
func runLearn(args []string) int {
	fs := newFlags("learn")
	logsDir := fs.String("logs", "", "directory containing *.jsonl corrections logs")
	storePath := fs.String("store", habits.DefaultStorePath(), "path to habits.json")
	asJSON := fs.Bool("json", false, "emit a JSON summary instead of plain text")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *logsDir == "" {
		fmt.Fprintln(os.Stderr, "learn requires --logs <dir>")
		return exitUsage
	}

	records, err := habits.LoadCorrectionLogs(*logsDir)
	if err != nil {
		// degrade: partial load is still useful; report but continue
		fmt.Fprintf(os.Stderr, "warning: some correction logs could not be read: %v\n", err)
	}

	store, err := habits.Load(*storePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "can't load the habit store:", err)
		return exitErr
	}

	learnable := store.ObserveAll(records)
	if err := store.Save(*storePath); err != nil {
		fmt.Fprintln(os.Stderr, "can't save the habit store:", err)
		return exitErr
	}
	return emitLearn(store, *storePath, *logsDir, len(records), learnable, *asJSON)
}

// emitLearn prints the post-learn summary (plain or JSON).
func emitLearn(store *habits.Store, storePath, logsDir string, total, learnable int, asJSON bool) int {
	if asJSON {
		rep := store.BuildReport()
		rep.Note = fmt.Sprintf("loaded %d record(s) from %s, %d learnable, into %s",
			total, logsDir, learnable, storePath)
		return encode(rep)
	}
	skipped := total - learnable
	fmt.Printf("loaded %d correction(s) from %s into %s\n", total, logsDir, storePath)
	if skipped > 0 {
		fmt.Printf("  (%d skipped — missing scope/field/fixed, nothing to learn)\n", skipped)
	}
	fmt.Println()
	fmt.Print(store.Describe())
	return exitOK
}

// runShow reports what becky has learned without ingesting anything.
func runShow(args []string) int {
	positional, flags := splitArgs(args)
	if len(positional) != 0 {
		fmt.Fprintln(os.Stderr, "show takes no file argument; did you mean `observe`?")
		return exitUsage
	}
	fs := newFlags("show")
	storePath := fs.String("store", habits.DefaultStorePath(), "path to habits.json")
	asJSON := fs.Bool("json", false, "emit a JSON report instead of plain text")
	if err := fs.Parse(flags); err != nil {
		return exitUsage
	}
	store, err := habits.Load(*storePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "can't load the habit store:", err)
		return exitErr
	}
	if *asJSON {
		return encode(store.BuildReport())
	}
	fmt.Print(store.Describe())
	return exitOK
}

// runUsual answers "what's my usual <scope>?" — it prints the LEARNED structured
// preferences (FX chains, sidechain routes, …) recorded under a scope so another
// tool (or Jordan) can recall "set up my usual drum bus". With no scope it lists
// every learned structured setup across all scopes.
//
// Usage: becky-habits usual [<scope>] [--store <path>] [--json]
func runUsual(args []string) int {
	positional, flags := splitArgs(args)
	fs := newFlags("usual")
	storePath := fs.String("store", habits.DefaultStorePath(), "path to habits.json")
	asJSON := fs.Bool("json", false, "emit a JSON list instead of plain text")
	if err := fs.Parse(flags); err != nil {
		return exitUsage
	}
	positional = append(positional, fs.Args()...)
	if len(positional) > 1 {
		fmt.Fprintln(os.Stderr, "usual takes at most one <scope> argument")
		return exitUsage
	}

	store, err := habits.Load(*storePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "can't load the habit store:", err)
		return exitErr
	}

	var prefs []habits.UsualPreference
	if len(positional) == 1 {
		prefs = store.Usual(positional[0])
	} else {
		// No scope: collect the learned structured habits across every scope.
		for _, h := range store.StructuredLearned() {
			prefs = append(prefs, habits.UsualPreference{
				Scope: h.Scope, Field: h.Field, Value: h.Default,
				Evidence: h.Evidence, Sources: h.Sources,
			})
		}
	}

	if *asJSON {
		return encode(prefs)
	}
	return emitUsual(prefs, positional)
}

// emitUsual prints the plain-language "your usual X" view.
func emitUsual(prefs []habits.UsualPreference, positional []string) int {
	if len(prefs) == 0 {
		if len(positional) == 1 {
			fmt.Printf("becky hasn't learned a usual setup for %q yet — keep correcting and a repeated structured fix becomes your default.\n", positional[0])
		} else {
			fmt.Println("becky hasn't learned any usual structured setups yet — keep correcting and a repeated structured fix becomes your default.")
		}
		return exitOK
	}
	if len(positional) == 1 {
		fmt.Printf("your usual %s:\n", positional[0])
	} else {
		fmt.Println("your usual setups:")
	}
	for _, p := range prefs {
		line := fmt.Sprintf("  - %s %s → %s (seen %dx)", p.Scope, p.Field, p.Value, p.Evidence)
		if len(p.Sources) > 0 {
			line += " from " + fmt.Sprint(p.Sources)
		}
		fmt.Println(line)
	}
	return exitOK
}

// encode writes v as indented JSON to stdout, degrading to an error exit on a
// marshal failure rather than panicking.
func encode(v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, "encode:", err)
		return exitErr
	}
	return exitOK
}

func usage() {
	fmt.Fprintln(os.Stderr, "becky-habits — learn Jordan's repeated corrections into defaults")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  becky-habits observe <corrections.json> [--store <path>] [--json]")
	fmt.Fprintln(os.Stderr, "  becky-habits learn   --logs <dir>        [--store <path>] [--json]")
	fmt.Fprintln(os.Stderr, "  becky-habits show                        [--store <path>] [--json]")
	fmt.Fprintln(os.Stderr, "  becky-habits usual   [<scope>]           [--store <path>] [--json]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "observe  ingest one corrections file (JSON array or {\"corrections\":[...]})")
	fmt.Fprintln(os.Stderr, "learn    walk a directory of *.jsonl logs from hum/vox/daw/canvas tools")
	fmt.Fprintln(os.Stderr, "show     report what becky has learned — scalar defaults AND structured setups")
	fmt.Fprintln(os.Stderr, "usual    recall your usual structured setup(s) (e.g. `usual bus.drums`)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "A correction recurring >= 2 times becomes a learned default. A structured")
	fmt.Fprintln(os.Stderr, "fix (a JSON blob like an FX chain or sidechain route) is learned the same way.")
	fmt.Fprintln(os.Stderr, "Exit codes: 0 ok, 1 error, 2 usage.")
}
