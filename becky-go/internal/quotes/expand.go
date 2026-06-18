// expand.go — recursive sentence-context expansion (SPEC §5, hard rule #2).
//
// After selection, each anchor is a span of sentences. We ask the Expander
// (a local model, yes/no) whether the sentence immediately BEFORE and the one
// immediately AFTER add necessary context to the CURRENT (already-expanded)
// block. If yes, we include it and RE-ASSESS the new neighbors — exactly the
// "re-assess after each inclusion" the requester described. We stop on no, or on
// a safety cap (max sentences per side / max region seconds), so a chatty stream
// can't expand a block into the whole dialogue.
//
// Determinism: the Expander runs at temperature 0; every yes/no decision is
// recorded so the engine can write the --log rationale (why a block is the size
// it is). With NO Expander (Exact/JSON modes, no model), expansion is a no-op:
// the block stays exactly the anchor's sentences.
package quotes

import (
	"context"
	"strings"
)

// ExpandCaps bound the recursive expansion (SPEC §5.4). Defaults are set by the
// CLI (--max-context-sentences, --max-region-seconds).
type ExpandCaps struct {
	MaxSentencesPerSide int     // stop extending a side after this many inclusions
	MaxRegionSeconds    float64 // stop once the block's duration reaches this
}

// expandDecision records one neighbor judgment for the audit log (SPEC §5.5).
type expandDecision struct {
	Side     string `json:"side"`     // "before" | "after"
	Sentence string `json:"sentence"` // the neighbor text considered
	Included bool   `json:"included"` // the model's yes/no
}

// sentenceBlock is a contiguous run of sentence indices [First,Last] (into the
// sentences slice). Expansion grows First down and Last up.
type sentenceBlock struct {
	First int
	Last  int
}

// expandBlock grows blk over sentences using exp, honoring caps. cues backs the
// duration check. Returns the grown block plus the decisions taken (for --log)
// and counts of how many sentences were added on each side. exp may be nil (no
// model) -> returns blk unchanged with zero expansion.
func expandBlock(ctx context.Context, blk sentenceBlock, sentences []Sentence, cues []Cue, exp Expander, caps ExpandCaps) (sentenceBlock, []expandDecision, int, int) {
	var decisions []expandDecision
	addedBefore, addedAfter := 0, 0
	if exp == nil || len(sentences) == 0 {
		return blk, decisions, 0, 0
	}

	beforeOpen, afterOpen := true, true
	for beforeOpen || afterOpen {
		// duration cap: stop everything once the block is long enough.
		if caps.MaxRegionSeconds > 0 && blockSeconds(blk, sentences, cues) >= caps.MaxRegionSeconds {
			break
		}
		extended := false

		// PREV neighbor (judged against the current block text).
		if beforeOpen {
			if caps.MaxSentencesPerSide > 0 && addedBefore >= caps.MaxSentencesPerSide {
				beforeOpen = false
			} else if blk.First-1 >= 0 {
				prev := sentences[blk.First-1]
				yes, err := exp.NeedsContext(ctx, blockText(blk, sentences), prev.Text)
				if err != nil {
					// model failed mid-expansion: stop cleanly, keep what we have.
					break
				}
				decisions = append(decisions, expandDecision{Side: "before", Sentence: clipForLog(prev.Text), Included: yes})
				if yes {
					blk.First--
					addedBefore++
					extended = true
				} else {
					beforeOpen = false
				}
			} else {
				beforeOpen = false
			}
		}

		// NEXT neighbor — re-read the (possibly extended) block text.
		if afterOpen {
			if caps.MaxSentencesPerSide > 0 && addedAfter >= caps.MaxSentencesPerSide {
				afterOpen = false
			} else if blk.Last+1 < len(sentences) {
				next := sentences[blk.Last+1]
				yes, err := exp.NeedsContext(ctx, blockText(blk, sentences), next.Text)
				if err != nil {
					break
				}
				decisions = append(decisions, expandDecision{Side: "after", Sentence: clipForLog(next.Text), Included: yes})
				if yes {
					blk.Last++
					addedAfter++
					extended = true
				} else {
					afterOpen = false
				}
			} else {
				afterOpen = false
			}
		}

		if !extended {
			break
		}
	}
	return blk, decisions, addedBefore, addedAfter
}

// blockText joins the sentence texts spanned by blk.
func blockText(blk sentenceBlock, sentences []Sentence) string {
	var parts []string
	for i := blk.First; i <= blk.Last && i < len(sentences); i++ {
		if i < 0 {
			continue
		}
		parts = append(parts, sentences[i].Text)
	}
	return strings.Join(parts, " ")
}

// blockSeconds is the wall-clock duration the block currently spans, using the
// enclosing cues of its first/last sentence.
func blockSeconds(blk sentenceBlock, sentences []Sentence, cues []Cue) float64 {
	if blk.First < 0 || blk.Last >= len(sentences) || len(cues) == 0 {
		return 0
	}
	startCue := sentences[blk.First].FirstCue
	endCue := sentences[blk.Last].LastCue
	if startCue < 0 || endCue >= len(cues) || endCue < startCue {
		return 0
	}
	return cues[endCue].End - cues[startCue].Start
}

// clipForLog shortens a sentence for the rationale log so the --log stays
// readable.
func clipForLog(s string) string {
	const max = 160
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
