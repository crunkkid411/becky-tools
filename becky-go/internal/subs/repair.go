package subs

import (
	"strings"
	"unicode"
)

// Deterministic phrase-integrity repair.
//
// Jordan's rule, in his words: `"can you post" / "ten times a day?" is correct
// because "ten" clearly is part of the subphrase "ten times a day" - if it's too
// many characters, then it should be; "can you post" / "ten times" / "a day?"`.
//
// A prompt asking a model to respect that is a hope, not a guarantee, and the
// model proved inconsistent about it on the same construction inside one file
// ("to post 10 times" / "a day" correct, "don't post 27" / "times a day" wrong).
// So the rule is enforced here in code, after any chunking — pass 1 or model —
// and the result is deterministic, which is what the rest of becky expects.
//
// The repair is one-directional and total: a line may never END on a word that
// governs the next line's first word. Such a word is pushed forward.

// danglingWords are function words that modify what FOLLOWS them, so a caption
// line ending on one splits a phrase.
//
// Deliberately narrow: only words that essentially never END an utterance.
// Verbs and pronouns were in this list once and it was wrong — "yeah you can"
// and "what it does" are complete, and treating "can"/"does" as danglers merged
// lines that read fine. When in doubt, leave a word out: a missed repair is a
// slightly awkward break, a wrong repair destroys a good one.
var danglingWords = map[string]bool{
	// Determiners.
	"a": true, "an": true, "the": true, "this": true, "these": true, "those": true,
	"my": true, "your": true, "his": true, "her": true, "its": true, "our": true,
	"their": true,
	// Conjunctions.
	"and": true, "or": true, "but": true, "if": true, "when": true, "while": true,
	"because": true, "than": true,
	// Prepositions.
	"of": true, "to": true, "in": true, "on": true, "at": true, "for": true,
	"with": true, "from": true, "by": true, "into": true, "onto": true, "about": true,
	"against": true,
	// Intensifiers and quantifiers that lead a noun phrase.
	"very": true, "really": true, "more": true, "most": true, "less": true,
	"least": true, "gotta": true, "gonna": true, "just": true, "every": true,
	"some": true, "any": true, "each": true, "another": true, "such": true,
}

// numberWords are spelled-out numbers. A number belongs with the unit it counts
// ("ten times", "twenty-seven times"), never stranded at the end of a line.
var numberWords = map[string]bool{
	"zero": true, "one": true, "two": true, "three": true, "four": true, "five": true,
	"six": true, "seven": true, "eight": true, "nine": true, "ten": true,
	"eleven": true, "twelve": true, "thirteen": true, "fourteen": true,
	"fifteen": true, "sixteen": true, "seventeen": true, "eighteen": true,
	"nineteen": true, "twenty": true, "thirty": true, "forty": true, "fifty": true,
	"sixty": true, "seventy": true, "eighty": true, "ninety": true,
	"hundred": true, "thousand": true, "million": true, "billion": true,
}

// isDangling reports whether a caption line ending on this word would split a
// phrase.
func isDangling(raw string) bool {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimRight(s, ".,;:!?\"')")
	s = strings.TrimLeft(s, "\"'(")
	if s == "" {
		return false
	}
	// Any word carrying a digit is a number: "27", "10x", "1st".
	for _, r := range s {
		if unicode.IsDigit(r) {
			return true
		}
	}
	// "twenty-seven" -> check the leading part too.
	if i := strings.Index(s, "-"); i > 0 {
		if numberWords[s[:i]] {
			return true
		}
	}
	return danglingWords[s] || numberWords[s]
}

// lineLen is the rendered length of a caption line.
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

// overflowSlack is how far past MaxChars a line may go to keep a phrase whole.
// Jordan: "A SHORTER line that is phrase-complete always beats a longer line
// that splits a phrase" — but the line still has to fit on screen, so the give
// is bounded to roughly one short word.
//
// ponytail: fixed slack, not a fitting algorithm. If captions start overflowing
// visibly, make this a flag rather than growing a line-breaker here.
const overflowSlack = 6

// isNumber reports whether the word is a count. A number NEVER gets separated
// from the unit it counts — that rule outranks the character cap, because
// "don't post 27" / "times a day" is exactly the break Jordan called out.
func isNumber(raw string) bool {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimRight(s, ".,;:!?\"')")
	for _, r := range s {
		if unicode.IsDigit(r) {
			return true
		}
	}
	if i := strings.Index(s, "-"); i > 0 && numberWords[s[:i]] {
		return true
	}
	return numberWords[s]
}

// isContentless reports whether the line is a LONE modifier — "a", "at",
// "more" — which cannot be repaired by pushing (that would empty the line) and
// so has to be folded into the next line instead.
//
// Deliberately restricted to a single word. Treating any all-function-word line
// as contentless swallows real lines: "at least ten" is every-word-dangling but
// the right fix is to push "ten" onto its unit, not to merge the whole line.
func isContentless(chunk []Word) bool {
	return len(chunk) == 1 && isDangling(chunk[0].Word)
}

