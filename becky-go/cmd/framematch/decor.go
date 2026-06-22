// decor.go — the static-decor keypoint signal: the second INDEPENDENT signal in
// the room call. A DecorMatcher detects repeatable keypoints (corners) in a
// frame's ROI and counts how many line up between two frames. Many shared fixed
// features = strong same-room evidence that survives a camera-angle change (which
// aHash does not).
//
// The interface keeps the corroborate-then-conclude logic shippable NOW: the
// pure-Go default (deterministic, offline, cloud-buildable + testable) ships;
// a heavy gocv/OpenCV ORB implementation, if Jordan chooses it, plugs into the
// SAME interface as a documented LOCAL build step (cgo + native OpenCV cannot be
// built or run on the cloud agent). See SPEC-FRAMEMATCH-HARDENING.md §2.2/§7.
package main

import (
	"image"
	"sort"
)

// DecorMatcher detects + matches static-decor keypoints between two ROI images.
//   - Keypoints(roi) reports how many keypoints were detected in one ROI (used
//     for the per-frame count and to decide whether there is enough signal).
//   - Match(roiA, roiB) returns (inliers, keypoints): inliers is the count of
//     geometrically-consistent shared features; keypoints is the smaller of the
//     two detected counts (the judgeable population).
//
// Both methods are deterministic (fixed algorithm, fixed any-seed) and must
// never panic — a featureless or tiny ROI returns zeros, not an error.
type DecorMatcher interface {
	Keypoints(roi image.Image) int
	Match(roiA, roiB image.Image) (inliers, keypoints int)
}

// PureGoDecorMatcher is the dependency-free default: a FAST-style corner
// detector over an 8x8-cell luma grid of the ROI, matched by a translation-
// consistency vote. It is weaker than ORB+RANSAC but offline, deterministic, and
// cloud-testable; it is enough to act as a real second signal for corroboration.
type PureGoDecorMatcher struct{}

// gridN is the corner-sampling grid resolution over the ROI. A coarse grid keeps
// the detector deterministic and cheap while still capturing trim/vent/corner
// structure in the ceiling band.
const gridN = 16

// keypoint is a detected corner: its grid cell and a local descriptor used for
// matching. The descriptor is an 8-neighbour brighter-than-center bit pattern (a
// tiny BRIEF-style census), which distinguishes corner ORIENTATION (top-left vs
// bottom-right edges) so unrelated bright blobs don't all match each other.
type keypoint struct {
	gx, gy int
	sig    byte // 8-neighbour census descriptor
}

// lumaGrid samples the ROI into a gridN×gridN luma grid (center-of-cell, same
// spirit as the aHash sampler). Returns nil for an empty image.
func lumaGrid(roi image.Image) [][]int {
	b := roi.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < gridN || h < gridN {
		return nil
	}
	g := make([][]int, gridN)
	for gy := 0; gy < gridN; gy++ {
		g[gy] = make([]int, gridN)
		for gx := 0; gx < gridN; gx++ {
			sx := b.Min.X + (gx*w+w/2)/gridN
			sy := b.Min.Y + (gy*h+h/2)/gridN
			r, gg, bl, _ := roi.At(sx, sy).RGBA()
			g[gy][gx] = (299*int(r>>8) + 587*int(gg>>8) + 114*int(bl>>8)) / 1000
		}
	}
	return g
}

