package palantir

import "testing"

// obsCoOccur builds three clips containing both john and personA, plus one clip
// containing only john — so john~A co-occurs in exactly three shared clips.
func obsCoOccur() Ingest {
	in := Ingest{}
	add := func(clip, node, label string) {
		in.Observations = append(in.Observations, Observation{
			RowID: clip + ":" + node, Kind: KindPerson, NodeID: node, Label: label,
			Status: StatusDocumented, SourceFile: clip, Signal: "face", From: "identify.json",
		})
	}
	for _, c := range []string{"c1.mp4", "c2.mp4", "c3.mp4"} {
		add(c, "person:john", "John")
		add(c, "person:a", "Person A")
	}
	add("c4.mp4", "person:john", "John") // john alone — no edge contribution
	return in
}

func TestBuildCooccur_personPairWeightedByClips(t *testing.T) {
	nodes, edges := BuildCooccur(obsCoOccur())
	if len(nodes) != 2 {
		t.Fatalf("want 2 nodes, got %d: %+v", len(nodes), nodes)
	}
	var found *Edge
	for i := range edges {
		if edges[i].Kind == EdgeCoOccurrence {
			found = &edges[i]
		}
	}
	if found == nil {
		t.Fatal("no co_occurrence edge built")
	}
	if found.Weight != 3 {
		t.Errorf("weight = %d, want 3 (distinct shared clips)", found.Weight)
	}
	if found.Directed {
		t.Error("co_occurrence must be undirected")
	}
	for _, n := range nodes {
		if n.NodeID == "person:john" && n.DistinctSourceFiles != 4 {
			t.Errorf("john distinct files = %d, want 4", n.DistinctSourceFiles)
		}
	}
}

func TestBuildCooccur_deterministicAcrossOrder(t *testing.T) {
	in := obsCoOccur()
	_, e1 := BuildCooccur(in)
	rev := Ingest{}
	for i := len(in.Observations) - 1; i >= 0; i-- {
		rev.Observations = append(rev.Observations, in.Observations[i])
	}
	_, e2 := BuildCooccur(rev)
	if len(e1) != len(e2) {
		t.Fatalf("edge count differs: %d vs %d", len(e1), len(e2))
	}
	for i := range e1 {
		if e1[i].EdgeID != e2[i].EdgeID || e1[i].Weight != e2[i].Weight {
			t.Errorf("edge %d differs by order: %q(%d) vs %q(%d)",
				i, e1[i].EdgeID, e1[i].Weight, e2[i].EdgeID, e2[i].Weight)
		}
	}
}

func TestBuildCooccur_crossKindEdges(t *testing.T) {
	in := Ingest{Observations: []Observation{
		{RowID: "p", Kind: KindPerson, NodeID: "person:john", Label: "John", SourceFile: "c1.mp4", Signal: "face", From: "identify.json"},
		{RowID: "pl", Kind: KindPlace, NodeID: "place:gps:1.0_2.0", Label: "loc", SourceFile: "c1.mp4", Signal: "exif-gps", From: "osint"},
		{RowID: "d", Kind: KindDevice, NodeID: "device:iphone", Label: "iPhone", SourceFile: "c1.mp4", Signal: "exif-make-model", From: "osint"},
		{RowID: "ev", Kind: KindEvent, NodeID: "event:phone@1.0", Label: "phone", SourceFile: "c1.mp4", Signal: "events", From: "events.json"},
	}}
	_, edges := BuildCooccur(in)
	kinds := map[string]bool{}
	for _, e := range edges {
		kinds[e.Kind] = true
		if e.Kind != EdgeCoOccurrence && !e.Directed {
			t.Errorf("cross edge %q should be directed", e.EdgeID)
		}
	}
	for _, want := range []string{EdgeLocation, EdgeDevice, EdgeTimeline} {
		if !kinds[want] {
			t.Errorf("missing %q edge from person to that entity", want)
		}
	}
}
