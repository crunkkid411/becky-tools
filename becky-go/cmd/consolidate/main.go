// becky-consolidate — propagate confirmed names across the corpus and report
// per-entity recognition coverage so accuracy gaps are VISIBLE.
//
//	becky-consolidate [--db forensic.db] [--threshold 0.8] [--dry-run]
//	                  [--format json|txt] [--verbose]
//	becky-consolidate --ingest <identified.json> --source <video>
//	                  [--verified-by <name>] [--db forensic.db]
//
// Two modes, one binary:
//
//  1. --ingest: load a real becky-identify result into the shared `identifications`
//     table (each identifications[] entry -> one row). --verified-by marks the
//     rows confirmed; omitted leaves them unconfirmed (a model guess). This is the
//     identify->DB bridge that makes the corpus real and the report testable.
//
//  2. default (report): read all identifications, compute per-entity coverage
//     (overall + per modality voice/face/location) over the distinct-video corpus,
//     flag coverage gaps with deterministic templated suggestions, then PROPAGATE
//     each confirmed entity's name onto that entity's other unconfirmed rows whose
//     confidence clears --threshold. --dry-run reports the propagation plan without
//     writing.
//
// Deterministic only — NO LLM (Jordan's rule for non-review tools). Names are only
// ever propagated from a CONFIRMED identification; nothing is invented. JSON to
// stdout (primary contract); --format txt renders the human report. Diagnostics to
// stderr; exit 0 on success (including an empty DB), exit 1 on error.
package main

import (
	"flag"
	"fmt"
	"os"

	"becky-go/internal/beckydb"
	"becky-go/internal/beckyio"
	"becky-go/internal/config"
)

func main() {
	dbPath := flag.String("db", "forensic.db", "SQLite database path")
	threshold := flag.Float64("threshold", 0.8, "confidence threshold for auto-propagation (0..1)")
	dryRun := flag.Bool("dry-run", false, "show what would be propagated without writing")
	format := flag.String("format", "json", "output format: json, txt")
	verbose := flag.Bool("verbose", false, "show progress on stderr")

	// Ingest mode (identify -> DB bridge).
	ingest := flag.String("ingest", "", "ingest a becky-identify JSON into the identifications table")
	source := flag.String("source", "", "source video label for --ingest rows (defaults to the JSON's \"file\")")
	verifiedBy := flag.String("verified-by", "", "mark --ingest rows confirmed by this name (omit = unconfirmed)")

	flag.Parse()

	if *format != "json" && *format != "txt" {
		beckyio.Fatalf("--format must be json or txt (got %q)", *format)
	}
	if *threshold < 0 || *threshold > 1 {
		beckyio.Fatalf("--threshold must be in [0,1] (got %g)", *threshold)
	}

	cfg := config.Load()
	db, err := beckydb.Open(cfg, *dbPath)
	if err != nil {
		beckyio.Fatalf("open db: %v", err)
	}
	// EnsureSchema makes the tool robust against an empty/new DB: it creates the
	// canonical tables (including identifications) so reads return zero rows
	// instead of erroring, and ingest has a table to write into.
	if err := db.EnsureSchema(); err != nil {
		beckyio.Fatalf("ensure schema: %v", err)
	}

	if *ingest != "" {
		if err := runIngest(db, *ingest, *source, *verifiedBy, *verbose); err != nil {
			beckyio.Fatalf("%v", err)
		}
		return
	}

	report, err := buildReport(db, *dbPath, *threshold, *dryRun, *verbose)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}

	if *format == "txt" {
		fmt.Print(renderTxt(report))
		return
	}
	beckyio.PrintJSON(report)
}

// fileExists reports whether path is an existing file (used to validate --ingest).
func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}
