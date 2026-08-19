package moment

import "sort"

// Overlap suppression: stop the tool returning ten re-cuts of the same moment.
//
// MEASURED on test-for-clips.mp4 (2026-08-19), the structural top ten was:
//
//	146.2-174.6  217.7-242.9  130.4-174.6  146.2-171.8  226.1-242.9
//	188.3-219.9  152.9-174.6  160.6-198.2  160.6-193.7  146.2-198.2
//
// Ten "moments" spanning exactly TWO regions of a five-minute video. Every
// window is a valid, well-formed thought, so nothing in the structural score
// could reject them — they are individually right and collectively useless.
// `--top 10` rendered ten shorts of the same two stories with slightly
// different in/out points, which is precisely the auto-clipper output Jordan
// rejected as slop.
//
// This is also the real shape of the "score saturation" reported in
// HANDOFF-SHORTS-PIPELINE.md §7.2. On his 177-transcript folder the top 4,000
// of 112,625 candidates all scored 0.985-1.000 — but a spread that narrow is
// what you EXPECT when thousands of those windows are shifted copies of each
// other. Widening the score range would have made duplicates look meaningfully
// ranked; removing them is the fix. Measured here, the prior is not saturated
// at all (min 0.640, max 0.982, none at the ceiling) — so "make the numbers
// spread out" was treating a symptom that does not exist on his own footage.
//
// The algorithm is ordinary non-maximum suppression, the same one object
// detectors use: walk the list best-first, keep a window, drop any later window
// that substantially repeats one already kept.

// DefaultMaxOverlap is the fraction of the SHORTER window that may already
// appear in a kept window before the later one counts as a repeat.
//
// Overlap-of-the-shorter, not IoU: IoU flatters a long window paired with a
// short one. 146.2-174.6 against 160.6-198.2 shares 14.0s — half the shorter
// clip, the same story told again — yet scores only 0.27 IoU and would survive.
// At 0.5 the rule reads plainly: if more than half of this clip is footage the
// viewer already saw in a better one, it is not a second moment.
//
// Kept as a knob (--max-overlap) rather than a constant because it is a taste
// call on real footage, and Jordan is the editor.
const DefaultMaxOverlap = 0.5

// overlapFrac is the shared duration as a fraction of the shorter window.
// Windows from different sources never overlap, whatever their timestamps say —
// two files that happen to share a timecode are not the same footage. Getting
// this wrong once already labelled moments with the wrong source video.
func overlapFrac(aSrc string, aStart, aEnd float64, bSrc string, bStart, bEnd float64) float64 {
	if aSrc != bSrc {
		return 0
	}
	lo := maxF(aStart, bStart)
	hi := minF(aEnd, bEnd)
	shared := hi - lo
	if shared <= 0 {
		return 0
	}
	shorter := minF(aEnd-aStart, bEnd-bStart)
	if shorter <= 0 {
		return 0
	}
	return shared / shorter
}

// keepDistinct returns the indices to keep, best-first, dropping any window
// that repeats more than maxOverlap of an already-kept one.
//
// order must already be best-first; this function does not decide what "best"
// means, because the answer differs before judging (structure) and after
// (the corroborated final score).
func keepDistinct(order []int, span func(i int) (src string, start, end float64), maxOverlap float64) []int {
	if maxOverlap <= 0 || maxOverlap >= 1 {
		return order
	}
	kept := make([]int, 0, len(order))
	for _, i := range order {
		si, ai, bi := span(i)
		dup := false
		for _, k := range kept {
			sk, ak, bk := span(k)
			if overlapFrac(si, ai, bi, sk, ak, bk) > maxOverlap {
				dup = true
				break
			}
		}
		if !dup {
			kept = append(kept, i)
		}
	}
	return kept
}

// Distinct drops candidates that substantially repeat a higher-scoring one,
// and returns what is left in transcript order (source, then time) so the
// caller's later handling is unchanged.
//
// Use this BEFORE choosing which candidates to send to the content judge: on a
// folder of streams the shortlist is otherwise spent on four hundred variants
// of the same handful of stories, and the judge cannot rank what it never saw.
func Distinct(cands []Candidate, maxOverlap float64) []Candidate {
	if len(cands) < 2 {
		return cands
	}
	order := make([]int, len(cands))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		ia, ib := order[a], order[b]
		if cands[ia].Score != cands[ib].Score {
			return cands[ia].Score > cands[ib].Score
		}
		// Deterministic tie-break: the earlier, then shorter window wins, so the
		// same transcript always yields the same picks.
		if cands[ia].Start != cands[ib].Start {
			return cands[ia].Start < cands[ib].Start
		}
		return cands[ia].End < cands[ib].End
	})

	kept := keepDistinct(order, func(i int) (string, float64, float64) {
		return cands[i].Source, cands[i].Start, cands[i].End
	}, maxOverlap)

	out := make([]Candidate, 0, len(kept))
	for _, i := range kept {
		out = append(out, cands[i])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		if out[i].Start != out[j].Start {
			return out[i].Start < out[j].Start
		}
		return out[i].End < out[j].End
	})
	return out
}

// DistinctRanked is Distinct for a ranked list. It preserves the ranking order
// it was given — Rank has already decided what "best" is, including the veto —
// so this only removes repeats.
func DistinctRanked(ranked []Ranked, maxOverlap float64) []Ranked {
	if len(ranked) < 2 {
		return ranked
	}
	order := make([]int, len(ranked))
	for i := range order {
		order[i] = i
	}
	kept := keepDistinct(order, func(i int) (string, float64, float64) {
		return ranked[i].Source, ranked[i].Start, ranked[i].End
	}, maxOverlap)

	out := make([]Ranked, 0, len(kept))
	for _, i := range kept {
		out = append(out, ranked[i])
	}
	return out
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