// detect finds corner-like cells: a cell whose luma differs strongly from the
// local 4-neighbour average is a candidate keypoint (a deterministic FAST-style
// test). The descriptor is the cell's luma quantized to 4 bits so two frames of
// the same fixture under similar exposure describe it the same way.
func detect(g [][]int) []keypoint {
	if g == nil {
		return nil
	}
	const contrast = 24 // luma delta that marks a corner/edge cell
	var kps []keypoint
	for gy := 1; gy < gridN-1; gy++ {
		for gx := 1; gx < gridN-1; gx++ {
			c := g[gy][gx]
			avg := (g[gy-1][gx] + g[gy+1][gx] + g[gy][gx-1] + g[gy][gx+1]) / 4
			d := c - avg
			if d < 0 {
				d = -d
			}
			if d >= contrast {
				kps = append(kps, keypoint{gx: gx, gy: gy, sig: census8(g, gx, gy)})
			}
		}
	}
	// Deterministic order.
	sort.Slice(kps, func(i, j int) bool {
		if kps[i].gy != kps[j].gy {
			return kps[i].gy < kps[j].gy
		}
		return kps[i].gx < kps[j].gx
	})
	return kps
}

// census8 builds an 8-neighbour census descriptor for cell (gx,gy): bit k is set
// when neighbour k is brighter than the center. This encodes the local edge
// orientation so a top-left corner and a bottom-right corner describe DIFFERENTLY
// even at the same brightness — the discriminator that stops unrelated bright
// blobs from matching.
func census8(g [][]int, gx, gy int) byte {
	c := g[gy][gx]
	offs := [8][2]int{{-1, -1}, {0, -1}, {1, -1}, {-1, 0}, {1, 0}, {-1, 1}, {0, 1}, {1, 1}}
	var sig byte
	for k, o := range offs {
		ny, nx := gy+o[1], gx+o[0]
		if ny < 0 || ny >= gridN || nx < 0 || nx >= gridN {
			continue
		}
		if g[ny][nx] > c {
			sig |= 1 << uint(k)
		}
	}
	return sig
}

// Keypoints reports the number of static-decor keypoints in one ROI.
func (PureGoDecorMatcher) Keypoints(roi image.Image) int {
	return len(detect(lumaGrid(roi)))
}

// Match counts geometrically-consistent shared keypoints between two ROIs. It
// pairs keypoints with the same descriptor, votes on the dominant (dx,dy)
// translation between matched pairs, and counts inliers consistent with that
// translation (a tolerant, deterministic stand-in for RANSAC homography). This
// rewards many fixed features that move together (a camera shift of the same
// room) and rejects coincidental single matches.
func (PureGoDecorMatcher) Match(roiA, roiB image.Image) (inliers, keypoints int) {
	ka := detect(lumaGrid(roiA))
	kb := detect(lumaGrid(roiB))
	keypoints = len(ka)
	if len(kb) < keypoints {
		keypoints = len(kb)
	}
	if keypoints == 0 {
		return 0, keypoints
	}

	// Candidate matches by identical descriptor; record their translation vote.
	type vec struct{ dx, dy int }
	votes := map[vec]int{}
	type match struct {
		ai, bi int
		v      vec
	}
	var matches []match
	for ai, a := range ka {
		for bi, b := range kb {
			if a.sig != b.sig {
				continue
			}
			v := vec{dx: b.gx - a.gx, dy: b.gy - a.gy}
			votes[v]++
			matches = append(matches, match{ai: ai, bi: bi, v: v})
		}
	}
	if len(matches) == 0 {
		return 0, keypoints
	}
	// Dominant translation (deterministic tie-break by smallest vector).
	best := vec{}
	bestN := -1
	keys := make([]vec, 0, len(votes))
	for v := range votes {
		keys = append(keys, v)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].dy != keys[j].dy {
			return keys[i].dy < keys[j].dy
		}
		return keys[i].dx < keys[j].dx
	})
	for _, v := range keys {
		if votes[v] > bestN {
			bestN = votes[v]
			best = v
		}
	}
	// Inliers: distinct keypoints whose match agrees with the dominant
	// translation within a 1-cell tolerance (mutual 1:1 by greedy first-use).
	usedA := map[int]bool{}
	usedB := map[int]bool{}
	for _, m := range matches {
		if usedA[m.ai] || usedB[m.bi] {
			continue
		}
		if m.v == best {
			usedA[m.ai] = true
			usedB[m.bi] = true
			inliers++
		}
	}
	return inliers, keypoints
}
