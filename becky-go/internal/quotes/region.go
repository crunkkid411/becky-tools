// region.go — boundary snapping (SPEC §6), overlap/merge/order (SPEC §7), and
// SubRip emit (SPEC §8). A Region is a span of source cues; its timestamps are
// the VERBATIM start/end strings of the first/last cue (timestamp identity, hard
// rule #4). Region text is the verbatim concatenation of the spanned cues' text
// — never a paraphrase.
package quotes

import (
	"fmt"
	"sort"
	"strings"
)

// Region is one emitted quote block. Times are kept as both verbatim strings
// (StartRaw/EndRaw — what the .srt gets) and seconds (for sort/merge math).
type Region struct {
	StartCue int     // source cue index of the block's first word
	EndCue   int     // source cue index of the block's last word
	StartRaw string  // == cues[StartCue].StartRaw (emitted verbatim)
	EndRaw   string  // == cues[EndCue].EndRaw (emitted verbatim)
	Start    float64 // seconds (math only)
	End      float64 // seconds (math only)
	Text     string  // verbatim concatenation of spanned cue text
	Because  string  // selection rationale (for JSON/log; never in the .srt)

	ExpandedBefore int // sentences added before the anchor (provenance)
	ExpandedAfter  int // sentences added after the anchor (provenance)
}

// regionFromCueSpan snaps a [startCue,endCue] span to a Region with verbatim
// timestamps. Sentence boundaries that fell mid-cue are already widened to the
// enclosing cues by the caller (we never trim to a sub-cue time, which would
// invent a timestamp). Returns ok=false on an out-of-range span.
func regionFromCueSpan(cues []Cue, startCue, endCue int) (Region, bool) {
	if startCue < 0 || endCue < 0 || startCue >= len(cues) || endCue >= len(cues) {
		return Region{}, false
	}
	if endCue < startCue {
		startCue, endCue = endCue, startCue
	}
	var parts []string
	for i := startCue; i <= endCue; i++ {
		if t := strings.TrimSpace(cues[i].Text); t != "" {
			parts = append(parts, t)
		}
	}
	return Region{
		StartCue: startCue,
		EndCue:   endCue,
		StartRaw: cues[startCue].StartRaw,
		EndRaw:   cues[endCue].EndRaw,
		Start:    cues[startCue].Start,
		End:      cues[endCue].End,
		Text:     strings.Join(parts, " "),
	}, true
}

// mergeRegions merges regions that overlap or sit within mergeGapSec seconds of
// each other (SPEC §7), sorts chronologically, and de-duplicates identical
// spans. Merge is by CUE span (so the merged region's verbatim timestamps still
// come from real cues). cues backs re-derivation of the merged text/timestamps.
func mergeRegions(regions []Region, cues []Cue, mergeGapSec float64) []Region {
	if len(regions) == 0 {
		return nil
	}
	// sort by start cue, then end cue.
	sort.SliceStable(regions, func(i, j int) bool {
		if regions[i].StartCue != regions[j].StartCue {
			return regions[i].StartCue < regions[j].StartCue
		}
		return regions[i].EndCue < regions[j].EndCue
	})

	merged := make([]Region, 0, len(regions))
	cur := regions[0]
	for _, r := range regions[1:] {
		// Overlap or adjacency by cue, or a small time gap between the current
		// end and the next start, triggers a merge.
		gap := cues[r.StartCue].Start - cues[cur.EndCue].End
		overlapOrAdjacent := r.StartCue <= cur.EndCue+1
		withinGap := mergeGapSec > 0 && gap >= 0 && gap <= mergeGapSec
		if overlapOrAdjacent || withinGap {
			if r.EndCue > cur.EndCue {
				cur.EndCue = r.EndCue
			}
			if r.ExpandedBefore > cur.ExpandedBefore {
				cur.ExpandedBefore = r.ExpandedBefore
			}
			if r.ExpandedAfter > cur.ExpandedAfter {
				cur.ExpandedAfter = r.ExpandedAfter
			}
			if cur.Because == "" {
				cur.Because = r.Because
			}
			continue
		}
		merged = append(merged, rebuildRegion(cur, cues))
		cur = r
	}
	merged = append(merged, rebuildRegion(cur, cues))
	return merged
}

// rebuildRegion re-derives a region's verbatim timestamps + text from its
// (possibly widened) cue span, so a merged region's fields stay byte-identical to
// the source cues.
func rebuildRegion(r Region, cues []Cue) Region {
	rebuilt, ok := regionFromCueSpan(cues, r.StartCue, r.EndCue)
	if !ok {
		return r
	}
	rebuilt.Because = r.Because
	rebuilt.ExpandedBefore = r.ExpandedBefore
	rebuilt.ExpandedAfter = r.ExpandedAfter
	return rebuilt
}

// WriteSRT renders regions as a standard SubRip file: 1-based index, verbatim
// "start --> end", verbatim text, blank-line separator. NO comment lines (SPEC
// §8) — provenance lives in the JSON summary / --log, never in the .srt.
func WriteSRT(regions []Region) string {
	var b strings.Builder
	for i, r := range regions {
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, r.StartRaw, r.EndRaw, r.Text)
	}
	return b.String()
}
