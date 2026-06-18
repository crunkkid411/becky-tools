// engine.go — the end-to-end pipeline (SPEC §10) that turns a parsed transcript
// + a Selector into regions and a JSON summary:
//
//	parse (cues.go) -> segment (segment.go) -> SELECT anchors (Selector) ->
//	resolve each anchor to a cue span (index.go) -> recursive expansion
//	(expand.go) -> snap to verbatim cue timestamps (region.go) ->
//	merge/sort/renumber (region.go) -> Summary + optional rationale log.
//
// The deterministic core (Exact/JSON selection + all mapping/expand/snap/merge/
// emit) runs with NO model. Only LocalSelector/LLMExpander touch a model, and
// both degrade rather than crash. Determinism: anchors are de-duplicated and
// sorted, so the same inputs at temperature 0 yield identical output.
package quotes

import (
	"context"
	"strconv"
	"strings"
)

// Options configures one Run. Selector is required; Expander is optional (nil =>
// no context expansion, used by Exact/JSON modes without a model).
type Options struct {
	Selector Selector
	Expander Expander   // optional; nil disables recursive expansion
	Criteria string     // selection objective (--criteria)
	Caps     ExpandCaps // expansion safety caps (--max-context-sentences / --max-region-seconds)
	MergeGap float64    // merge regions within this many seconds (--merge-gap)

	// ChunkCues / ChunkOverlap window long transcripts for the Selector (SPEC
	// §4.5). 0 ChunkCues => no chunking (one Select over the whole transcript).
	ChunkCues    int
	ChunkOverlap int
}

// Summary is the structured result returned to the CLI for the stdout JSON
// (SPEC §2). It does not include file paths/model id — the CLI wraps it with
// those for the final payload.
type Summary struct {
	Regions       []RegionSummary
	SelectedCount int // anchors that resolved to a region (pre-merge)
	AfterMerge    int // regions after merge
	Unmatched     []string
	Decisions     []RegionDecisions // per-region expansion rationale (for --log)

	// regions carries the rendered Region slice so the CLI can WriteSRT without
	// re-running the pipeline. Unexported: not part of the JSON payload.
	regions []Region
}

// RegionSummary is one region in the stdout JSON (SPEC §2 "regions[]").
type RegionSummary struct {
	Index           int    `json:"index"`
	Start           string `json:"start"`
	End             string `json:"end"`
	StartCue        int    `json:"start_cue"`
	EndCue          int    `json:"end_cue"`
	Text            string `json:"text"`
	SelectedBecause string `json:"selected_because"`
	ExpandedBefore  int    `json:"expanded_before"`
	ExpandedAfter   int    `json:"expanded_after"`
}

// RegionDecisions ties a region's verbatim quote to the yes/no neighbor
// decisions taken while expanding it (written to --log so a human can audit WHY a
// block is the size it is — a forensic requirement, SPEC §5.5).
type RegionDecisions struct {
	Start     string           `json:"start"`
	End       string           `json:"end"`
	Because   string           `json:"selected_because"`
	Decisions []expandDecision `json:"decisions"`
}

// resolvedAnchor is an anchor after it has been located in the transcript.
type resolvedAnchor struct {
	startCue  int
	endCue    int
	because   string
	decisions []expandDecision
	addBefore int
	addAfter  int
}

// Run executes the pipeline over cues with opts and returns the regions + a
// summary. Selection errors (e.g. a model unavailable in LLM mode) propagate so
// the CLI can degrade with a clear note; everything past selection is
// deterministic and cannot fail on data.
func Run(ctx context.Context, cues []Cue, opts Options) (Summary, error) {
	idx := buildIndex(cues)

	// 1) segment into sentences, falling back to cue-level units if poor.
	sentences := Segment(cues)
	if SegmentationPoor(cues, sentences) {
		sentences = cueLevelSentences(cues)
	}

	// 2) SELECT anchors (possibly chunked for long transcripts).
	anchors, err := selectAnchors(ctx, cues, opts)
	if err != nil {
		return Summary{}, err
	}

	// 3) resolve + 5) expand each anchor; collect unmatched.
	var resolved []resolvedAnchor
	var unmatched []string
	seenSpan := map[[2]int]bool{}
	for _, a := range anchors {
		sc, ec, ok := resolveAnchor(idx, cues, a)
		if !ok {
			if q := strings.TrimSpace(a.Quote); q != "" {
				unmatched = append(unmatched, q)
			}
			continue
		}
		// map the cue span to the covering sentence range, expand, re-snap.
		blk := sentenceRangeForCueSpan(sentences, sc, ec)
		grown, decisions, addB, addA := expandBlock(ctx, blk, sentences, cues, opts.Expander, opts.Caps)
		esc, eec := cueSpanForSentenceRange(sentences, grown, sc, ec)
		key := [2]int{esc, eec}
		if seenSpan[key] {
			continue // identical span already taken
		}
		seenSpan[key] = true
		resolved = append(resolved, resolvedAnchor{
			startCue: esc, endCue: eec, because: a.Because,
			decisions: decisions, addBefore: addB, addAfter: addA,
		})
	}

	// 6) snap to regions.
	var regions []Region
	for _, r := range resolved {
		reg, ok := regionFromCueSpan(cues, r.startCue, r.endCue)
		if !ok {
			continue
		}
		reg.Because = r.because
		reg.ExpandedBefore = r.addBefore
		reg.ExpandedAfter = r.addAfter
		regions = append(regions, reg)
	}
	selectedCount := len(regions)

	// 7) merge overlaps/near-neighbors; sort. Renumber happens at emit.
	merged := mergeRegions(regions, cues, opts.MergeGap)

	// build the summary + the per-region decision log (aligned by start time).
	sum := Summary{
		SelectedCount: selectedCount,
		AfterMerge:    len(merged),
		Unmatched:     unmatched,
		regions:       merged,
	}
	decByStart := map[string][]expandDecision{}
	for _, r := range resolved {
		reg, ok := regionFromCueSpan(cues, r.startCue, r.endCue)
		if !ok {
			continue
		}
		if _, dup := decByStart[reg.StartRaw]; !dup {
			decByStart[reg.StartRaw] = r.decisions
		}
	}
	for i, reg := range merged {
		sum.Regions = append(sum.Regions, RegionSummary{
			Index:           i + 1,
			Start:           reg.StartRaw,
			End:             reg.EndRaw,
			StartCue:        cueOneBased(reg.StartCue, cues),
			EndCue:          cueOneBased(reg.EndCue, cues),
			Text:            reg.Text,
			SelectedBecause: reg.Because,
			ExpandedBefore:  reg.ExpandedBefore,
			ExpandedAfter:   reg.ExpandedAfter,
		})
		if d := decByStart[reg.StartRaw]; len(d) > 0 {
			sum.Decisions = append(sum.Decisions, RegionDecisions{
				Start: reg.StartRaw, End: reg.EndRaw, Because: reg.Because, Decisions: d,
			})
		}
	}
	return sum, nil
}

