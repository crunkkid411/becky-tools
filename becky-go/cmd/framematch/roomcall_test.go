// roomcall_test.go — value-asserting tests for the hardened room-matching: the
// ROI-aware pairing, the pure-Go decor matcher, and the corroborate-then-conclude
// room call. Each test maps to a failure mode in SPEC-FRAMEMATCH-HARDENING.md §6
// and asserts SPECIFIC values (calls, distances, inlier counts), not truthiness.
package main

import (
	"image"
	"image/color"
	"testing"

	"becky-go/internal/config"
	"becky-go/internal/osintexport"
)

// --- Phase 2: pairing ranks on the ROI hash ----------------------------------

// TestPairFramesUsesROIHash — a pair that FAILS on the whole-frame hash (the
// body changed) but PASSES on the ROI hash (same ceiling) must still be surfaced,
// ranked by ROI distance. This is the false-negative fix end-to-end at the
// pairing layer.
func TestPairFramesUsesROIHash(t *testing.T) {
	// Whole-frame hashes are far apart (ham 64); ROI hashes are identical (ham 0).
	a := mkFrameROI("A", 0, "ffffffffffffffff", "00000000000000ff")
	b := mkFrameROI("B", 0, "0000000000000000", "00000000000000ff")

	pairs := pairFrames([]Frame{a}, []Frame{b}, 10, 0, testBandCfg(8), config.Config{})
	if len(pairs) != 1 {
		t.Fatalf("expected 1 ROI-matched pair (whole-frame would miss it), got %d", len(pairs))
	}
	if pairs[0].ROIHamming != 0 {
		t.Errorf("ROIHamming = %d, want 0 (identical ROI band)", pairs[0].ROIHamming)
	}
	if pairs[0].Hamming != 64 {
		t.Errorf("whole-frame Hamming = %d, want 64 (kept as the weak/provenance signal)", pairs[0].Hamming)
	}
}

// TestPairFramesUnknownROIHashFallsBackToWhole — when ROI hashes are absent the
// pairing falls back to the legacy whole-frame ranking and records ROIHamming=-1.
func TestPairFramesUnknownROIHashFallsBackToWhole(t *testing.T) {
	a := mkFrame("A", 0, "00000000000000ff") // no ROIHash
	b := mkFrame("B", 0, "00000000000000ff")
	pairs := pairFrames([]Frame{a}, []Frame{b}, 10, 0, testBandCfg(8), config.Config{})
	if len(pairs) != 1 {
		t.Fatalf("expected 1 pair on whole-frame fallback, got %d", len(pairs))
	}
	if pairs[0].ROIHamming != -1 {
		t.Errorf("ROIHamming = %d, want -1 (ROI unknown)", pairs[0].ROIHamming)
	}
	if pairs[0].Hamming != 0 {
		t.Errorf("whole-frame Hamming = %d, want 0", pairs[0].Hamming)
	}
}

// --- Phase 3: the pure-Go decor matcher --------------------------------------

// plantCorners draws n bright square "fixtures" on a dark background at the given
// cell positions, producing detectable corner keypoints.
func plantCorners(w, h int, cells [][2]int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	drawRect2(img, 0, 0, w, h, color.RGBA{30, 30, 30, 255})
	cw, ch := w/gridN, h/gridN
	if cw < 2 {
		cw = 2
	}
	if ch < 2 {
		ch = 2
	}
	for _, c := range cells {
		x0 := c[0] * cw
		y0 := c[1] * ch
		drawRect2(img, x0, y0, x0+cw, y0+ch, color.RGBA{240, 240, 240, 255})
	}
	return img
}

// TestDecorMatcherCountsSharedFeatures — shared planted corners → many inliers;
// disjoint corners → ~0 inliers. Asserts the counts directly.
func TestDecorMatcherCountsSharedFeatures(t *testing.T) {
	m := PureGoDecorMatcher{}
	shared := [][2]int{{2, 2}, {5, 4}, {8, 3}, {11, 6}, {13, 2}, {4, 9}, {9, 9}}
	a := plantCorners(256, 256, shared)
	b := plantCorners(256, 256, shared) // identical fixtures = same room

	inliers, pop := m.Match(a, b)
	if pop < len(shared) {
		t.Fatalf("population = %d, want >= %d detected keypoints", pop, len(shared))
	}
	if inliers < len(shared) {
		t.Errorf("inliers = %d, want >= %d (all shared fixtures should line up)", inliers, len(shared))
	}

	// Disjoint fixtures = different room → far fewer consistent matches than the
	// same-room case, and below the same-room agree threshold (minInliers=12).
	disjoint := [][2]int{{1, 1}, {3, 13}, {14, 14}, {7, 1}}
	c := plantCorners(256, 256, disjoint)
	inliers2, _ := m.Match(a, c)
	if inliers2 >= inliers/2 {
		t.Errorf("disjoint inliers = %d, want well below the shared count %d (different room)", inliers2, inliers)
	}
	if inliers2 >= 12 {
		t.Errorf("disjoint inliers = %d, must be below the agree threshold (minInliers=12)", inliers2)
	}
	t.Logf("shared inliers=%d (pop %d)  disjoint inliers=%d", inliers, pop, inliers2)
}

