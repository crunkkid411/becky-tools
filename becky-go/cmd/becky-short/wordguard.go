// wordguard.go — the tightening pass may never cut through a word.
//
// THE BUG THIS FIXES, in Jordan's words: "whatever you did to change the
// cut-times based on the audio energy absolutely did not work - it now cuts off
// words and makes the footage completely unusable."
//
// He is right, and the mechanism is planShotSpans + boundaryTighten. At every
// existing shot boundary, boundaryTighten looks for a becky-cut REMOVE (dead
// air) span within +/-0.4s and trims THAT SPAN'S WHOLE LENGTH, capped at 0.6s,
// then the caller splits it in half and takes half off the END of the earlier
// span and half off the START of the later one.
//
// Nothing in that checks WHERE the silence actually was. A 0.6s silence sitting
// entirely BEFORE the boundary still causes a 0.3s trim off the start of the
// span AFTER it — and that side is speech. 0.3s at Jordan's delivery is one to
// two words, sheared off the front of every shot. Across twenty boundaries that
// is the whole clip.
//
// wordrescue.go already established the principle for the raw-footage path: "a
// span that carries a word is not silence, whatever the level says." The
// already-edited path had no such guard at all, which is why the complaint
// arrived about the path he actually uses. Same rule, applied where the trim
// happens rather than after the fact.
//
// THE RULE: a span edge may move INTO silence as far as it likes, and may never
// move INTO a word. Applied per side and independently — the two sides of a
// boundary are different audio and there is no reason for them to trim equally.
package main

import "becky-go/internal/subs"

// wordEdgePad keeps a trimmed edge this far clear of the nearest word, so the
// word's own attack/release survives instead of being clipped to a stub. Same
// order as wordRescuePad (0.12) and becky-cut's own "0.04s,0.25s" margins.
const wordEdgePad = 0.06

// clampInToWords pulls a span's START back so it never lands inside (or too
// close to the front of) a word. want is where the tightening asked to put it,
// floor is the earliest it may sit (the untrimmed boundary). Returns the
// allowed start.
//
// Pure and total: no words, or no word straddling the edge, returns want
// unchanged, so a boundary in real silence still tightens by the full amount.
func clampInToWords(want, floor float64, words []subs.Word) float64 {
	if want <= floor {
		return floor
	}
	for _, w := range words {
		if w.End <= floor || w.Start >= want+wordEdgePad {
			continue // wholly outside the region the trim would eat
		}
		// This word starts before the trim would finish, so the trim would clip
		// its head (or land mid-word). Back off to just before the word.
		if s := w.Start - wordEdgePad; s < want {
			want = s
		}
	}
	if want < floor {
		return floor
	}
	return want
}

// clampOutToWords is clampInToWords for a span's END: push it forward so a
// trim never shears the tail off the last word. ceil is the latest it may sit.
func clampOutToWords(want, ceil float64, words []subs.Word) float64 {
	if want >= ceil {
		return ceil
	}
	for _, w := range words {
		if w.Start >= ceil || w.End <= want-wordEdgePad {
			continue
		}
		if e := w.End + wordEdgePad; e > want {
			want = e
		}
	}
	if want > ceil {
		return ceil
	}
	return want
}

// wordsCrossed counts how many words a proposed [in,out] would cut through —
// the honesty signal. Reported, never hidden: a non-zero count after clamping
// means the guard could not fully protect the edge and Jordan should see it.
func wordsCrossed(in, out float64, words []subs.Word) int {
	n := 0
	for _, w := range words {
		if w.Start < in && w.End > in {
			n++
			continue
		}
		if w.Start < out && w.End > out {
			n++
		}
	}
	return n
}