// EnforceMaxChars splits any line longer than maxChars into readable pieces.
//
// The review model groups by MEANING, which is what makes captions read well -
// but it ignores the character cap and happily returns a 67-character line
// ("want to challenge this notion that in order to grow on social media"),
// which is unusable burned onto a vertical video. Jordan's own rule covers this
// exact case: `"can you post" / "ten times a day?" is correct ... if it's too
// many characters, then it should be; "can you post" / "ten times" / "a day?"`.
//
// So the model decides WHERE the thoughts are and this decides where a long
// thought has to break, filling greedily up to the cap and then handing the
// pieces to RepairDangling so no piece ends on a word that governs the next.
func EnforceMaxChars(chunks [][]Word, maxChars int) [][]Word {
	if maxChars <= 0 {
		return chunks
	}
	out := make([][]Word, 0, len(chunks))
	for _, chunk := range chunks {
		if lineLen(chunk) <= maxChars {
			out = append(out, chunk)
			continue
		}
		out = append(out, RepairDangling(splitAtBiggestPause(chunk, maxChars), maxChars)...)
	}
	return out
}

// splitAtBiggestPause breaks an over-long line at the point the SPEAKER paused
// longest, then recurses until every piece fits.
//
// Filling greedily to the cap instead would be wrong on Jordan's own rule -
// "A SHORTER line that is phrase-complete always beats a longer line that
// splits a phrase. Do NOT pad a line toward the character cap." Greedy fill
// produced "can you post ten times" | "a day?" (22 chars, splits the phrase);
// splitting on the pause produces "can you post" | "ten times a day?", which is
// the grouping he asked for.
//
// A split is only considered where the LEFT piece fits; if nothing fits (one
// enormous word run), it falls back to the most balanced point so the recursion
// always shrinks.
func splitAtBiggestPause(chunk []Word, maxChars int) [][]Word {
	if len(chunk) < 2 || lineLen(chunk) <= maxChars {
		return [][]Word{chunk}
	}

	best, bestGap := -1, -1.0
	fallback, bestBalance := 1, 1<<30
	for i := 1; i < len(chunk); i++ {
		left := lineLen(chunk[:i])

		// Most balanced point, used only if no split leaves a fitting left side.
		if d := abs(left - (lineLen(chunk) - left)); d < bestBalance {
			bestBalance, fallback = d, i
		}
		right := lineLen(chunk) - left
		// Both sides must carry real content. Without this the biggest pause is
		// often right after the first word, which strands "want" / "can" / "that"
		// on a line of their own - technically a pause, useless as a caption.
		if left > maxChars || left < minPiece(maxChars) || right < minPiece(maxChars) {
			continue
		}
		gap := chunk[i].Start - chunk[i-1].End
		if gap < 0 {
			gap = 0
		}
		// >= keeps the LATER of two equal pauses, which fills a line more fully
		// without ever exceeding the cap.
		if gap >= bestGap {
			bestGap, best = gap, i
		}
	}
	if best < 0 {
		best = fallback
	}

	out := splitAtBiggestPause(chunk[:best], maxChars)
	return append(out, splitAtBiggestPause(chunk[best:], maxChars)...)
}

// minPiece is the smallest a split piece may be. A third of the cap keeps
// lines substantial without forcing an unnatural break.
func minPiece(maxChars int) int { return maxChars / 3 }

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// rebalanceCapSplits fixes ChunkWords' one blind spot: it packs greedily with
// NO LOOKAHEAD, so the word that happens to land right after the cap is hit
// becomes its own line even when nothing about the speech justifies a break
// there. On Jordan's real edit this produced "to grow on social" / "media" -
// the combined line ("to grow on social media") is a single character over
// MaxChars, and the word left behind was never near a pause.
//
// Fix: whenever the line AFTER a cap-driven break (never a real pause - see
// the gap check below) is exactly one word, recombine it with the line
// before it. Two cases, both guarded so this can never make things worse:
//
//   - The line before does NOT end on a dangling word: an ordinary cap-driven
//     break with nothing else going on, so hand the pair to
//     splitAtBiggestPause, which picks a natural pause point instead of the
//     greedy cap boundary. If the re-split can't do any better than the
//     input - ANY piece still a lone word - the original chunks are kept.
//   - The line before DOES end on a dangling word (usually a number: "a
//     thousand" / "videos"): that is RepairDangling's case, and it already
//     has a proven fix (push the word forward). Racing it here goes wrong on
//     real data - re-splitting "compares it against" / "other" ahead of the
//     dangling-push picked the pause before "it" and produced "compares" /
//     "it against other", the SAME defect on a different word, when
//     RepairDangling on its own turns that pair into "compares it" /
//     "against other" correctly. So this case is left to RepairDangling
//     UNLESS RepairDangling's own guard would decline to push at all because
//     the leftover would itself be too short to stand ("a thousand" / "videos"
//     -> pushing "thousand" strands "a", so RepairDangling refuses and the
//     number never reaches its unit). Only then is the pair folded into one
//     line outright - not a split decision, so there is nothing to get wrong,
//     and it is exactly the number-stays-with-its-unit rule with nothing left
//     to strand.
//
// A break caused by an actual pause (gap > gapSeconds) is left untouched in
// both cases - that word "genuinely stands alone" (Jordan's rule) and merging
// across a real pause would trade a cosmetic problem for a timing one, which
// is worse.
func rebalanceCapSplits(chunks [][]Word, maxChars int, gapSeconds float64) [][]Word {
	if maxChars <= 0 || len(chunks) < 2 {
		return chunks
	}
	out := make([][]Word, 0, len(chunks))
	for i := 0; i < len(chunks); i++ {
		cur := chunks[i]
		if i+1 < len(chunks) && len(chunks[i+1]) == 1 && len(cur) >= 2 {
			next := chunks[i+1][0]
			last := strings.TrimSpace(cur[len(cur)-1].Word)
			gap := next.Start - cur[len(cur)-1].End
			if gap < 0 {
				gap = 0
			}
			switch {
			case gap > gapSeconds:
				// A real pause - not this bug, leave it.
			case isDangling(last):
				if lineLen(cur)-len(last)-1 < minPiece(maxChars) {
					// RepairDangling's own push-guard will decline (the
					// leftover would be too short to stand alone), so fold
					// the whole pair into one line.
					if merged := append(append([]Word{}, cur...), next); lineLen(merged) <= maxChars+overflowSlack {
						out = append(out, merged)
						i++
						continue
					}
				}
				// Otherwise RepairDangling handles this pair better than a
				// blind re-split would - leave it for that pass.
			default:
				combined := append(append([]Word{}, cur...), next)
				if pieces := splitAtBiggestPause(combined, maxChars); len(pieces) >= 2 && noLonePiece(pieces) {
					out = append(out, pieces...)
					i++ // the lone chunk was folded into pieces above
					continue
				}
			}
		}
		out = append(out, cur)
	}
	return out
}

