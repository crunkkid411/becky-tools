package main

import (
	"math"
	"testing"
)

// mkApp builds a test appearance with an L2-normalized vector for clustering.
func mkApp(id, modality, src string, vec []float64) appearance {
	return appearance{
		ID: id, Modality: modality, SourceFile: src,
		SourceSHA256: sha12(src), DetScore: 1.0, Vector: normalize(vec),
	}
}

// twoTightGroups returns two well-separated groups of unit vectors: indices 0,1,2
// near the +x axis (same person) and 3,4 near the +y axis (a different person).
func twoTightGroups() []appearance {
	return []appearance{
		mkApp("a0", "voice", "clipA.mp4", []float64{1.0, 0.02, 0}),
		mkApp("a1", "voice", "clipB.mp4", []float64{0.98, 0.04, 0}),
		mkApp("a2", "voice", "clipC.mp4", []float64{0.99, 0.0, 0.01}),
		mkApp("b0", "voice", "clipD.mp4", []float64{0.02, 1.0, 0}),
		mkApp("b1", "voice", "clipE.mp4", []float64{0.0, 0.99, 0.03}),
	}
}

func TestCosineKnownValues(t *testing.T) {
	orthogonal := cosine([]float64{1, 0, 0}, []float64{0, 1, 0})
	if math.Abs(orthogonal) > 1e-9 {
		t.Errorf("orthogonal cosine = %v, want 0", orthogonal)
	}
	identical := cosine(normalize([]float64{1, 1, 1}), normalize([]float64{1, 1, 1}))
	if math.Abs(identical-1.0) > 1e-9 {
		t.Errorf("identical cosine = %v, want 1", identical)
	}
	if cosine([]float64{1, 2}, []float64{1, 2, 3}) != 0 {
		t.Error("mismatched-length cosine should be 0")
	}
}

func TestAgglomerativeSplitsTwoPeople(t *testing.T) {
	apps := twoTightGroups()
	groups := agglomerative(apps, 0.65)
	if len(groups) != 2 {
		t.Fatalf("expected 2 clusters, got %d: %v", len(groups), groups)
	}
	// The first group (smallest min index) is the 3-member +x person.
	if len(groups[0]) != 3 || len(groups[1]) != 2 {
		t.Errorf("expected sizes [3,2], got [%d,%d]", len(groups[0]), len(groups[1]))
	}
	// Members 0,1,2 must be together (same person across 3 clips).
	if !sameSet(groups[0], []int{0, 1, 2}) {
		t.Errorf("group0 = %v, want {0,1,2}", groups[0])
	}
}

func TestAgglomerativeHighThresholdKeepsSeparate(t *testing.T) {
	// At an impossibly high edge, nothing merges: all singletons.
	apps := twoTightGroups()
	groups := agglomerative(apps, 0.999999)
	if len(groups) != len(apps) {
		t.Errorf("expected %d singletons at edge 0.999999, got %d", len(apps), len(groups))
	}
}

func TestAgglomerativeLowThresholdMergesAll(t *testing.T) {
	// At a very low edge, even the two people merge into one cluster.
	apps := twoTightGroups()
	groups := agglomerative(apps, -1.0)
	if len(groups) != 1 || len(groups[0]) != len(apps) {
		t.Errorf("expected 1 cluster of %d at edge -1.0, got %v", len(apps), groups)
	}
}

func TestChineseWhispersSplitsTwoPeople(t *testing.T) {
	apps := twoTightGroups()
	groups := chineseWhispers(apps, 0.65, 20)
	if len(groups) != 2 {
		t.Fatalf("expected 2 face communities, got %d: %v", len(groups), groups)
	}
	if !sameSet(groups[0], []int{0, 1, 2}) {
		t.Errorf("community0 = %v, want {0,1,2}", groups[0])
	}
}

func TestChineseWhispersDeterministic(t *testing.T) {
	apps := twoTightGroups()
	first := chineseWhispers(apps, 0.65, 20)
	for i := 0; i < 5; i++ {
		again := chineseWhispers(apps, 0.65, 20)
		if !equalGroups(first, again) {
			t.Fatalf("chineseWhispers not deterministic: %v vs %v", first, again)
		}
	}
}

func TestCohesionTightVsLoose(t *testing.T) {
	apps := twoTightGroups()
	sim := similarityMatrix(apps)
	tight := cohesion([]int{0, 1, 2}, sim) // same person
	loose := cohesion([]int{0, 3}, sim)    // two different people forced together
	if tight <= loose {
		t.Errorf("tight cohesion (%v) should exceed loose cohesion (%v)", tight, loose)
	}
	if tight < 0.9 {
		t.Errorf("same-person cohesion unexpectedly low: %v", tight)
	}
	if single := cohesion([]int{0}, sim); single != 1.0 {
		t.Errorf("singleton cohesion = %v, want 1.0", single)
	}
}

func TestCentroidIsNormalized(t *testing.T) {
	apps := twoTightGroups()
	c := centroid([]int{0, 1, 2}, apps)
	var sum float64
	for _, x := range c {
		sum += x * x
	}
	if math.Abs(math.Sqrt(sum)-1.0) > 1e-9 {
		t.Errorf("centroid not unit length: |c| = %v", math.Sqrt(sum))
	}
}

func TestLetterLabel(t *testing.T) {
	cases := map[int]string{0: "A", 1: "B", 25: "Z", 26: "AA", 27: "AB"}
	for n, want := range cases {
		if got := letterLabel(n); got != want {
			t.Errorf("letterLabel(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestAppearanceIDDeterministic(t *testing.T) {
	a := appearanceID("clipA.mp4", "voice", 0)
	b := appearanceID("clipA.mp4", "voice", 0)
	if a != b {
		t.Errorf("appearanceID not deterministic: %q vs %q", a, b)
	}
	if appearanceID("clipA.mp4", "face", 0) == a {
		t.Error("different modality should yield different id")
	}
}

func TestEmptyInputsDoNotPanic(t *testing.T) {
	if g := agglomerative(nil, 0.65); g != nil {
		t.Errorf("agglomerative(nil) = %v, want nil", g)
	}
	if g := chineseWhispers(nil, 0.65, 20); g != nil {
		t.Errorf("chineseWhispers(nil) = %v, want nil", g)
	}
}

// --- test helpers -----------------------------------------------------------

func sameSet(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[int]bool{}
	for _, x := range a {
		m[x] = true
	}
	for _, x := range b {
		if !m[x] {
			return false
		}
	}
	return true
}

func equalGroups(a, b [][]int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameSet(a[i], b[i]) {
			return false
		}
	}
	return true
}
