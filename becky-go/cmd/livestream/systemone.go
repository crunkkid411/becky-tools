package main

// systemone.go - the content decision by a System One decision model
// (--model systemone; Jordan, 2026-10-08: "Livestream-Edit_System-One.bat").
// A decision model answers typed questions with probabilities instead of
// writing text, so becky asks two per sentence and applies the keep rule
// itself, exactly as for Gemma/Qwen: which kind of talk it is (the six labels)
// and whether it is part of what the guidance asks to keep. Calls go through
// the capped internal/systemone client ($5 a month, Perplexity Decider by
// default). A whole stream costs a few cents.

import (
	"context"
	"fmt"
	"math"
	"time"

	"becky-go/internal/systemone"
)

const s1Batch = 15 // sentences per request (two questions each)

var labelDesc = map[string]string{
	"narrative":  "Jordan talking about a topic (the real content)",
	"chat_reply": "reading or answering live chat, greeting or thanking viewers",
	"super_chat": "reading or thanking for a paid Super Chat, donation or membership",
	"break":      "stepping away, be right back, waiting, drinking, filler while nothing happens",
	"retake":     "a false start or broken sentence that he says again right after",
	"meta":       "about the stream itself: can you hear me, camera or audio problems, start or end housekeeping, like and subscribe",
}

func s1Questions(ss []Sentence, i int, guidance string) (systemone.Question, systemone.Question) {
	ctx := ""
	for j := max(0, i-contextCarry); j < i; j++ {
		ctx += ss[j].Text + " "
	}
	opts := make([]systemone.Option, len(labels))
	for k, l := range labels {
		opts[k] = systemone.Option{Key: l, Desc: labelDesc[l]}
	}
	kind := systemone.Choice(fmt.Sprintf(`Hair Jordan is livestreaming. Just before, he said: "%s". Then he said: "%s". What kind of talk is that last sentence?`, ctx, ss[i].Text), opts...)
	keep := systemone.NoulWith("`sentence` belongs in the edit: Jordan is talking about a topic the editor's `guidance` asks to keep - not reading or answering chat, thanking for a super chat, taking a break, starting a sentence he then says again, or doing stream housekeeping. A sentence counts while he is still on that topic, even when it does not name it (`before` is what he said just before).",
		map[string]any{"sentence": ss[i].Text, "before": ctx, "guidance": guidance})
	return kind, keep
}

// s1Decision turns the two answers into a Decision (Topic 1 = wanted, 0 = not).
// The keep question decides on its own: on the 27-livestream (2026-10-08) its
// answers were decisive (229 of 300 below 0.1 or above 0.9) while the six-way
// label spread out (top choice under 0.7 for 183 of 300), so taking the label
// into the keep rule or the confidence marked 185 of 300 sentences unsure.
// The label only names the sentence in the report and markers.
func s1Decision(id int, kind, keep systemone.Answer) Decision {
	d := Decision{ID: id, Label: kind.Choice}
	if !validLabel(d.Label) {
		d.Label = "narrative"
	}
	if d.Keep = keep.Noul >= 0.5; d.Keep {
		d.Topic, d.Label = 1, "narrative"
	}
	d.Confidence = int(math.Round(100 * math.Max(keep.Noul, 1-keep.Noul)))
	return d
}

func runSystemOne(dec systemone.Decider, ss []Sentence, guidance string, logf func(string, ...any)) (Selection, error) {
	start := time.Now()
	sel := Selection{Model: "systemone", Guidance: guidance, Topics: []string{guidance}}
	ds := make([]Decision, 0, len(ss))
	for b := 0; b < len(ss); b += s1Batch {
		e := min(b+s1Batch, len(ss))
		req := systemone.Request{State: "A livestream recording by Hair Jordan, a YouTuber, being edited down to the topics the editor asks for.",
			Questions: map[string]systemone.Question{}}
		for i := b; i < e; i++ {
			k, p := s1Questions(ss, i, guidance)
			req.Questions[fmt.Sprintf("k%d", i)], req.Questions[fmt.Sprintf("p%d", i)] = k, p
		}
		c, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		res, err := dec.Decide(c, req)
		cancel()
		if err != nil {
			return sel, fmt.Errorf("the decision model stopped at sentence %d of %d: %w", b+1, len(ss), err)
		}
		for i := b; i < e; i++ {
			ds = append(ds, s1Decision(ss[i].ID, res.Answers[fmt.Sprintf("k%d", i)], res.Answers[fmt.Sprintf("p%d", i)]))
		}
		logf("  systemone decided %d of %d sentences", e, len(ss))
	}
	sel.Decisions, sel.Rules = conclude(ds, nil, "systemone"), rulesVersion
	sel.Seconds = time.Since(start).Seconds()
	return sel, nil
}
