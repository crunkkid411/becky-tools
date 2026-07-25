package subs

import "strings"

// The caption chunker is ChunkWords (subs.go), which applies Jordan's rules directly.
// The old accretion (rebalanceCapSplits, tiered splitAtBiggestPause, overflow slack,
// number rules, mergeContentless cascades, forceUnderCap, splitAtHardBoundaries) is
// gone (2026-07-24). Only two small helpers remain, because they ARE Jordan's rules:
// splitToFit (an over-cap phrase breaks at its biggest internal pause, never a dumb
// wrap) and pushDanglers (a line never ends on a/the/to/and...).

// lineLen is the rendered character length of a caption line (words joined by spaces).
func lineLen(chunk []Word) int {
	n := 0
	for i, w := range chunk {
		if i > 0 {
			n++
		}
		n += len(strings.TrimSpace(w.Word))
	}
	return n
}

// danglers are function words that modify what FOLLOWS them, so a caption line should
// not END on one (Jordan: "a line must never end on a, the, to, and..."). Kept small.
var danglers = map[string]bool{
	"a": true, "an": true, "the": true, "this": true, "these": true, "those": true,
	"my": true, "your": true, "his": true, "her": true, "its": true, "our": true, "their": true,
	"and": true, "or": true, "but": true, "if": true, "so": true, "than": true, "that": true,
	"of": true, "to": true, "in": true, "on": true, "at": true, "for": true, "with": true,
	"from": true, "by": true, "into": true, "onto": true, "about": true, "as": true,
}

// isDangler reports whether ending a line on this word would split a phrase.
func isDangler(word string) bool {
	s := strings.ToLower(strings.TrimRight(strings.TrimSpace(word), `.,;:!?"')`))
	return danglers[s]
}

// splitToFit breaks a phrase run that is over the cap into lines that fit, filling
// each line with as many whole words as the cap allows (never a mid-word wrap) and
// letting the leftover start the next line. Biggest-gap splitting was tried and
// fragmented short pieces ("bullshit" | "advice"); greedy fill keeps lines full and
// predictable, and pushDanglers then makes sure none ends on a/the/to/and. A single
// word longer than the cap simply stands on its own line.
func splitToFit(run []Word, maxChars int) [][]Word {
	if maxChars <= 0 || lineLen(run) <= maxChars {
		return [][]Word{run}
	}
	var out [][]Word
	var cur []Word
	curLen := 0
	for _, w := range run {
		wl := len(strings.TrimSpace(w.Word))
		add := wl
		if len(cur) > 0 {
			add++ // joining space
		}
		if len(cur) > 0 && curLen+add > maxChars {
			out = append(out, cur)
			cur = nil
			curLen = 0
			add = wl
		}
		cur = append(cur, w)
		curLen += add
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// pushDanglers moves a line's trailing function word onto the next line so no line
// (except the last) ends on a/the/to/and... — Jordan's "keep phrases together".
// Never empties a line and never pushes a word past the cap.
func pushDanglers(chunks [][]Word, maxChars int) [][]Word {
	for i := 0; i+1 < len(chunks); i++ {
		for len(chunks[i]) > 1 && isDangler(chunks[i][len(chunks[i])-1].Word) {
			word := chunks[i][len(chunks[i])-1]
			if maxChars > 0 && lineLen(chunks[i+1])+1+len(strings.TrimSpace(word.Word)) > maxChars {
				break // would overflow the next line — leave it (the cap wins)
			}
			chunks[i] = chunks[i][:len(chunks[i])-1]
			chunks[i+1] = append([]Word{word}, chunks[i+1]...)
		}
	}
	return chunks
}

// Pass1Chunks is the deterministic recipe used everywhere pass-1 chunks are produced
// (Build's own chunking, and PlanChunks' pass-1/fallback). It is just ChunkWords.
func Pass1Chunks(words []Word, maxChars int, gapSeconds float64) [][]Word {
	return ChunkWords(words, maxChars, gapSeconds)
}

// RepairModelGroups is the LLM review pass's counterpart. The model may only suggest
// a word ORDER; it can never override the deterministic rules, so its grouping is
// flattened back to word order and re-chunked by ChunkWords. (The LLM review is off
// by default now — Jordan paused it — so on the normal path this is unused, but if it
// is ever re-enabled the rules still hold.)
func RepairModelGroups(groups [][]Word, maxChars int, gapSeconds float64) [][]Word {
	var flat []Word
	for _, g := range groups {
		flat = append(flat, g...)
	}
	return ChunkWords(flat, maxChars, gapSeconds)
}

// abs is a small integer helper shared with style.go.
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
