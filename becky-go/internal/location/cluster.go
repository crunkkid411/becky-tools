// cluster.go — deterministic agglomerative clustering of clips into DISTINCT
// rooms, with the ≥MinSignals corroboration merge rule. No random init —
// determinism is an invariant (CLAUDE.md §2); ties break by clip index, mirroring
// pairing.go's stable sort.
package location

import (
	"fmt"
	"sort"
)

// Clip is one input video reduced to its primary room fingerprint plus
// provenance the engine carries through to the report. (Keyframe SAMPLING and the
// medoid selection happen in the CLI; the engine reasons over the primary
// fingerprint per clip.)
type Clip struct {
	Index       int
	Path        string
	SHA256      string
	Duration    float64
	KeyframeN   int
	Print       Fingerprint
	CaptureTime string // RFC3339 or "" — a dwelling signal (trusted vs mtime is the CLI's call)
	GPS         string // "lat,lon" or "" — a dwelling signal
	// Degraded, when set, means this clip could not be fingerprinted; it is
	// reported in degraded[] and excluded from clustering.
	Degraded string
}

// Room is a distinct-room cluster: the set of clips agreeing on ≥MinSignals.
type Room struct {
	ID       string
	Label    string
	Clips    []int   // clip indices, ascending
	Cohesion float64 // mean intra-cluster pairwise agreement fraction (0..1)
}

// WeakLink is a pair that shares a LONE signal — not enough to merge into one
// room (the ≥2-signal rule). It is surfaced for human review, never auto-merged.
type WeakLink struct {
	A, B   int
	Reason string
}

// PairScore is a fully-scored pair retained for the verdict + review sections.
type PairScore struct {
	A, B  int
	Score SignalScore
}

// ClusterResult is the output of the room-clustering step.
type ClusterResult struct {
	Rooms     []Room
	RoomOf    map[int]string // clip index → room id
	WeakLinks []WeakLink
	Pairs     []PairScore // every scored pair of non-degraded clips
}

// Cluster groups clips into rooms via single-link agglomerative merging, where an
// edge exists between two clips ONLY when fuse() reports ≥t.MinSignals agreeing
// independent signals. Pairs with exactly ONE agreeing signal become weak links
// (review_required), never merges. Deterministic: clips are processed in index
// order and union-find ties break by lowest index.
func Cluster(clips []Clip, t Thresholds) ClusterResult {
	if t.MinSignals < 1 {
		t.MinSignals = 1
	}
	res := ClusterResult{RoomOf: map[int]string{}}

	// Only non-degraded clips participate.
	active := make([]Clip, 0, len(clips))
	for _, c := range clips {
		if c.Degraded == "" {
			active = append(active, c)
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].Index < active[j].Index })

	n := len(active)
	if n == 0 {
		return res
	}

	// Union-find keyed by position in `active`.
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra == rb {
			return
		}
		// Always point the higher root at the lower so the canonical root is the
		// lowest clip index — deterministic membership.
		if active[ra].Index < active[rb].Index {
			parent[rb] = ra
		} else {
			parent[ra] = rb
		}
	}

	// Score every pair once (stable order), record merges + weak links.
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			score := fuse(active[i].Print, active[j].Print, t)
			res.Pairs = append(res.Pairs, PairScore{A: active[i].Index, B: active[j].Index, Score: score})
			switch {
			case score.Agreeing >= t.MinSignals:
				union(i, j)
			case score.Agreeing == 1:
				res.WeakLinks = append(res.WeakLinks, WeakLink{
					A:      active[i].Index,
					B:      active[j].Index,
					Reason: weakReason(score),
				})
			}
		}
	}

	// Collect clusters by canonical root, label by ascending lowest member index.
	members := map[int][]int{}
	for i := 0; i < n; i++ {
		r := find(i)
		members[r] = append(members[r], active[i].Index)
	}
	roots := make([]int, 0, len(members))
	for r := range members {
		roots = append(roots, r)
	}
	// Sort roots by their lowest member index for stable room numbering.
	sort.Slice(roots, func(x, y int) bool {
		return minIndex(members[roots[x]]) < minIndex(members[roots[y]])
	})

	for ri, r := range roots {
		idxs := members[r]
		sort.Ints(idxs)
		id := fmt.Sprintf("room-%d", ri+1)
		room := Room{
			ID:       id,
			Label:    fmt.Sprintf("Room %d", ri+1),
			Clips:    idxs,
			Cohesion: cohesion(idxs, active, t),
		}
		res.Rooms = append(res.Rooms, room)
		for _, ci := range idxs {
			res.RoomOf[ci] = id
		}
	}
	return res
}

// cohesion is the mean per-signal agreement fraction across all intra-cluster
// pairs (1.0 for a singleton). It feeds the per-clip room_confidence + the room
// cohesion field.
func cohesion(idxs []int, active []Clip, t Thresholds) float64 {
	if len(idxs) <= 1 {
		return 1.0
	}
	// Build a quick index→clip map within active.
	byIdx := map[int]Fingerprint{}
	for _, c := range active {
		byIdx[c.Index] = c.Print
	}
	var sum float64
	var pairs int
	for i := 0; i < len(idxs); i++ {
		for j := i + 1; j < len(idxs); j++ {
			s := fuse(byIdx[idxs[i]], byIdx[idxs[j]], t)
			if s.Available > 0 {
				sum += float64(s.Agreeing) / float64(s.Available)
			}
			pairs++
		}
	}
	if pairs == 0 {
		return 1.0
	}
	return round3(sum / float64(pairs))
}

func weakReason(s SignalScore) string {
	switch {
	case s.ColorAgrees && !s.DecorAgrees:
		return "color matches but decor hash disagrees (1 signal only)"
	case s.DecorAgrees && !s.ColorAgrees:
		return "decor hash matches but color disagrees (1 signal only)"
	case s.FeatureAgrees:
		return "features match but other signals disagree (1 signal only)"
	default:
		return "only one signal agrees — not enough to conclude same room"
	}
}

func minIndex(xs []int) int {
	m := xs[0]
	for _, v := range xs {
		if v < m {
			m = v
		}
	}
	return m
}

func round3(f float64) float64 {
	return float64(int(f*1000+0.5)) / 1000
}