// noLonePiece reports whether every piece has more than one word. A split
// that merely relocates the lone-word problem to a different word is not an
// improvement.
func noLonePiece(pieces [][]Word) bool {
	for _, p := range pieces {
		if len(p) < 2 {
			return false
		}
	}
	return true
}

// Pass1Chunks is the deterministic recipe used everywhere pass-1 chunks are
// produced (Build's own chunking, and PlanChunks' pass-1/fallback): pace-
// driven chunking, the cap-split rebalance above, then the phrase-integrity
// repairs. Centralised so the two call sites can't drift apart.
func Pass1Chunks(words []Word, maxChars int, gapSeconds float64) [][]Word {
	raw := rebalanceCapSplits(ChunkWords(words, maxChars, gapSeconds), maxChars, gapSeconds)
	return RepairDangling(raw, maxChars)
}

// RepairDangling pushes any phrase-splitting trailing word onto the next line,
// and merges away a line that is nothing but such words. It never changes word
// order and never drops a word, so the result is still a strict in-order
// partition of the input.
//
// The LAST line is left alone: it ends where the cut ends, so the phrase stops
// there because the editor stopped it, not because the chunking was wrong.
func RepairDangling(chunks [][]Word, maxChars int) [][]Word {
	if len(chunks) < 2 {
		return chunks
	}
	out := make([][]Word, 0, len(chunks))
	for _, c := range chunks {
		if len(c) > 0 {
			out = append(out, c)
		}
	}

	// mergeContentless folds a lone-modifier line into the line after it.
	// Walking backwards settles a run like "a" | "of" | "sand" at once.
	mergeContentless := func(in [][]Word) [][]Word {
		for i := len(in) - 2; i >= 0; i-- {
			if isContentless(in[i]) {
				merged := append(append([]Word{}, in[i]...), in[i+1]...)
				in = append(in[:i], in[i+1:]...)
				in[i] = merged
			}
		}
		return in
	}

	// pushTrailing moves a phrase-splitting last word onto the next line.
	pushTrailing := func(in [][]Word) [][]Word {
		for i := len(in) - 2; i >= 0; i-- {
			for len(in[i]) > 1 && isDangling(in[i][len(in[i])-1].Word) {
				word := in[i][len(in[i])-1]
				// Never strand what is left behind. Pushing "to" out of "want to"
				// leaves "want" alone on screen, which is worse than the dangle
				// it was fixing.
				if lineLen(in[i])-len(strings.TrimSpace(word.Word))-1 < minPiece(maxChars) {
					break
				}
				// A number moves regardless of length; anything else yields to
				// readability once the next line is already full.
				if !isNumber(word.Word) &&
					lineLen(in[i+1])+1+len(strings.TrimSpace(word.Word)) > maxChars+overflowSlack {
					break
				}
				in[i] = in[i][:len(in[i])-1]
				in[i+1] = append([]Word{word}, in[i+1]...)
			}
		}
		return in
	}

	// Merge, push, merge: pushing can leave a line contentless ("the a" -> "the"),
	// so one more merge settles it. Bounded and deterministic.
	out = mergeContentless(out)
	out = pushTrailing(out)
	return mergeContentless(out)
}
