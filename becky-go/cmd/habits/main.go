// becky-habits — the preference-learning loop's brain. becky's tools already log
// every time an AUTO value is overridden by Jordan's MANUAL fix; this tool turns a
// REPEATED correction into a learned DEFAULT ("you always pull the kick to -7 →
// that becomes the default"). It is a Go port of dawbase's MIT HabitStore.
//
//	becky-habits observe <corrections.json> [--store <path>] [--json]
//	becky-habits show [--store <path>] [--json]
//
// `observe` ingests a corrections log and updates the store. `show` reports, in
// plain language, what becky has LEARNED (corroborated >= the threshold) and what
// is still a CANDIDATE (seen once). Conservative by design: a one-off fix never
// becomes a default — corroborate, then conclude (CLAUDE.md §2).
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
	case "show":
		return runShow(args[1:])
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
	fmt.Fprintln(os.Stderr, "  becky-habits show [--store <path>] [--json]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "A correction recurring >= 2 times becomes a learned default.")
	fmt.Fprintln(os.Stderr, "Exit codes: 0 ok, 1 error, 2 usage.")
}
