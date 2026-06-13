// cluster.go — the clustering algorithms, in pure Go (deterministic + testable).
//
// becky already computes the embeddings (CAM++ voice, ArcFace 512-d face) and the
// cosine math (cmd/identify/vec.go). Clustering is a post-processing layer over
// those vectors, so per SPEC-PERSON-CLUSTERING §4c the math stays in Go where becky
// already does it — no new Python/scikit-learn dependency, fully offline. The code
// is dimension-AGNOSTIC: it clusters whatever dim the helpers emit (the deployed
// CAM++ model 3dspeaker_..._voxceleb_16k.onnx outputs 512-d on this machine, not
// the 192-d the spec assumed — clustering is unaffected as long as all voice
// vectors share one space, which they do).
//
// Two algorithms, matched to the modality per the spec:
//
//   - VOICE  -> agglomerative (average-linkage, cosine STOP-threshold). Merge the
//     highest-similarity pair of clusters until no pair's average inter-cluster
//     cosine clears --voice-edge. The threshold (not a target count) decides where
//     to stop, so an unknown number of speakers is discovered. becky's measured
//     voice margin (same ~0.84 vs different ~0.03) makes this very clean.
//   - FACE   -> Chinese Whispers (graph label propagation). Each node starts in its
//     own class; an edge links two faces whose cosine >= --face-edge; iterate,
//     each node adopting the strongest-weighted label among its neighbors. Reported
//     as the best performer on InsightFace/ArcFace embeddings vs DBSCAN/HDBSCAN.
//
// Both are precision-leaning by design (SPEC §8): a false MERGE of two different
// strangers is the dangerous error (it attributes one person's appearances to
// another), so thresholds are stricter than becky-identify's recall-first matching
// thresholds, and low-cohesion clusters are surfaced for extra human scrutiny.
package main

import (
	"math"
	"sort"
)

// appearance is one embedded sighting of a (so-far unknown) person: a vector plus
// the provenance that lets a cluster point back to "clip X at time T". Vectors are
// L2-normalized on load so cosine == dot product (matches becky's convention).
type appearance struct {
	ID           string    // deterministic appearance id (sha12(source)+":"+modality+":"+frame)
	Modality     string    // "voice" | "face"
	SourceFile   string    // originating clip path
	SourceSHA256 string    // provenance hash of the source
	Timestamp    float64   // seconds into the clip (voice: span start; face: frame time)
	FrameIndex   int       // frame index (face) or speaker-ordinal (voice)
	SpeakerID    string    // diarized speaker label for voice appearances (e.g. SPEAKER_00)
	DetScore     float64   // detector confidence (face det_score; 1.0 for voice)
	Vector       []float64 // L2-normalized embedding
}

// agglomerative clusters appearances by average-linkage with a cosine STOP
// threshold: repeatedly merge the two clusters whose mean pairwise cosine is
// highest, stopping when that best similarity drops below edge. Returns a slice of
// member-index groups (indices into apps). Deterministic: ties broken by lowest
// index, and the merged-pair search scans in a fixed order.
//
// O(n^3) worst case via the classic "recompute best pair each round" loop, which
// is fine for the becky batch scale (clusters over a corpus of clips, not frames),
// and keeps the code obviously correct rather than clever.
func agglomerative(apps []appearance, edge float64) [][]int {
	n := len(apps)
	if n == 0 {
		return nil
	}
	// Precompute the full cosine similarity matrix once.
	sim := similarityMatrix(apps)

	// Each cluster is a set of member indices; start with singletons.
	clusters := make([][]int, n)
	for i := range clusters {
		clusters[i] = []int{i}
	}

	for len(clusters) > 1 {
		ai, bi, best := bestPair(clusters, sim)
		if ai < 0 || best < edge {
			break // no pair is similar enough to merge: stop
		}
		// Merge bi into ai (keep ai's slot, drop bi). ai < bi always (see bestPair).
		clusters[ai] = append(clusters[ai], clusters[bi]...)
		clusters = append(clusters[:bi], clusters[bi+1:]...)
	}

	for _, c := range clusters {
		sort.Ints(c)
	}
	return clusters
}

// bestPair returns the indices (a<b) of the two clusters with the highest average
// inter-cluster cosine, plus that similarity. Returns (-1,-1,-1) if <2 clusters.
// Average linkage: the score is the mean cosine over all cross-cluster member
// pairs — robust to a single outlier member vs single-linkage's chaining.
func bestPair(clusters [][]int, sim [][]float64) (int, int, float64) {
	bestA, bestB := -1, -1
	bestSim := math.Inf(-1)
	for a := 0; a < len(clusters); a++ {
		for b := a + 1; b < len(clusters); b++ {
			s := avgLinkage(clusters[a], clusters[b], sim)
			if s > bestSim {
				bestSim, bestA, bestB = s, a, b
			}
		}
	}
	return bestA, bestB, bestSim
}

