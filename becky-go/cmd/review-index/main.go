// becky-review-index — the offline folder index + transcript search behind the
// Becky Review app's LEFT pane (gui/BeckyReview). It is a thin JSON wrapper over
// the existing internal/footage engine — it reimplements nothing:
//
//	becky-review-index --folder <dir>                 -> list every video + its transcript
//	becky-review-index --folder <dir> --search "cat"  -> ranked transcript cue hits (with timecodes)
//
// Pure Go, offline, deterministic, NO model and NO DB (footage's Tier-0 keyword
// grep). The original media is never opened. JSON to stdout; diagnostics to
// stderr; exit 0 on success (including no-results), nonzero on a fatal error.
//
// Shape (stdout):
//
//	{
//	  "root": "<abs folder>",
//	  "videos":   [ {path,name,has_transcript,transcript_path,meta{...}}, ... ],
//	  "candidates":[ {source,name,timestamp,end,text,score,terms}, ... ]   // only with --search
//	}
package main

import (
	"flag"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/footage"
)

// output is the stdout JSON contract consumed by the Becky Review UI. Candidates
// is nil (omitted) when no --search was requested, so the UI knows to list the
// videos; otherwise it lists the ranked cue hits.
type output struct {
	Root       string              `json:"root"`
	Videos     []footage.Video     `json:"videos"`
	Candidates []footage.Candidate `json:"candidates,omitempty"`
}

func main() {
	folder := flag.String("folder", "", "case folder to index (required)")
	search := flag.String("search", "", "optional space-separated transcript terms to rank cue hits")
	limit := flag.Int("limit", 500, "max candidate cue hits to return when --search is given")
	flag.Parse()

	if strings.TrimSpace(*folder) == "" {
		beckyio.Fatalf("--folder is required")
	}

	idx, err := footage.Index(*folder)
	if err != nil {
		beckyio.Fatalf("index %q: %v", *folder, err)
	}

	out := output{Root: idx.Root, Videos: idx.Videos}

	if terms := splitTerms(*search); len(terms) > 0 {
		// Merge spoken-transcript hits with orphan-transcript hits (footage already
		// orders each deterministically; both share the Candidate shape).
		cands := footage.GrepTranscripts(idx, terms)
		cands = append(cands, footage.GrepOrphans(idx, terms)...)
		if *limit > 0 && len(cands) > *limit {
			cands = cands[:*limit]
		}
		out.Candidates = cands
	}

	beckyio.PrintJSON(out)
}

// splitTerms breaks the --search string into terms on whitespace, dropping blanks.
// footage.GrepTranscripts itself normalizes (lowercase/dedup), so this only needs
// to tokenize.
func splitTerms(s string) []string {
	fields := strings.Fields(s)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}
