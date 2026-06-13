package main

import (
	"math"
	"testing"

	"becky-go/internal/beckydb"
)

// nb is a tiny helper to build a Neighbor with an id, similarity and 1-based rank.
func nb(id string, sim float64, rank int) beckydb.Neighbor {
	return beckydb.Neighbor{
		Segment:    beckydb.Segment{SegmentID: id, Text: id},
		Similarity: sim,
		Rank:       rank,
	}
}

func TestMatchedLabel(t *testing.T) {
	cases := []struct {
		v, k bool
		want string
	}{
		{true, true, "hybrid"},
		{true, false, "vector"},
		{false, true, "keyword"},
		{false, false, "vector"},
	}
	for _, c := range cases {
		if got := matchedLabel(c.v, c.k); got != c.want {
			t.Errorf("matchedLabel(%v,%v) = %q, want %q", c.v, c.k, got, c.want)
		}
	}
}

func TestTagAll(t *testing.T) {
	info := tagAll([]beckydb.Neighbor{nb("a", 0.9, 1), nb("b", 0.8, 2)}, "keyword")
	if info["a"].matched != "keyword" || info["b"].matched != "keyword" {
		t.Errorf("tagAll did not tag both as keyword: %+v", info)
	}
	if info["a"].fused != 0 {
		t.Errorf("tagAll fused should be 0, got %v", info["a"].fused)
	}
}

// TestFuseRRF_Formula checks the RRF score math and the matched labels for a doc
// that hits one or both lists.
func TestFuseRRF_Formula(t *testing.T) {
	const rrfK = 60
	// "a" appears in BOTH lists (vector rank 2, keyword rank 1) -> hybrid.
	// "b" appears in vector only (rank 1) -> vector.
	// "c" appears in keyword only (rank 2) -> keyword.
	vec := []beckydb.Neighbor{nb("b", 0.95, 1), nb("a", 0.70, 2)}
	kw := []beckydb.Neighbor{nb("a", 0, 1), nb("c", 0, 2)}

	fused, info := fuseRRF(vec, kw, rrfK)

	wantScore := map[string]float64{
		"a": 1.0/float64(rrfK+2) + 1.0/float64(rrfK+1), // both lists
		"b": 1.0 / float64(rrfK+1),                     // vector only
		"c": 1.0 / float64(rrfK+2),                     // keyword only
	}
	for id, want := range wantScore {
		if math.Abs(info[id].fused-want) > 1e-12 {
			t.Errorf("fused[%s] = %v, want %v", id, info[id].fused, want)
		}
	}
	if info["a"].matched != "hybrid" {
		t.Errorf("a matched = %q, want hybrid", info["a"].matched)
	}
	if info["b"].matched != "vector" {
		t.Errorf("b matched = %q, want vector", info["b"].matched)
	}
	if info["c"].matched != "keyword" {
		t.Errorf("c matched = %q, want keyword", info["c"].matched)
	}

	// a (in both) must outrank b and c (each in one list). Order: a, then b, then c.
	if len(fused) != 3 {
		t.Fatalf("fused len = %d, want 3", len(fused))
	}
	if fused[0].SegmentID != "a" {
		t.Errorf("fused[0] = %q, want a (hit in both lists)", fused[0].SegmentID)
	}
	// b (1/61) > c (1/62), so b precedes c.
	if fused[1].SegmentID != "b" || fused[2].SegmentID != "c" {
		t.Errorf("fused tail = %q,%q want b,c", fused[1].SegmentID, fused[2].SegmentID)
	}

	// The fused "a" row should keep the VECTOR similarity (0.70), not the keyword 0.
	if math.Abs(fused[0].Similarity-0.70) > 1e-9 {
		t.Errorf("fused a similarity = %v, want 0.70 (vector row preferred)", fused[0].Similarity)
	}
}

// TestFuseRRF_KeywordOnlyWins proves the core forensic property: an exact-token
// segment that the vector list ranks LOW but the keyword list ranks #1 can be
// pulled to the top by fusion — the whole point of hybrid retrieval.
func TestFuseRRF_KeywordOnlyWins(t *testing.T) {
	const rrfK = 60
	// Vector list ranks the needle ("plate") dead last (rank 5); a generic
	// paraphrase row is rank 1. Keyword list ranks the needle #1.
	vec := []beckydb.Neighbor{
		nb("generic", 0.61, 1),
		nb("x1", 0.60, 2),
		nb("x2", 0.59, 3),
		nb("x3", 0.58, 4),
		nb("plate", 0.55, 5),
	}
	kw := []beckydb.Neighbor{nb("plate", 0, 1)}

	fused, info := fuseRRF(vec, kw, rrfK)
	if fused[0].SegmentID != "plate" {
		t.Fatalf("fused[0] = %q, want plate (keyword #1 + vector presence should win)", fused[0].SegmentID)
	}
	if info["plate"].matched != "hybrid" {
		t.Errorf("plate matched = %q, want hybrid", info["plate"].matched)
	}
}

// TestFuseRRF_Deterministic ensures equal-score docs sort stably (similarity
// desc, then id asc) so identical inputs always produce identical output.
func TestFuseRRF_Deterministic(t *testing.T) {
	const rrfK = 60
	// Two docs at the SAME rank in the same single list => identical RRF score;
	// tiebreak must be similarity desc then id asc.
	vec := []beckydb.Neighbor{nb("zeta", 0.5, 1), nb("alpha", 0.9, 1)}
	fused, _ := fuseRRF(vec, nil, rrfK)
	if fused[0].SegmentID != "alpha" {
		t.Errorf("tiebreak by similarity failed: fused[0] = %q, want alpha", fused[0].SegmentID)
	}

	// Equal score AND equal similarity => tiebreak by id asc.
	vec2 := []beckydb.Neighbor{nb("zeta", 0.5, 1), nb("beta", 0.5, 1)}
	fused2, _ := fuseRRF(vec2, nil, rrfK)
	if fused2[0].SegmentID != "beta" {
		t.Errorf("tiebreak by id failed: fused[0] = %q, want beta", fused2[0].SegmentID)
	}
}
