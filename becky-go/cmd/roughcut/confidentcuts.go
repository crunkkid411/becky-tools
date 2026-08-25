package main

// confidentcuts.go — the one exception to "a detector never cuts."
//
// Jordan, 2026-08-25 ~3am, after the LR-ASD speaking sweep finished and he
// measured the delivered cut against it himself: explicit, urgent, in-the-
// moment override of SKILL.md's standing "a detector is a signal, never a
// verdict" rule - he wants confirmed dead air actually removed, not just
// flagged, and said so directly ("FUCKING FIX THIS SHIT... work while I
// sleep"). This is scoped as narrowly as the override itself: ONLY spans
// where BOTH independent signals agree there is nothing there - LR-ASD sees
// no confident speaker AND the transcript has no real words either. A span
// where the transcript shows real words but LR-ASD's visual check is weak
// (he turned away from camera, walked off-frame while still talking, a
// cutaway with voiceover) is NOT touched here - that stays exactly what it
// was, a speakingCorroboration review marker, because losing legitimate
// speech in a criminal-case video is worse than leaving a few extra seconds
// of dead air on the timeline for him to trim by hand.

const (
	// speakingConfidentCutThreshold is an order of magnitude stricter than
	// speakingCorroborationThreshold's 0.35 review-marker bar (dossier.go) -
	// cutting needs far more confidence than flagging.
	speakingConfidentCutThreshold = 0.10
	// speakingConfidentCutMinOverlapSec avoids acting on a sliver of a merged
	// LR-ASD block that barely touches a keep.
	speakingConfidentCutMinOverlapSec = 2.0
	// speakingConfidentCutMaxWordSec: this much real transcript word-time (or
	// more) inside the candidate span blocks the cut - see the file header.
	speakingConfidentCutMaxWordSec = 0.5
	// maxPlausibleWordSec caps a single word's duration before it can block a
	// cut. Real data surprise (2026-08-25, found by this exact function
	// wrongly refusing a real cut): a meaningful fraction of Parakeet word
	// timestamps are corrupted by forced-alignment errors into implausibly
	// long spans (measured: "dude." reported as 11.12s, matching an earlier,
	// separately-found case of a 9.36s single word - HANDOFF-ROUGHCUT-2026-
	// 08-24-NIGHT.md 5.6). Uncapped, one bad timestamp fools the safety check
	// into thinking a long dead-air stretch is full of real speech. No real
	// single word runs this long; 3s is generous even for a dragged-out one.
	maxPlausibleWordSec = 3.0
)

// speakingConfidentCuts returns the sub-spans of keeps that BOTH LR-ASD (no
// confident visible speaker) and the transcript (no real words) agree have
// nothing in them - safe to actually remove, not just flag. Unlike
// speakingCorroboration, this walks EVERY overlapping speaking window per
// keep (not just the best-overlap one), so a keep that spans one confidently
// dead stretch and one confidently spoken stretch gets only the dead part
// cut, not an all-or-nothing verdict on the whole keep.
func speakingConfidentCuts(keeps []span, speaking []speakingWindow, words []span) []span {
	var cuts []span
	for _, k := range keeps {
		for _, w := range speaking {
			lo, hi := maxF(w.Start, k.Start), minF(w.End, k.End)
			overlap := hi - lo
			if overlap < speakingConfidentCutMinOverlapSec {
				continue
			}
			if w.BestFrac >= speakingConfidentCutThreshold {
				continue
			}
			if wordSecondsIn(words, lo, hi) >= speakingConfidentCutMaxWordSec {
				continue // real transcript content here - leave it for the review marker, don't auto-cut
			}
			cuts = append(cuts, span{lo, hi})
		}
	}
	return cuts
}

// wordSecondsIn sums how much of [lo,hi) is covered by real transcript words.
func wordSecondsIn(words []span, lo, hi float64) float64 {
	total := 0.0
	for _, w := range words {
		end := w.End
		if end > w.Start+maxPlausibleWordSec {
			end = w.Start + maxPlausibleWordSec
		}
		a, b := maxF(w.Start, lo), minF(end, hi)
		if b > a {
			total += b - a
		}
	}
	return total
}
