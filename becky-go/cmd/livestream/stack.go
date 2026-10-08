package main

// stack.go - --model systemone-stack: the --model qwen workflow with System One
// as one more specialist data point, everything else the same. Jordan,
// 2026-10-08: "systemone-stack should have the systemone model be its own
// separate pass BEFORE the qwen model - treat it just like the mediapipe,
// falcon perception, etc. It provides data to Qwen, then Qwen decides... Then
// gemma reviews just like in the other workflows - not voting".
//
// So: System One answers its two questions for every sentence first (what kind
// of talk; does it belong in the edit - capped Hosted client, a few cents).
// Its answer is printed after each sentence the models read, and the brief
// tells them to trust it unless the context shows a reason not to. Then
// runLocal(qwen, gemma) runs unchanged: Qwen decides, Gemma reviews the calls
// the workflow picks (reviewTargets), conclude() as for --model qwen.
// (--model systemone, together.go, is the voting design and stays separate.)

import "fmt"

// s1Data explains the System One notes to Qwen and Gemma.
const s1Data = `SPECIALIST NOTES: each sentence ends with [System One: ...]. System One is a specialised decision model that read each sentence (with the few before it) and the guidance, and measured how likely it is to belong in the edit, and if not, what kind of talk it looks like. Treat it like any specialist measurement: trust it unless the transcript around the sentence shows a reason not to. It does not see the whole stream; you do, and you make the call.

`

func (r *run) stack(ss []Sentence, fresh bool) (Selection, error) {
	s1, err := r.calls("systemone", localModelSpec{}, ss, fresh)
	if err != nil {
		return Selection{}, fmt.Errorf("systemone could not decide: %w", err)
	}
	r.logf("systemone: its call on every sentence goes to Qwen and Gemma as a specialist note")
	gm, _, _ := r.cfg.GemmaAVLM()
	qm, _, _ := r.cfg.Qwen()
	gemma := localModelSpec{name: "gemma4", model: gm, server: r.cfg.LlamaServer}
	qwen := localModelSpec{name: "qwen3.5", model: qm, server: r.cfg.LlamaServer}
	sel, err := runLocal(qwen, gemma, withS1(ss, s1.Decisions), r.guidance, s1Data, r.logf)
	if err != nil {
		return sel, err
	}
	sel.Model = "systemone-stack"
	for i := range sel.Decisions { // an unsure marker says what every model said
		if d := &sel.Decisions[i]; d.Unsure && i < len(s1.Decisions) {
			d.Note += " - " + s1Hint(s1.Decisions[i])
		}
	}
	return sel, nil
}

// withS1 is a copy of the sentences with System One's call as each one's hint.
func withS1(ss []Sentence, s1 []Decision) []Sentence {
	out := append([]Sentence(nil), ss...)
	for i := range out {
		if i < len(s1) {
			out[i].Hint = "   " + s1Hint(s1[i])
		}
	}
	return out
}

// s1Hint: "[System One: belongs in the edit, 93%]" or "[System One: does not
// belong, 88%; looks like chat_reply]". Said is its own keep call, Confidence
// how sure it is of that call.
func s1Hint(d Decision) string {
	if d.Said {
		return fmt.Sprintf("[System One: belongs in the edit, %d%%]", d.Confidence)
	}
	h := fmt.Sprintf("[System One: does not belong, %d%%", d.Confidence)
	if d.Label != "" && d.Label != "narrative" {
		h += "; looks like " + d.Label
	}
	return h + "]"
}
