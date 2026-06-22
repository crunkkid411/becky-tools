// distance.go — per-signal distances + the fused corroboration rule. This is the
// corroboration mechanism in code: a pair is "same-room agreeing" only when ≥N
// independent signals fall under their thresholds (FORENSIC-OUTPUT-PHILOSOPHY.md
// ≥2-independent-signals rule; SPEC §2c). A lone signal is a weak link for human
// review, never an automatic merge.
package location

import "math/bits"

// Thresholds are the same-room cutoffs per signal. Defaults mirror the calibrated
// framematch values (SPEC §2c): Hamming 10 over 64 bits → ~0.84 similarity.
type Thresholds struct {
	DecorHamming int     // max masked-band aHash Hamming to count decor as agreeing (0-64)
	ColorChi2    float64 // max color chi-square distance to count color as agreeing (0-1)
	FeatureDist  float64 // max feature distance (1 - inlier_ratio) to count features as agreeing
	MinSignals   int     // independent agreeing signals required to MERGE (≥2 = the rule)
}

// DefaultThresholds returns the documented, overridable defaults (SPEC §3a).
func DefaultThresholds() Thresholds {
	return Thresholds{
		DecorHamming: 10,
		ColorChi2:    0.25,
		FeatureDist:  0.55, // ≥45% inliers counts as agreeing
		MinSignals:   2,
	}
}

// decorHamming counts differing bits between two masked-band aHashes (popcount of
// XOR), mirroring osintexport.HammingDistance.
func decorHamming(a, b uint64) int {
	return bits.OnesCount64(a ^ b)
}

// colorChi2 is the symmetric chi-square distance between two L1-normalized color
// histograms, scaled to roughly 0..1 for typical histograms. Returns -1 when
// either histogram is absent or their lengths differ (signal unavailable). The
// formula is sum( (p-q)^2 / (p+q) ) / 2, which is 0 for identical histograms and
// approaches 1 for fully disjoint ones.
func colorChi2(p, q []float64) float64 {
	if len(p) == 0 || len(q) == 0 || len(p) != len(q) {
		return -1
	}
	var sum float64
	for i := range p {
		d := p[i] - q[i]
		s := p[i] + q[i]
		if s > 0 {
			sum += (d * d) / s
		}
	}
	return sum / 2.0
}

// featureDistance converts a feature-match inlier ratio (0..1, fraction of
// descriptors that matched geometrically) into a distance (1 - ratio). A higher
// inlier ratio → smaller distance. The pairwise matching itself is the LOCAL cv2
// helper's job; this function is the engine-side conversion.
func featureDistance(inlierRatio float64) float64 {
	if inlierRatio < 0 {
		return 1
	}
	if inlierRatio > 1 {
		inlierRatio = 1
	}
	return 1 - inlierRatio
}

// SignalScore is the per-pair breakdown the fuse step produces.
type SignalScore struct {
	DecorHamming int     // bits differing on the masked decor hash
	ColorChi2    float64 // -1 when color is unavailable
	FeatureDist  float64 // -1 when features are unavailable
	// per-signal agreement flags (under threshold AND available)
	DecorAgrees   bool
	ColorAgrees   bool
	FeatureAgrees bool
	Available     int     // how many signals were present at all
	Agreeing      int     // how many present signals fell under threshold
	Dist          float64 // fused distance, 0..1 (lower = more alike)
}

// fuse computes the per-signal distances between two fingerprints and counts how
// many INDEPENDENT signals agree (fall under their threshold). The fused Dist is
// the mean of the available, normalized signal distances — used by the clustering
// step for ordering; the MERGE decision uses Agreeing vs MinSignals, never Dist
// alone (corroborate-then-conclude).
func fuse(a, b Fingerprint, t Thresholds) SignalScore {
	var s SignalScore

	// Decor hash is always available (a uint64; even an empty frame yields one).
	s.DecorHamming = decorHamming(a.DecorHash, b.DecorHash)
	s.DecorAgrees = s.DecorHamming <= t.DecorHamming
	s.Available++
	decorNorm := float64(s.DecorHamming) / 64.0

	// Color signal (available only when both have a histogram).
	s.ColorChi2 = colorChi2(a.ColorHist, b.ColorHist)
	colorAvail := s.ColorChi2 >= 0
	if colorAvail {
		s.ColorAgrees = s.ColorChi2 <= t.ColorChi2
		s.Available++
	}

	// Feature signal (available only when both carry descriptors). The engine
	// stores a precomputed inlier ratio in the descriptor blob's first 8 bytes
	// when the LOCAL helper provides it; in the pure-Go path Features is nil so
	// this signal is simply absent (the honest deterministic floor).
	if a.HasFeatures() && b.HasFeatures() {
		ratio := decodeInlierRatio(a.Features, b.Features)
		s.FeatureDist = featureDistance(ratio)
		s.FeatureAgrees = s.FeatureDist <= t.FeatureDist
		s.Available++
	} else {
		s.FeatureDist = -1
	}

	// Count agreeing signals and accumulate the fused distance over available ones.
	sum := decorNorm
	n := 1
	if s.DecorAgrees {
		s.Agreeing++
	}
	if colorAvail {
		sum += s.ColorChi2
		n++
		if s.ColorAgrees {
			s.Agreeing++
		}
	}
	if s.FeatureDist >= 0 {
		sum += s.FeatureDist
		n++
		if s.FeatureAgrees {
			s.Agreeing++
		}
	}
	s.Dist = sum / float64(n)
	return s
}

// decodeInlierRatio is the engine-side reader of the LOCAL feature helper's
// pairwise result. In the cloud build Features is nil and this is never reached;
// it exists so the local agent has a defined contract. The deterministic test
// path supplies descriptors whose first byte encodes a coarse inlier ratio so the
// fused-distance math can be exercised WITHOUT cv2 (see distance_test.go).
func decodeInlierRatio(a, b []byte) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	// Coarse deterministic stand-in: identical descriptor blobs → ratio 1.0;
	// otherwise the fraction of leading bytes that match. The real local helper
	// replaces this with a true ORB/AKAZE geometric inlier ratio.
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n == 0 {
		return 0
	}
	match := 0
	for i := 0; i < n; i++ {
		if a[i] == b[i] {
			match++
		}
	}
	return float64(match) / float64(n)
}
