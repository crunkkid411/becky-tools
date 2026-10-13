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
	applyInstructions(ds, ss, []Instruction{in}) // the vote stands inside a moved part
	keep := []bool{}
	for _, d := range ds {
		keep = append(keep, d.Keep)
	}
	if !reflect.DeepEqual(keep, []bool{true, true, true, false, false, false}) {
		t.Fatalf("keep %v", keep)
	}
	// an anchor inside the part is refused
	reads[0].After, reads[0].Words = 4, "John kickflipped"
	if in := combine(reads[:1], ss); in.Applied {
		t.Fatalf("anchor inside the part was applied: %+v", in)
	}
}

// llama-server answers HTTP 400 to a bare schema as response_format (2026-10-12 run).
func TestInstructionSchemasAreWrapped(t *testing.T) {
	for _, s := range []map[string]any{findSchema, whatSchema, whereSchema} {
		if s["type"] != "json_schema" || s["json_schema"] == nil {
			t.Fatalf("schema not wrapped for llama-server: %v", s)
		}
	}
}

func TestBracketFillsPartBetweenRequests(t *testing.T) {
	in := Instruction{Request: []int{64, 80, 81, 83}, PartFirst: 66, PartLast: 70}
	bracket(&in)
	if in.PartFirst != 65 || in.PartLast != 79 {
		t.Fatalf("part %d-%d, want 65-79", in.PartFirst, in.PartLast)
	}
	in = Instruction{Request: []int{64}, PartFirst: 66, PartLast: 70} // asked only before: left alone
	bracket(&in)
	if in.PartFirst != 66 || in.PartLast != 70 {
		t.Fatalf("part %d-%d, want 66-70", in.PartFirst, in.PartLast)
	}
}

// "Oh wait, I forgot to mention." was cut by the voters as an instruction; the
// 12B read it as said to the viewers, so it goes back in (2026-10-13).
func TestClearedSentenceGoesBackIn(t *testing.T) {
	ss := []Sentence{{ID: 0, Text: "Oh wait, I forgot to mention."}, {ID: 1, Text: "Okay."}, {ID: 2, Text: "Cut that out."}}
	ds := []Decision{{ID: 0}, {ID: 1}, {ID: 2}}
	list := []Instruction{{Action: "none", Cleared: []int{0, 1}, Note: "gemma4-12b: none"}}
	applyInstructions(ds, ss, list)
	if !ds[0].Keep || ds[1].Keep || ds[2].Keep { // filler stays cut; unflagged untouched
		t.Fatalf("keep %v %v %v", ds[0].Keep, ds[1].Keep, ds[2].Keep)
	}
	if !reflect.DeepEqual(list[0].Restored, []int{0}) || list[0].lead() != "gemma4-12b" {
		t.Fatalf("restored %v lead %q", list[0].Restored, list[0].lead())
	}
}

func TestInformativeDropsSignalEverywhere(t *testing.T) {
	var cands []moment
	var flagged []int
	for i := 0; i < 30; i++ {
		s := []string{"face lost x20"}
		if i%10 == 0 {
			s = append(s, "Open Palm (MediaPipe gesture) x3")
		}
		cands = append(cands, moment{Signals: s})
		flagged = append(flagged, i)
	}
	watch, every := informative(cands, flagged)
	if !reflect.DeepEqual(watch, []int{0, 10, 20}) || !reflect.DeepEqual(every, []string{"\"face lost\""}) {
		t.Fatalf("watch %v everywhere %v", watch, every)
	}
	if w, _ := informative(cands[:5], flagged[:5]); len(w) != 5 { // too few stretches to judge
		t.Fatalf("small footage: watch %v", w)
	}
}
