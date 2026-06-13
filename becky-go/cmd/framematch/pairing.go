// pairing.go — pair frames ACROSS the two sources by low perceptual-hash
// (Hamming) distance, then rank the candidate pairs. Hamming distance mirrors
// internal/osintexport (the same metric becky-events/becky-identify use), so
// "looks alike" means the same thing across the toolset.
//
// Pairing is greedy 1:1 by ascending Hamming: every candidate (A,B) within
// --threshold is considered, the closest pairs are taken first, and each A-frame
// and each B-frame is used at most once. That yields ONE clear comparison per
// distinct scene instead of the same scene repeated across adjacent samples —
// which is exactly what a court-exhibit page set needs.
package main

import "sort"

// candidate is an intermediate (i,j) cross-source pair with its distance.
type candidate struct {
	ai, bi  int
	hamming int
}

// pairFrames returns the ranked candidate pairs between framesA and framesB
// whose Hamming distance is <= threshold, using greedy 1:1 selection (closest
// first). maxPairs caps the result (<=0 means no cap). Frame copies/exhibit
// images are NOT produced here — pairing is pure ranking over hashes.
func pairFrames(framesA, framesB []Frame, threshold, maxPairs int) []Pair {
	cands := make([]candidate, 0, len(framesA)*len(framesB))
	for i := range framesA {
		ha, badA := parseHash(framesA[i].Hash)
		if badA {
			continue
		}
		for j := range framesB {
			hb, badB := parseHash(framesB[j].Hash)
			if badB {
				continue
			}
			d := hamming64(ha, hb)
			if d <= threshold {
				cands = append(cands, candidate{ai: i, bi: j, hamming: d})
			}
		}
	}
	// Closest first; ties broken by source order for deterministic output.
	sort.SliceStable(cands, func(x, y int) bool {
		if cands[x].hamming != cands[y].hamming {
			return cands[x].hamming < cands[y].hamming
		}
		if cands[x].ai != cands[y].ai {
			return cands[x].ai < cands[y].ai
		}
		return cands[x].bi < cands[y].bi
	})

	usedA := make(map[int]bool)
	usedB := make(map[int]bool)
	pairs := make([]Pair, 0, len(cands))
	for _, c := range cands {
		if usedA[c.ai] || usedB[c.bi] {
			continue
		}
		usedA[c.ai] = true
		usedB[c.bi] = true
		pairs = append(pairs, Pair{
			Rank:          len(pairs) + 1,
			Hamming:       c.hamming,
			Similarity:    round3(1.0 - float64(c.hamming)/64.0),
			WhatToLookFor: whatToLookFor(c.hamming),
			A:             framesA[c.ai],
			B:             framesB[c.bi],
			Enhancements:  []Enhance{},
		})
		if maxPairs > 0 && len(pairs) >= maxPairs {
			break
		}
	}
	return pairs
}

// whatToLookFor returns a plain-language reviewer hint scaled by how close the
// match is. It points the eye at the shared structure to confirm; it never
// concludes "same place" (candidate-not-conclusion).
func whatToLookFor(hamming int) string {
	switch {
	case hamming <= 4:
		return "Very close visual match — compare fixed features (windows, vents, trim, outlets, " +
			"furniture placement, wall corners) to confirm it is the same room/object."
	case hamming <= 10:
		return "Strong layout match — line up the room's fixed structure (wall/ceiling lines, " +
			"window/door positions, fixtures) to confirm framing despite a different camera angle."
	default:
		return "Possible match — overall light/composition is similar; look for a SPECIFIC shared " +
			"feature (a vent, outlet, mark, or fixture) before treating this as a candidate."
	}
}

// parseHash converts a 16-char hex aHash into a uint64; returns (0,true) on a
// malformed hash so the caller can skip that frame.
func parseHash(hexStr string) (uint64, bool) {
	if len(hexStr) != 16 {
		return 0, true
	}
	var v uint64
	for _, c := range hexStr {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v |= uint64(c - '0')
		case c >= 'a' && c <= 'f':
			v |= uint64(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v |= uint64(c-'A') + 10
		default:
			return 0, true
		}
	}
	return v, false
}

// hamming64 counts differing bits between two 64-bit hashes (popcount of XOR),
// mirroring osintexport.HammingDistance but on already-parsed uint64s.
func hamming64(a, b uint64) int {
	x := a ^ b
	count := 0
	for x != 0 {
		x &= x - 1
		count++
	}
	return count
}
