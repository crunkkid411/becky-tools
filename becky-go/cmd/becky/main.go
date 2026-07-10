// becky — the top-level orchestrator ("the brain"). It turns plain-language named
// operations into the right chain of existing becky-*.exe tools, so a non-developer
// never needs to know a flag. Recall-first; it surfaces candidate MOMENTS (clips,
// frames, timestamps) for human review and never concludes on its own.
//
//	becky enroll-wiki [--kb <dir>] [--wiki <dir>]...   build the KB from the wiki
//	becky index <corpus-dir> [--db <path>] [--kb <dir>] transcribe+embed a corpus
//	becky profile "<name>" [--corpus <dir>] [--kb <dir>] summary card for a person
//	becky appearances "<name>" [--corpus <dir>] [--kb]   where a person appears
//	becky find "<query>" [--db <path>]                   hybrid search the corpus
//	becky corroborate "<claim>" [--db] [--corpus] [--kb] cross-reference a claim
//	becky ingest <folder> [--kb <dir>] [--no-pipeline]   run the pipeline + write DIGEST.md
//	becky list [--json]                                  machine-readable tool inventory
//
// Each op prints a plain-English summary to stderr (the headline) AND a structured
// JSON result to stdout (the underlying data + paths to the actual clips/frames).
// It is a thin driver: it shells out to the existing becky binaries (resolved next
// to this executable or via --bin) and never re-implements their logic. Exit 0 on
// success; non-zero on a usage/fatal error.
package main

import (
	"fmt"
	"os"
	"strings"

	"becky-go/internal/beckyio"
)

// usage is printed when no/!known op is given.
const usage = `becky — forensic-video orchestrator

Usage:
  becky enroll-wiki [--kb <dir>] [--wiki <dir>]...      build the identify KB from the case wiki
  becky learn "<name>" <clip> [--kb <dir>]              teach the KB one person from one clip
  becky "this is <name>" <clip> [--kb <dir>]            same, in plain language (no keyword)
  becky index <corpus-dir> [--db <path>] [--kb <dir>]   transcribe + embed a folder of videos
  becky profile "<name>" [--corpus <dir>] [--kb <dir>]  one-card summary for a person
  becky appearances "<name>" [--corpus <dir>] [--kb]    which videos a person appears in
  becky find "<query>" [--db <path>]                    natural-language search of the corpus
  becky corroborate "<claim>" [--db] [--corpus] [--kb]  cross-reference a claim for review
  becky ingest <folder> [--kb <dir>] [--no-pipeline]    run the forensic pipeline over a folder + write one DIGEST.md
  becky list [--json]                                   machine-readable inventory: installed tools + one-line contracts

Common flags: --bin <dir> (becky-*.exe location), --verbose, --json (JSON only, no headline)

Recall-first: becky surfaces candidate moments for human review. It never concludes.`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	op := strings.ToLower(args[0])
	rest := args[1:]

	// NATURAL-LANGUAGE TEACH: `becky "this is Shelby" <clip>` (no "enroll", no "learn"
	// keyword). If the first argument is a teaching phrase ("this is X", "that's X",
	// "this is a video of X", ...), route the WHOLE arg list to the learn op, which
	// extracts the name from the phrase. Checked before the op switch so the phrase is
	// not mistaken for an unknown operation.
	if name, ok := parseTeachPhrase(args[0]); ok {
		if err := runLearn(append([]string{name}, rest...)); err != nil {
			beckyio.Fatalf("%v", err)
		}
		return
	}

	var err error
	switch op {
	case "enroll-wiki", "enroll":
		err = runEnrollWiki(rest)
	case "learn", "teach", "this-is":
		err = runLearn(rest)
	case "index":
		err = runIndex(rest)
	case "profile":
		err = runProfile(rest)
	case "appearances", "appearance":
		err = runAppearances(rest)
	case "find", "search":
		err = runFind(rest)
	case "corroborate":
		err = runCorroborate(rest)
	case "ingest":
		err = runIngest(rest)
	case "list":
		err = runList(rest)
	case "help", "-h", "--help":
		fmt.Fprintln(os.Stderr, usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown operation: %s\n\n%s\n", op, usage)
		os.Exit(2)
	}

	if err != nil {
		beckyio.Fatalf("%v", err)
	}
}
