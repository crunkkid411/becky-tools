package main

import (
	"strings"
	"testing"
)

// System One's call reaches Qwen and Gemma as a note on each sentence line;
// the sentences themselves are not changed.
func TestStackShowsSystemOneOnEachLine(t *testing.T) {
	ss := []Sentence{{ID: 0, Text: "Calling someone bald is not an insult."}, {ID: 1, Text: "Thanks for the super chat."}}
	s1 := []Decision{{Said: true, Confidence: 93, Label: "narrative"}, {Said: false, Confidence: 88, Label: "super_chat"}}
	lines := sentenceLines(withS1(ss, s1), 0, 2)
	for _, want := range []string{"insult. [System One: belongs in the edit, 93%]", "chat. [System One: does not belong, 88%; looks like super_chat]"} {
		if !strings.Contains(strings.ReplaceAll(lines, "   [", " ["), want) {
			t.Errorf("lines miss %q:\n%s", want, lines)
		}
	}
	if ss[0].Hint != "" || strings.Contains(sentenceLines(ss, 0, 2), "System One") {
		t.Error("the original sentences got the note too")
	}
}