// TestDecorMatcherDeterministic — same inputs → identical counts (no randomness).
func TestDecorMatcherDeterministic(t *testing.T) {
	m := PureGoDecorMatcher{}
	cells := [][2]int{{2, 2}, {6, 5}, {10, 8}, {12, 3}}
	a := plantCorners(256, 256, cells)
	b := plantCorners(256, 256, cells)
	i1, p1 := m.Match(a, b)
	i2, p2 := m.Match(a, b)
	if i1 != i2 || p1 != p2 {
		t.Errorf("non-deterministic: (%d,%d) vs (%d,%d)", i1, p1, i2, p2)
	}
}

// --- Phase 4: corroborate-then-conclude room call ----------------------------

// TestRoomCall_TwoSignalsAgree_SameRoom — ROI agree + keypoints agree →
// same_room, two signals, confidence in the conclusion range.
func TestRoomCall_TwoSignalsAgree_SameRoom(t *testing.T) {
	in := roomInputs{
		roiHamming: 2, roiOK: true, roiFeatured: true, roiThreshold: 8,
		keypointsOn: true, keypointInliers: 18, keypointPop: 30, minInliers: 12,
		wholeHamming: 5, wholeThreshold: 10,
	}
	res := computeRoomCall(in)
	if res.call != callSameRoom {
		t.Fatalf("call = %q, want %q", res.call, callSameRoom)
	}
	if len(res.signalsUsed) != 2 {
		t.Errorf("signalsUsed = %v, want 2 signals", res.signalsUsed)
	}
	if res.confidence < 0.6 || res.confidence > 1.0 {
		t.Errorf("confidence = %v, want in [0.6,1.0] for a 2-signal conclusion", res.confidence)
	}
}

// TestRoomCall_LoneWeakSignal_NeverConcludes — the HEADLINE invariant. Only the
// whole-frame aHash "agrees" (ROI disagrees, keypoints off) → must be candidate,
// NEVER same_room. A lone weak signal can never conclude.
func TestRoomCall_LoneWeakSignal_NeverConcludes(t *testing.T) {
	in := roomInputs{
		// ROI clearly DISAGREES (far past threshold); whole-frame is tiny.
		roiHamming: 30, roiOK: true, roiFeatured: true, roiThreshold: 8,
		keypointsOn:  false,
		wholeHamming: 1, wholeThreshold: 10, // the weak signal "agrees"
	}
	res := computeRoomCall(in)
	if res.call == callSameRoom {
		t.Fatalf("call = %q, but a lone whole-frame signal must NEVER reach same_room", res.call)
	}
	if res.call != callCandidate {
		t.Errorf("call = %q, want %q (one strong signal disagrees, no corroboration)", res.call, callCandidate)
	}
}

// TestRoomCall_OneStrongAgrees_Candidate — only ROI agrees, keypoints off →
// candidate (one strong signal cannot conclude same_room).
func TestRoomCall_OneStrongAgrees_Candidate(t *testing.T) {
	in := roomInputs{
		roiHamming: 1, roiOK: true, roiFeatured: true, roiThreshold: 8,
		keypointsOn:  false,
		wholeHamming: 40, wholeThreshold: 10,
	}
	res := computeRoomCall(in)
	if res.call != callCandidate {
		t.Fatalf("call = %q, want %q (lone strong ROI signal)", res.call, callCandidate)
	}
	if res.confidence < 0.3 || res.confidence >= 0.6 {
		t.Errorf("candidate confidence = %v, want in [0.3,0.6)", res.confidence)
	}
}

// TestRoomCall_SignalsConflict_DifferentRoom — ROI disagrees + keypoints
// disagree → different_room (two signals agree it is NOT the same room).
func TestRoomCall_SignalsConflict_DifferentRoom(t *testing.T) {
	in := roomInputs{
		roiHamming: 40, roiOK: true, roiFeatured: true, roiThreshold: 8,
		keypointsOn: true, keypointInliers: 0, keypointPop: 25, minInliers: 12,
		wholeHamming: 3, wholeThreshold: 10,
	}
	res := computeRoomCall(in)
	if res.call != callDifferentRoom {
		t.Fatalf("call = %q, want %q (two signals disagree)", res.call, callDifferentRoom)
	}
}