// SRT returns the rendered <video-stem>_QUOTES.srt body for this summary's
// regions (standard SubRip, verbatim timestamps, no comment lines).
func (s *Summary) SRT() string { return WriteSRT(s.regions) }

// selectAnchors runs the Selector once over the whole transcript, or windowed
// over chunks for long transcripts (SPEC §4.5), de-duplicating anchors by
// normalized quote.
func selectAnchors(ctx context.Context, cues []Cue, opts Options) ([]Anchor, error) {
	if opts.ChunkCues <= 0 || len(cues) <= opts.ChunkCues {
		return opts.Selector.Select(ctx, joinCueText(cues, 0, len(cues)), opts.Criteria)
	}
	overlap := opts.ChunkOverlap
	if overlap < 0 {
		overlap = 0
	}
	step := opts.ChunkCues - overlap
	if step <= 0 {
		step = opts.ChunkCues
	}
	var all []Anchor
	seen := map[string]bool{}
	for start := 0; start < len(cues); start += step {
		end := start + opts.ChunkCues
		if end > len(cues) {
			end = len(cues)
		}
		text := joinCueText(cues, start, end)
		got, err := opts.Selector.Select(ctx, text, opts.Criteria)
		if err != nil {
			return nil, err
		}
		for _, a := range got {
			key := normalize(a.Quote)
			if a.Cue >= 0 {
				key = "cue:" + strconv.Itoa(a.Cue)
			}
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			all = append(all, a)
		}
		if end == len(cues) {
			break
		}
	}
	return all, nil
}

// resolveAnchor turns an Anchor into a cue span. A direct Cue index wins; else
// the Quote is located in the transcript (with the optional timecode hint).
func resolveAnchor(idx *cueIndex, cues []Cue, a Anchor) (startCue, endCue int, ok bool) {
	if a.Cue >= 0 && a.Cue < len(cues) {
		return a.Cue, a.Cue, true
	}
	if strings.TrimSpace(a.Quote) == "" {
		return 0, 0, false
	}
	hint := -1.0
	if h := strings.TrimSpace(a.hint); h != "" {
		hint = timecodeToSeconds(h)
	}
	return idx.locate(a.Quote, 0, hint)
}

// sentenceRangeForCueSpan returns the sentence block whose sentences cover the
// cue span [sc,ec]. If sentences is empty, returns a degenerate block the
// expander treats as a no-op.
func sentenceRangeForCueSpan(sentences []Sentence, sc, ec int) sentenceBlock {
	if len(sentences) == 0 {
		return sentenceBlock{First: 0, Last: -1}
	}
	first := -1
	last := -1
	for i, s := range sentences {
		if s.LastCue < sc || s.FirstCue > ec {
			continue
		}
		if first == -1 {
			first = i
		}
		last = i
	}
	if first == -1 {
		// no sentence overlaps (shouldn't happen) — degenerate to the first.
		first, last = 0, 0
	}
	return sentenceBlock{First: first, Last: last}
}

// cueSpanForSentenceRange converts an expanded sentence block back to a cue span.
// fallbackSC/fallbackEC are used when the block is degenerate (no sentences).
func cueSpanForSentenceRange(sentences []Sentence, blk sentenceBlock, fallbackSC, fallbackEC int) (int, int) {
	if blk.First < 0 || blk.Last < blk.First || blk.Last >= len(sentences) {
		return fallbackSC, fallbackEC
	}
	return sentences[blk.First].FirstCue, sentences[blk.Last].LastCue
}

// joinCueText concatenates cue text over [start,end) with single spaces.
func joinCueText(cues []Cue, start, end int) string {
	var parts []string
	for i := start; i < end && i < len(cues); i++ {
		if t := strings.TrimSpace(cues[i].Text); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " ")
}

// cueOneBased converts a 0-based cue index to the 1-based source index recorded
// on the cue (provenance for the JSON summary).
func cueOneBased(i int, cues []Cue) int {
	if i < 0 || i >= len(cues) {
		return 0
	}
	return cues[i].Index
}
