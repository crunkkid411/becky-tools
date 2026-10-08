package main

import (
	"context"
	"strings"
	"testing"

	"becky-go/internal/systemone"
)

type fakeS1 struct{}

// fakeS1 calls every sentence containing "spider" wanted narrative, "chat" a chat reply.
func (fakeS1) Decide(_ context.Context, req systemone.Request) (systemone.Response, error) {
	res := systemone.Response{Answers: map[string]systemone.Answer{}}
	for k, q := range req.Questions {
		text := ""
		if s, ok := q.Instructions.(string); ok {
			text = s
		} else {
			text = q.Instructions.(map[string]any)["sentence"].(string)
		}
		switch k[0] {
		case 'k':
			l := "narrative"
			if strings.Contains(text, "chat") {
				l = "chat_reply"
			}
			res.Answers[k] = systemone.Answer{Choice: l, Probabilities: map[string]float64{l: 0.9}}
		case 'p':
			p := 0.1
			if strings.Contains(text, "spider") {
				p = 0.95
			}
			res.Answers[k] = systemone.Answer{Noul: p}
		}
	}
	return res, nil
}

func TestSystemOneAppliesKeepRule(t *testing.T) {
	ss := []Sentence{{ID: 0, Text: "The spider is huge."}, {ID: 1, Text: "Thanks chat for the hearts."}, {ID: 2, Text: "My cat ate lunch."}}
	sel, err := runSystemOne(fakeS1{}, ss, "keep the robot spider talk", func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	got := []bool{sel.Decisions[0].Keep, sel.Decisions[1].Keep, sel.Decisions[2].Keep}
	if got[0] != true || got[1] != false || got[2] != false || sel.Decisions[1].Label != "chat_reply" || sel.Decisions[0].Confidence != 95 {
		t.Fatalf("got keep %v, decisions %+v", got, sel.Decisions)
	}
}
