package main

import (
	"context"
	"strings"
	"testing"

	"becky-go/internal/systemone"
)

// fakeDecider says "restart" when the later line starts like the earlier one,
// and "finished" when a take ends with a period.
type fakeDecider struct{}

func (fakeDecider) Decide(_ context.Context, req systemone.Request) (systemone.Response, error) {
	out := systemone.Response{Answers: map[string]systemone.Answer{}}
	for k, q := range req.Questions {
		obj := q.Instructions.(map[string]any)
		p := 0.05
		if take, ok := obj["take"].(string); ok && strings.HasSuffix(take, ".") {
			p = 0.9
		}
		if e, ok := obj["earlier"].(string); ok && openingOverlap(e, obj["later"].(string)) == 1 {
			p = 0.9
		}
		if l, ok := obj["line"].(string); ok && containment(l, obj["kept"].(string)) > 0.7 {
			p = 0.9
		}
		out.Answers[k] = systemone.Answer{Type: "noul", Noul: p}
	}
	return out, nil
}

func words(spec ...string) []word {
	var ws []word
	t := 0.0
	for _, sentence := range spec {
		for _, w := range strings.Fields(sentence) {
			ws = append(ws, word{Word: w, Start: t, End: t + 0.3})
			t += 0.35
		}
		t += 1.2 // a pause longer than lineGap ends each line
	}
	return ws
}

func TestKeepsLastFinishedAttempt(t *testing.T) {
	ws := words("Hello there everyone.", "The best part is the price", "The best part is,", "The best part is you get ten percent off.", "That is all.")
	res, err := run(context.Background(), fakeDecider{}, ws, 6)
	if err != nil {
		t.Fatal(err)
	}
	keep := map[string]bool{}
	for _, l := range res.Lines {
		keep[l.Text] = l.Keep
	}
	if !keep["The best part is you get ten percent off."] || keep["The best part is,"] || !keep["Hello there everyone."] || !keep["That is all."] {
		t.Fatalf("wrong picks: %+v", keep)
	}
	if res.RetakeGroups != 1 {
		t.Fatalf("retake groups = %d, want 1", res.RetakeGroups)
	}
}

func TestSplitsStutterInsideOneLine(t *testing.T) {
	lines := splitLines(words("Do not buy one of the Do not buy one of the AI robots."))
	if len(lines) != 2 || lines[1].Text != "Do not buy one of the AI robots." {
		t.Fatalf("lines: %+v", lines)
	}
}

func TestKeepsAsideInsideRedonePassage(t *testing.T) {
	ws := words("But don't get one yet.", "I did the research for you.", "But don't get one yet, you need the right one.")
	res, err := run(context.Background(), fakeDecider{}, ws, 6)
	if err != nil {
		t.Fatal(err)
	}
	keep := map[string]bool{}
	for _, l := range res.Lines {
		keep[l.Text] = l.Keep
	}
	if !keep["I did the research for you."] || keep["But don't get one yet."] {
		t.Fatalf("wrong picks: %+v", keep)
	}
}

func TestOpeningOverlap(t *testing.T) {
	if got := openingOverlap("So the whole point", "So the whole point of this video"); got != 1 {
		t.Fatalf("got %v", got)
	}
	if got := openingOverlap("Totally different words", "So the whole point"); got != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestSplitLinesOnPunctuationAndPauses(t *testing.T) {
	ws := []word{{"Hi.", 0, 0.2}, {"So", 0.3, 0.4}, {"um", 0.5, 0.6}, {"next", 2.0, 2.2}, {"thing.", 2.3, 2.5}}
	lines := splitLines(ws)
	if len(lines) != 3 || lines[1].Text != "So um" || !lines[1].Noise {
		t.Fatalf("lines: %+v", lines)
	}
}
