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

import (
	"image"
	"os"
	"sort"

	"becky-go/internal/config"
)

// candidate is an intermediate (i,j) cross-source pair with its distances.
// hamming is the ranking key (ROI-aHash when available, else whole-frame).
type candidate struct {
	ai, bi   int
	hamming  int // ranking distance (ROI primary)
	wholeHam int // whole-frame aHash distance (weak/provenance)
	roiHam   int // ROI-aHash distance (-1 if unknown/unparseable)
	roiOK    bool
}

// pairFrames returns the ranked candidate pairs between framesA and framesB,
// ranked on the ROI-aHash (the primary same-room signal) when available and
// falling back to the whole-frame hash otherwise. A pair is a candidate when its
// ROI distance is within --roi-threshold OR (legacy) its whole-frame distance is
// within --threshold, so a same-room pair the body-dominated whole-frame hash
// would miss is still surfaced. Greedy 1:1, closest first. maxPairs caps the
// result (<=0 means no cap). Each pair gets a corroborated room call. cfg is used
// only when keypoint corroboration is on (it re-decodes the two frames' ROIs).
func pairFrames(framesA, framesB []Frame, threshold, maxPairs int, roiCfg roiConfig, cfg config.Config) []Pair {
	cands := make([]candidate, 0, len(framesA)*len(framesB))
	for i := range framesA {
		ha, badA := parseHash(framesA[i].Hash)
		for j := range framesB {
			hb, badB := parseHash(framesB[j].Hash)
			wholeHam := 64
			if !badA && !badB {
				wholeHam = hamming64(ha, hb)
			}
			roiHam, roiOK := roiCfg.roiHammingOf(framesA[i].ROIHash, framesB[j].ROIHash)

			// Rank on ROI when we have it, else whole-frame.
			rank := wholeHam
			if roiOK {
				rank = roiHam
			}
			// Surface as a candidate if EITHER signal is within its threshold.
			roiPass := roiOK && roiHam <= roiCfg.roiThreshold
			wholePass := !badA && !badB && wholeHam <= threshold
			if roiPass || wholePass {
				cands = append(cands, candidate{
					ai: i, bi: j, hamming: rank,
					wholeHam: wholeHam, roiHam: roiHamOrNeg(roiHam, roiOK), roiOK: roiOK,
				})
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
		fa, fb := framesA[c.ai], framesB[c.bi]

		in := roomInputs{
			roiHamming:     c.roiHam,
			roiOK:          c.roiOK,
			roiFeatured:    roiCfg.roiFeaturedHex(fa.ROIHash) && roiCfg.roiFeaturedHex(fb.ROIHash),
			roiThreshold:   roiCfg.roiThreshold,
			wholeHamming:   c.wholeHam,
			wholeThreshold: threshold,
		}
		if roiCfg.keypoints {
			inliers, pop := keypointMatch(cfg, roiCfg, fa, fb)
			in.keypointsOn = true
			in.keypointInliers = inliers
			in.keypointPop = pop
			in.minInliers = roiCfg.minInliers
		}
		res := computeRoomCall(in)

		pairs = append(pairs, Pair{
			Rank:            len(pairs) + 1,
			Hamming:         c.wholeHam,
			Similarity:      round3(1.0 - float64(c.wholeHam)/64.0),
			WhatToLookFor:   whatToLookForCall(res, in, roiCfg.spec()),
			RoomCall:        res.call,
			RoomCallText:    roomCallPhrase(res),
			Confidence:      res.confidence,
			ROIHamming:      c.roiHam,
			KeypointInliers: in.keypointInliers,
			SignalsUsed:     signalsOrEmpty(res.signalsUsed),
			A:               fa,
			B:               fb,
			Enhancements:    []Enhance{},
		})
		if maxPairs > 0 && len(pairs) >= maxPairs {
			break
		}
	}
	return pairs
}

// roiHamOrNeg returns the ROI Hamming, or -1 when the ROI signal is unknown
// (unparseable hashes), so the manifest records "unknown" rather than a fake 0.
func roiHamOrNeg(roiHam int, ok bool) int {
	if !ok {
		return -1
	}
	return roiHam
}

// signalsOrEmpty ensures the JSON array is [] not null.
func signalsOrEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// keypointMatch re-decodes the two frames' ROIs and runs the decor matcher,
// returning (inliers, judgeable-population). Degrade-never-crash: any decode
// failure yields (0,0) → the keypoint signal becomes "unknown".
func keypointMatch(_ config.Config, roiCfg roiConfig, a, b Frame) (inliers, pop int) {
	if roiCfg.matcher == nil {
		return 0, 0
	}
	ia := decodeFrame(a.Path)
	ib := decodeFrame(b.Path)
	if ia == nil || ib == nil {
		return 0, 0
	}
	return roiCfg.matcher.Match(roiCfg.cropROI(ia), roiCfg.cropROI(ib))
}

// decodeFrame decodes a frame copy from disk, or nil on any error.
func decodeFrame(slashPath string) image.Image {
	f, err := os.Open(slashPath)
	if err != nil {
		return nil
	}
	defer f.Close()
	img, _, derr := image.Decode(f)
	if derr != nil {
		return nil
	}
	return img
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
