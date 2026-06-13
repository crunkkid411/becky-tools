package main

import "testing"

// TestCategorize covers the candidate_* heuristics that tag OCR lines for triage.
// These are search HINTS (plainly "candidate_"), not conclusions, so the bar is
// "does it route the obvious cases right without over-claiming", not perfection.
func TestCategorize(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// Addresses: number + street + suffix (abbreviated or full).
		{"2601 Chatham Cir", catAddress},
		{"186 Surina Way", catAddress},
		{"1234 N Main Street", catAddress},
		// Timestamps: short, mostly the time/date.
		{"18:14:31", catTimestamp},
		{"5:17 PM", catTimestamp},
		{"7/4/2025", catTimestamp},
		// A long sentence that mentions a time stays plain text (not a timestamp).
		{"@williamcurbeam4088 was timed out for 1.8K seconds at 5:17", catText},
		// Plates: state-prefixed or a mixed alnum block.
		{"TX 7KZ123", catPlate},
		{"7KZ1234", catPlate},
		// A pure word or pure number is NOT a plate.
		{"ENTER", catText},
		{"12345", catText},
		// Business/signage: title-case / all-caps multiword, no terminal punctuation.
		{"GREENWOOD POLICE DEPARTMENT", catBusiness},
		{"Braxton's BBQ House", catBusiness},
		// A chat handle is not signage.
		{"@Kadeofspades ya cat is gonna be mine", catText},
		// A normal sentence is plain text.
		{"This user's messages will be hidden.", catText},
		{"", catText},
	}
	for _, c := range cases {
		if got := categorize(c.in); got != c.want {
			t.Errorf("categorize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestIsBusinessGuards checks the signage heuristic's negative guards directly.
func TestIsBusinessGuards(t *testing.T) {
	if isBusiness("hello there") { // lowercase -> not signage
		t.Error("lowercase phrase should not be business")
	}
	if isBusiness("Single") { // one word -> not signage
		t.Error("single word should not be business")
	}
	if isBusiness("One Two Three Four Five Six Seven") { // too many words
		t.Error("seven-word phrase should not be business")
	}
	if !isBusiness("Main Street Diner") {
		t.Error("title-case multiword should be business")
	}
}
