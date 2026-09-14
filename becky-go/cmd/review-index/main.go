// becky-review-index — the offline folder index + transcript search behind the
// Becky Review app's LEFT pane (gui/BeckyReview) and the VEGAS "Becky Search"
// panel. It is a thin JSON wrapper over the existing internal/footage engine — it
// reimplements nothing:
//
//	becky-review-index --folder <dir>                     -> list every video + its transcript
//	becky-review-index --folder <dir> --search "cat"      -> ranked transcript cue hits (with timecodes)
//	becky-review-index --timeline <tl.json> --search "x"  -> hits in the clips ON a VEGAS timeline,
//	                                                         each with where it sits on the ruler
//
// The timeline JSON is the same contract BeckyCaptions.cs writes
// (internal/edl/vegastimeline.go): every event's source file, source in/out and
// ruler position, plus an optional playback rate.
//
// Pure Go, offline, deterministic, NO model and NO DB (footage's Tier-0 keyword
// grep). The original media is never opened. JSON to stdout; diagnostics to
// stderr; exit 0 on success (including no-results), nonzero on a fatal error.
//
// Shape (stdout):
//
//	{
//	  "root": "<abs folder>",                                   // "" in timeline mode
//	  "videos":   [ {path,name,has_transcript,transcript_path,meta{...}}, ... ],
//	  "candidates":[ {source,name,timestamp,end,text,score,terms}, ... ]   // folder mode, only with --search
//	  "timeline_candidates":[ {...same fields..., on_timeline:[{timeline,timeline_end,track}]} ]  // timeline mode
//	}
package main

import (
	"flag"
	"sort"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/edl"
	"becky-go/internal/footage"
)

// output is the stdout JSON contract consumed by the Becky Review UI and the
// VEGAS panel. Candidates is nil (omitted) when no --search was requested, so the
// UI knows to list the videos; otherwise it lists the ranked cue hits.
type output struct {
	Root               string              `json:"root"`
	Videos             []footage.Video     `json:"videos"`
	Candidates         []footage.Candidate `json:"candidates,omitempty"`
	TimelineCandidates []timelineCandidate `json:"timeline_candidates,omitempty"`
}

// timelineCandidate is a cue hit plus every place the edit shows it. An empty
// OnTimeline means the line exists in a clip on the timeline but was cut out.
type timelineCandidate struct {
	footage.Candidate
	OnTimeline []edl.TimelineHit `json:"on_timeline"`
}

func main() {
	folder := flag.String("folder", "", "case folder to index")
	timeline := flag.String("timeline", "", "VEGAS timeline JSON (internal/edl VegasTimeline): search only the clips on it")
	search := flag.String("search", "", "optional space-separated transcript terms to rank cue hits")
	limit := flag.Int("limit", 500, "max candidate cue hits to return when --search is given")
	flag.Parse()

	hasFolder, hasTimeline := strings.TrimSpace(*folder) != "", strings.TrimSpace(*timeline) != ""
	switch {
	case hasFolder == hasTimeline:
		beckyio.Fatalf("give exactly one of --folder or --timeline")
	case hasTimeline:
		tl, err := edl.LoadVegasTimeline(*timeline)
		if err != nil {
			beckyio.Fatalf("timeline %q: %v", *timeline, err)
		}
		beckyio.PrintJSON(searchTimeline(tl, splitTerms(*search), *limit))
		return
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

// searchTimeline greps the transcripts of exactly the clips on the timeline and
// puts each hit back on the ruler. Hits that are in the edit come first, in ruler
// order - an editor asking "where do I say this" reads the edit top to bottom.
// Lines that were cut out follow, in footage's own rank order. The limit applies
// after that ordering so it never trims away a hit that is on the timeline in
// favour of one that is not.
func searchTimeline(tl edl.VegasTimeline, terms []string, limit int) output {
	sources := make([]string, 0, len(tl.Events))
	for _, e := range tl.Events {
		sources = append(sources, e.Source)
	}
	idx := footage.IndexFiles(sources)
	out := output{Videos: idx.Videos}
	if len(terms) == 0 {
		return out
	}

	cands := footage.GrepTranscripts(idx, terms)
	hits := make([]timelineCandidate, 0, len(cands))
	for _, c := range cands {
		hits = append(hits, timelineCandidate{Candidate: c, OnTimeline: tl.SourceHits(c.Source, c.Timestamp, c.End)})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i].OnTimeline, hits[j].OnTimeline
		if (len(a) > 0) != (len(b) > 0) {
			return len(a) > 0
		}
		if len(a) > 0 && a[0].Timeline != b[0].Timeline {
			return a[0].Timeline < b[0].Timeline
		}
		return false
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	out.TimelineCandidates = hits
	return out
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
