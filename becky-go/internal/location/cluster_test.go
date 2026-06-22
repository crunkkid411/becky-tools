package location

import (
	"sort"
	"testing"
)

// uniformHist returns a flat 64-bin histogram — a "neutral" color signal that is
// identical across clips so color always agrees, letting a test isolate the decor
// signal. (Two flat histograms have chi2 0 → color agrees.)
func uniformHist() []float64 {
	h := make([]float64, 64)
	for i := range h {
		h[i] = 1.0 / 64.0
	}
	return h
}

// distinctHist returns a histogram concentrated in one bin so two distinctHists
// with different bins are a DISAGREEING color signal.
func distinctHist(bin int) []float64 {
	h := make([]float64, 64)
	h[bin%64] = 1.0
	return h
}

// TestCluster_ThreeRooms: three synthetic groups whose decor hashes differ by ≤4
// within a group and ≥30 across groups, each group sharing a color palette →
// exactly 3 rooms with the expected membership (≥2 signals agree within a group).
func TestCluster_ThreeRooms(t *testing.T) {
	clips := []Clip{
		// Room A: hashes near 0x0, palette bin 0.
		{Index: 0, Print: Fingerprint{DecorHash: 0x0, ColorHist: distinctHist(0)}},
		{Index: 1, Print: Fingerprint{DecorHash: 0x3, ColorHist: distinctHist(0)}}, // hamming 2 from #0
		// Room B: hashes near 0xFF00, palette bin 10.
		{Index: 2, Print: Fingerprint{DecorHash: 0xFF00, ColorHist: distinctHist(10)}},
		{Index: 3, Print: Fingerprint{DecorHash: 0xFF03, ColorHist: distinctHist(10)}}, // hamming 2 from #2
		// Room C: hashes near 0xFFFF0000, palette bin 20.
		{Index: 4, Print: Fingerprint{DecorHash: 0xFFFF0000, ColorHist: distinctHist(20)}},
		{Index: 5, Print: Fingerprint{DecorHash: 0xFFFF0003, ColorHist: distinctHist(20)}}, // hamming 2 from #4
	}
	cr := Cluster(clips, DefaultThresholds())
	if len(cr.Rooms) != 3 {
		t.Fatalf("expected 3 rooms, got %d: %+v", len(cr.Rooms), cr.Rooms)
	}
	wantMembers := [][]int{{0, 1}, {2, 3}, {4, 5}}
	for i, room := range cr.Rooms {
		got := append([]int(nil), room.Clips...)
		sort.Ints(got)
		if !equalInts(got, wantMembers[i]) {
			t.Fatalf("room %d members = %v, want %v", i, got, wantMembers[i])
		}
	}
}

// TestCluster_BorderlineGoesToReview: a clip that shares ONLY a lone signal with
// another (similar color, disagreeing decor) must NOT auto-merge — it lands in
// review_required (the ≥2-signal rule).
func TestCluster_BorderlineGoesToReview(t *testing.T) {
	clips := []Clip{
		{Index: 0, Print: Fingerprint{DecorHash: 0x0, ColorHist: distinctHist(0)}},
		{Index: 1, Print: Fingerprint{DecorHash: 0x3, ColorHist: distinctHist(0)}}, // same room as #0
		// #2: SAME color palette as room-1 but a decor hash 64 bits away → lone signal.
		{Index: 2, Print: Fingerprint{DecorHash: 0xFFFFFFFFFFFFFFFF, ColorHist: distinctHist(0)}},
	}
	cr := Cluster(clips, DefaultThresholds())

	// #2 must be its OWN room (not merged with 0/1).
	if cr.RoomOf[2] == cr.RoomOf[0] {
		t.Fatalf("borderline clip #2 was wrongly merged into room of #0")
	}
	if len(cr.Rooms) != 2 {
		t.Fatalf("expected 2 rooms (0,1 together; 2 alone), got %d", len(cr.Rooms))
	}
	// A weak link between #2 and #0 (or #1) must be recorded for human review.
	foundWeak := false
	for _, w := range cr.WeakLinks {
		if w.A == 0 && w.B == 2 || w.A == 1 && w.B == 2 {
			foundWeak = true
		}
	}
	if !foundWeak {
		t.Fatalf("expected a weak link involving clip #2, got %+v", cr.WeakLinks)
	}
}

func TestCluster_Determinism(t *testing.T) {
	clips := []Clip{
		{Index: 0, Print: Fingerprint{DecorHash: 0x0, ColorHist: uniformHist()}},
		{Index: 1, Print: Fingerprint{DecorHash: 0x1, ColorHist: uniformHist()}},
		{Index: 2, Print: Fingerprint{DecorHash: 0xFFFF, ColorHist: distinctHist(5)}},
	}
	a := Cluster(clips, DefaultThresholds())
	b := Cluster(clips, DefaultThresholds())
	if len(a.Rooms) != len(b.Rooms) {
		t.Fatalf("non-deterministic room count: %d vs %d", len(a.Rooms), len(b.Rooms))
	}
	for i := range a.Rooms {
		if a.Rooms[i].ID != b.Rooms[i].ID || !equalInts(a.Rooms[i].Clips, b.Rooms[i].Clips) {
			t.Fatalf("non-deterministic clustering at room %d", i)
		}
	}
}

func TestCluster_DegradedExcluded(t *testing.T) {
	clips := []Clip{
		{Index: 0, Print: Fingerprint{DecorHash: 0x0, ColorHist: uniformHist()}},
		{Index: 1, Degraded: "no upright frames"},
		{Index: 2, Print: Fingerprint{DecorHash: 0x1, ColorHist: uniformHist()}},
	}
	cr := Cluster(clips, DefaultThresholds())
	if _, ok := cr.RoomOf[1]; ok {
		t.Fatalf("degraded clip #1 should not be assigned a room")
	}
	if len(cr.Rooms) != 1 {
		t.Fatalf("expected 0,2 to cluster into 1 room, got %d", len(cr.Rooms))
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
