package main

import (
	"reflect"
	"testing"
)

func TestMovePiecesPutsPartAfterAnchor(t *testing.T) {
	ps := []span{{0, 10}, {12, 20}, {30, 40}, {50, 60}}
	// the part is source 30-40 (between pauses); it goes right after source 10
	got := movePieces(ps, 25, 45, 11, 30)
	want := []span{{0, 10}, {30, 40}, {12, 20}, {50, 60}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// a piece that straddles the part's edge and the anchor is split there
	got = movePieces([]span{{0, 20}, {30, 60}}, 30, 45, 10, 30)
	want = []span{{0, 10}, {30, 45}, {10, 20}, {45, 60}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("split: got %v, want %v", got, want)
	}
}

func TestCombineMajorityAndAnchorCheck(t *testing.T) {
	ss := []Sentence{{ID: 0, Text: "Hey everybody welcome back"}, {ID: 1, Text: "Today I quit YouTube"},
		{ID: 2, Text: "Here is why"}, {ID: 3, Text: "Oh I forgot to mention at the beginning"},
		{ID: 4, Text: "John kickflipped my car"}, {ID: 5, Text: "So that goes up front editor"}}
	reads := []modelRead{
		{Model: "gemma4-12b", Action: "move", Request: []int{3, 5}, PartFirst: 4, PartLast: 4, After: 0, Words: "Hey everybody welcome", Said: "move John part to the start"},
		{Model: "gemma4", Action: "move", Request: []int{3}, PartFirst: 4, PartLast: 5, After: 1, Words: "nonsense words here", Said: "x"},
		{Model: "qwen3.5", Action: "none"},
	}
	in := combine(reads, ss)
	if !in.Applied || in.Action != "move" || in.PartFirst != 4 || in.PartLast != 4 || in.After != 0 {
		t.Fatalf("got %+v", in)
	}
	if !reflect.DeepEqual(in.Models, []string{"gemma4-12b"}) { // gemma4's quote does not match its id
		t.Fatalf("models %v", in.Models)
	}
	ds := []Decision{{ID: 0, Keep: true}, {ID: 1, Keep: true}, {ID: 2, Keep: true}, {ID: 3, Keep: true}, {ID: 4, Keep: false}, {ID: 5, Keep: true}}
	applyInstructions(ds, ss, []Instruction{in})
	keep := []bool{}
	for _, d := range ds {
		keep = append(keep, d.Keep)
	}
	if !reflect.DeepEqual(keep, []bool{true, true, true, false, true, false}) {
		t.Fatalf("keep %v", keep)
	}
	// an anchor inside the part is refused
	reads[0].After, reads[0].Words = 4, "John kickflipped"
	if in := combine(reads[:1], ss); in.Applied {
		t.Fatalf("anchor inside the part was applied: %+v", in)
	}
}