// avgLinkage returns the mean cosine over every cross pair (i in ca, j in cb).
func avgLinkage(ca, cb []int, sim [][]float64) float64 {
	var total float64
	for _, i := range ca {
		for _, j := range cb {
			total += sim[i][j]
		}
	}
	return total / float64(len(ca)*len(cb))
}

// chineseWhispers partitions appearances by graph label propagation (SPEC §4a):
// build a graph whose edges link faces with cosine >= edge (weighted by cosine),
// then iterate — each node adopts the label with the greatest summed edge weight
// among its neighbors. Converges to communities; naturally discovers the cluster
// count. Deterministic here: nodes are visited in index order (no randomization)
// and label ties break to the lowest label id, so repeated runs are identical
// (the canonical algorithm shuffles; we trade that for reproducible forensics).
func chineseWhispers(apps []appearance, edge float64, maxIter int) [][]int {
	n := len(apps)
	if n == 0 {
		return nil
	}
	sim := similarityMatrix(apps)

	// Adjacency: neighbors[i] = indices j with sim>=edge, weights parallel.
	neighbors := make([][]int, n)
	weights := make([][]float64, n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i != j && sim[i][j] >= edge {
				neighbors[i] = append(neighbors[i], j)
				weights[i] = append(weights[i], sim[i][j])
			}
		}
	}

	labels := make([]int, n)
	for i := range labels {
		labels[i] = i // every node its own class initially
	}
	if maxIter <= 0 {
		maxIter = 20
	}

	for iter := 0; iter < maxIter; iter++ {
		changed := false
		for i := 0; i < n; i++ {
			newLabel := strongestNeighborLabel(i, neighbors[i], weights[i], labels)
			if newLabel != labels[i] {
				labels[i] = newLabel
				changed = true
			}
		}
		if !changed {
			break // converged
		}
	}
	return groupByLabel(labels)
}

// strongestNeighborLabel returns the label carrying the greatest summed edge
// weight among node i's neighbors. With no neighbors, the node keeps its own
// label (a singleton). Ties break to the smallest label id for determinism.
func strongestNeighborLabel(i int, nbrs []int, w []float64, labels []int) int {
	if len(nbrs) == 0 {
		return labels[i]
	}
	score := map[int]float64{}
	for k, j := range nbrs {
		score[labels[j]] += w[k]
	}
	bestLabel := labels[i]
	bestScore := math.Inf(-1)
	for lbl, s := range score {
		if s > bestScore || (s == bestScore && lbl < bestLabel) {
			bestScore, bestLabel = s, lbl
		}
	}
	return bestLabel
}

// groupByLabel turns a per-node label slice into sorted member-index groups,
// ordered by each group's smallest member index for stable output.
func groupByLabel(labels []int) [][]int {
	byLabel := map[int][]int{}
	for i, l := range labels {
		byLabel[l] = append(byLabel[l], i)
	}
	groups := make([][]int, 0, len(byLabel))
	for _, members := range byLabel {
		sort.Ints(members)
		groups = append(groups, members)
	}
	sort.Slice(groups, func(a, b int) bool { return groups[a][0] < groups[b][0] })
	return groups
}

// similarityMatrix precomputes the full pairwise cosine matrix (symmetric, 1.0 on
// the diagonal). Vectors are assumed L2-normalized; cosine falls back to full
// normalization defensively (see vec.go) so a stray non-unit vector can't corrupt
// the matrix.
func similarityMatrix(apps []appearance) [][]float64 {
	n := len(apps)
	sim := make([][]float64, n)
	for i := range sim {
		sim[i] = make([]float64, n)
		sim[i][i] = 1.0
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			s := cosine(apps[i].Vector, apps[j].Vector)
			sim[i][j] = s
			sim[j][i] = s
		}
	}
	return sim
}

// cohesion returns the mean intra-cluster cosine over all member pairs — the
// quality signal surfaced per cluster (SPEC output: "cohesion"). A singleton has
// cohesion 1.0 (no pairs to disagree). Low cohesion flags a cluster a human should
// scrutinize (it may be a loose merge).
func cohesion(members []int, sim [][]float64) float64 {
	if len(members) < 2 {
		return 1.0
	}
	var total float64
	var pairs int
	for a := 0; a < len(members); a++ {
		for b := a + 1; b < len(members); b++ {
			total += sim[members[a]][members[b]]
			pairs++
		}
	}
	if pairs == 0 {
		return 1.0
	}
	return total / float64(pairs)
}

// centroid returns the L2-normalized mean of a cluster's member vectors — used for
// the KB cross-check (compare a cluster's center against enrolled prints) and as a
// compact cluster fingerprint. Reuses averageNormalized (vec.go).
func centroid(members []int, apps []appearance) []float64 {
	vecs := make([][]float64, 0, len(members))
	for _, m := range members {
		vecs = append(vecs, apps[m].Vector)
	}
	return averageNormalized(vecs)
}