// TestRoomCall_KeypointsOff_CappedAtCandidate — keypoints disabled, ROI agrees
// alone → candidate (the degrade path can never reach a conclusion).
func TestRoomCall_KeypointsOff_CappedAtCandidate(t *testing.T) {
	in := roomInputs{
		roiHamming: 0, roiOK: true, roiFeatured: true, roiThreshold: 8,
		keypointsOn:  false,
		wholeHamming: 0, wholeThreshold: 10,
	}
	res := computeRoomCall(in)
	if res.call != callCandidate {
		t.Errorf("call = %q, want %q (ROI alone, no second signal)", res.call, callCandidate)
	}
}

// TestRoomCall_FeaturelessROI_Unknown — a blank/uniform ROI (not featured) yields
// unknown, not a guess, even if the (meaningless) hashes match.
func TestRoomCall_FeaturelessROI_Unknown(t *testing.T) {
	in := roomInputs{
		roiHamming: 0, roiOK: true, roiFeatured: false, roiThreshold: 8,
		keypointsOn:  false,
		wholeHamming: 0, wholeThreshold: 10,
	}
	res := computeRoomCall(in)
	if res.call != callUnknown {
		t.Errorf("call = %q, want %q (featureless ROI must not be judged)", res.call, callUnknown)
	}
	if res.confidence != 0.0 {
		t.Errorf("unknown confidence = %v, want 0.0", res.confidence)
	}
}

// TestRoiFeaturedHex — a flat band hash (all-zero / all-F) reads as NOT featured;
// a mixed hash reads as featured.
func TestRoiFeaturedHex(t *testing.T) {
	c := testBandCfg(8)
	if c.roiFeaturedHex("0000000000000000") {
		t.Error("all-zero hash (flat band) must be unfeatured")
	}
	if c.roiFeaturedHex("ffffffffffffffff") {
		t.Error("all-ones hash (flat band) must be unfeatured")
	}
	if !c.roiFeaturedHex("00000000ffff00ff") {
		t.Error("a mixed hash must be featured")
	}
}

// TestBuildROIConfigValidates — invalid flag combinations are rejected (mirrors
// the existing fatal flag checks); valid band/corners/full resolve.
func TestBuildROIConfigValidates(t *testing.T) {
	bad := []struct {
		name                     string
		mode                     string
		top, height, left, width float64
		roiThreshold, minInliers int
	}{
		{"bad mode", "ceiling", 0, 0.35, 0, 1, 8, 12},
		{"roi-threshold too high", "band", 0, 0.35, 0, 1, 99, 12},
		{"negative min-inliers", "band", 0, 0.35, 0, 1, 8, -1},
		{"fraction out of range", "band", 0, 1.5, 0, 1, 8, 12},
		{"zero band height", "band", 0, 0, 0, 1, 8, 12},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			if _, err := buildROIConfig(c.mode, c.top, c.height, c.left, c.width, c.roiThreshold, false, c.minInliers); err == nil {
				t.Errorf("expected error for %s, got nil", c.name)
			}
		})
	}
	for _, mode := range []string{"band", "corners", "full"} {
		if _, err := buildROIConfig(mode, 0, 0.35, 0, 1, 8, false, 12); err != nil {
			t.Errorf("valid mode %q rejected: %v", mode, err)
		}
	}
}

// TestROIConfigSpecAndFullCompat — full mode hashes the whole frame (matches the
// legacy aHash bit for bit), and the band spec records the exact region.
func TestROIConfigSpecAndFullCompat(t *testing.T) {
	full, _ := buildROIConfig("full", 0, 1, 0, 1, 8, false, 12)
	img := ceilingPattern(80, 80, 28, color.RGBA{120, 90, 60, 255})
	drawRect2(img, 20, 40, 60, 70, color.RGBA{10, 10, 10, 255})
	if full.roiHashHex(img) != osintexport.HashHex(osintexport.AHashFromImage(img)) {
		t.Error("--roi full must reproduce the legacy whole-frame aHash exactly")
	}
	band, _ := buildROIConfig("band", 0, 0.35, 0, 1, 8, false, 12)
	if band.spec() != "band top=0.00 h=0.35 left=0.00 w=1.00" {
		t.Errorf("band spec = %q, unexpected", band.spec())
	}
}

// drawRect2 is a local fill helper (separate name to avoid clashing with the
// osintexport test's drawRect, which is in a different package).
func drawRect2(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

// ceilingPattern (framematch package copy) paints a structured top band for ROI
// tests that need a real image.
func ceilingPattern(w, h, bandH int, bg color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	drawRect2(img, 0, 0, w, h, bg)
	light := color.RGBA{210, 210, 210, 255}
	dark := color.RGBA{40, 40, 40, 255}
	cell := w / 8
	if cell < 1 {
		cell = 1
	}
	for x := 0; x < w; x++ {
		c := light
		if (x/cell)%2 == 0 {
			c = dark
		}
		for y := 0; y < bandH; y++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}
