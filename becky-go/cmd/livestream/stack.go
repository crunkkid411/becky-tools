package main

// stack.go - --model systemone-stack: the --model qwen workflow with System One
// put in it, everything else the same. Jordan, 2026-10-08: "duplicate the
// gemma|qwen|claude workflow, but insert the systemone step where appropriate
// and keep everything else the same... qwen was the most accurate of the three,
// so use that one."
//
// --model qwen: Qwen decides every sentence; Gemma re-decides the calls that
// need a second look (Qwen under 70% sure, a keep/cut boundary, a cut next to
// kept talk); where they disagree the line is kept and marked unsure.
// The stack adds System One (a decision model, a few cents a stream):
//  1. it decides every sentence too; every line where it disagrees with Qwen
//     also goes to Gemma (escalation - "low confidence means escalate");
//  2. where Qwen and Gemma still disagree, a System One call 80%+ sure settles
//     it (two of three agree). Only what all three leave open is marked unsure,
//     and the marker says what each of the three said.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const s1Sure = 80 // a System One call this sure settles a Qwen/Gemma split

func (r *run) stack(ss []Sentence, fresh bool) (Selection, error) {
	start := time.Now()
	gm, _, _ := r.cfg.GemmaAVLM()
	qm, _, _ := r.cfg.Qwen()
	gemma := localModelSpec{name: "gemma4", model: gm, server: r.cfg.LlamaServer}
	qwen := localModelSpec{name: "qwen3.5", model: qm, server: r.cfg.LlamaServer}

	sel, ds, reviews, err := r.qwenCalls(qwen, ss, fresh)
	if err != nil {
		return sel, err
	}
	s1, err := r.calls("systemone", localModelSpec{}, ss, fresh)
	if err != nil {
		return sel, fmt.Errorf("systemone could not decide: %w", err)
	}

	// Gemma looks at Qwen's usual review targets and every System One disagreement
	targets, extra := []int{}, 0
	for _, i := range reviewTargets(ds) {
		targets = append(targets, i)
	}
	in := map[int]bool{}
	for _, i := range targets {
		in[i] = true
	}
	for i := range ds {
		if !in[i] && s1.Decisions[i].Said != ds[i].Keep {
			targets, in[i] = append(targets, i), true
			extra++
		}
	}
	var todo []int
	for _, i := range targets {
		if _, done := reviews[ss[i].ID]; !done {
			todo = append(todo, i)
		}
	}
	r.logf("systemone-stack: Gemma checks %d calls (%d more because System One disagreed with Qwen); %d already checked in the qwen run",
		len(targets), extra, len(targets)-len(todo))
	if len(todo) > 0 {
		brief := "GUIDANCE: " + r.guidance + "\n\n" + wantedText(sel.Topics) + outlineText(sel.Outline)
		rm, rerr := gemma.open()
		var got map[int]Review
		if rerr == nil {
			got, rerr = rm.review(ss, windows(ss), brief, len(sel.Topics), todo, r.logf)
			rm.client.Close()
		}
		for id, rv := range got {
			reviews[id] = rv
		}
		if rerr != nil {
			sel.Notes = append(sel.Notes, fmt.Sprintf("the review by gemma4 did not finish (%v); unreviewed calls are marked unsure instead", rerr))
		}
	}
	sel.Model, sel.Reviewer = "systemone-stack", "gemma4 + systemone"
	sel.Decisions, sel.Rules = concludeStack(ds, reviews, s1.Decisions, "qwen3.5"), rulesVersion
	sel.Seconds = time.Since(start).Seconds()
	return sel, nil
}

// concludeStack is conclude() (the qwen rules, unchanged), then System One
// settles what is still unsure when it is sure and agrees with Qwen or Gemma.
func concludeStack(ds []Decision, reviews map[int]Review, s1 []Decision, lead string) []Decision {
	out := conclude(ds, reviews, lead)
	for i := range out {
		d, s := &out[i], s1[i]
		if !d.Unsure {
			continue
		}
		agrees := s.Said == d.Said || (d.Review != nil && d.Review.Keep == s.Said)
		if s.Confidence >= s1Sure && agrees {
			d.Keep, d.Unsure = s.Said, false
			d.Note += fmt.Sprintf(" - settled: systemone said %s (%d%% sure), two of three agree", verdict(s.Said), s.Confidence)
			continue
		}
		d.Note += fmt.Sprintf(" - systemone said %s (%d%% sure)", verdict(s.Said), s.Confidence)
	}
	return out
}

// qwenCalls is Qwen's own call on every sentence and Gemma's reviews of them:
// from a saved --model qwen run with the same guidance (its raw calls are kept
// in Said, its reviews in Review), else a new Qwen run (the review comes after).
func (r *run) qwenCalls(qwen localModelSpec, ss []Sentence, fresh bool) (Selection, []Decision, map[int]Review, error) {
	reviews := map[int]Review{}
	var s Selection
	b, err := os.ReadFile(filepath.Join(r.work, "selection-qwen3.5.json"))
	if err == nil && !fresh && json.Unmarshal(b, &s) == nil && s.Guidance == r.guidance && len(s.Decisions) == len(ss) && s.Rules >= 2 {
		r.logf("qwen3.5: using its earlier calls and Gemma's reviews (selection-qwen3.5.json)")
		ds := make([]Decision, len(s.Decisions))
		for i, d := range s.Decisions {
			if d.Review != nil {
				reviews[d.ID] = *d.Review
			}
			ds[i] = Decision{ID: d.ID, Label: d.Label, Topic: d.Topic, Keep: d.Said, Confidence: d.Confidence}
		}
		return s, ds, reviews, nil
	}
	sel, err := runLead(qwen, ss, r.guidance, r.logf)
	if err != nil {
		return sel, nil, nil, err
	}
	return sel, sel.Decisions, reviews, nil
}
