package main

// reply.go - is Jordan ANSWERING chat, not only reading it? Jordan, 2026-10-08:
// "if a chat says "what hair dye do you use?" and within that approximate
// timeframe (or slightly delayed) I randomly say "I use Manic Panic" - that is
// a direct answer to a direct chat question, even though I did not read the
// question out loud". Words do not overlap there, so plain code cannot see it:
// becky builds the candidates (the messages that arrived shortly before each
// line he says) and a System One decision model picks which one, if any, the
// line answers. "hang on let me scroll up" widens the look-back.

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"becky-go/internal/systemone"
)

const (
	replyLook     = 90.0  // messages this long before a line are candidates
	scrollLook    = 300.0 // ...this long after he says "let me scroll up" and the like
	cueReach      = 30.0  // a scroll cue widens the look-back for lines this long after it
	minReplyLag   = 2.0   // a message must have arrived this long before the line
	maxOptions    = 25    // most recent messages offered per line
	maxCueOptions = 40    // ...after a scroll cue (older questions added)
	replySure     = 0.7   // the model's probability needed to call a line a reply
	replyBatch    = 20    // lines per decision request
	sentenceGap   = 1.2   // a pause this long ends one of his lines
	sentenceMax   = 40    // run-ons are split
)

var scrollCue = regexp.MustCompile(`(?i)\bscroll(ing)?\s+(back\s+)?up\b|\b(someone|somebody|who|you guys|you)\s+(just\s+)?asked\b|\b(in|the)\s+chat\b|\bchat\s+(is\s+)?(says|said|asking|asked)\b|\blet me (see|look|check|read)\b`)

type line struct {
	Start float64
	Text  string
	Cue   bool
}

// reply is one line Jordan said that answers a chat message.
type reply struct {
	At     float64 `json:"at"`   // when he said it, seconds into the stream
	Line   string  `json:"line"` // what he said
	Msg    int     `json:"msg"`  // index into messages
	Author string  `json:"author"`
	Chat   string  `json:"chat"`
	Lag    float64 `json:"lag"` // seconds from the message to his answer
	P      float64 `json:"p"`   // the decision model's probability
}

// lines groups the transcript into the sentences he says.
func lines(ws []word) []line {
	var out []line
	var cur []string
	start := 0.0
	flush := func() {
		if len(cur) > 0 {
			t := strings.Join(cur, " ")
			out = append(out, line{Start: start, Text: t, Cue: scrollCue.MatchString(t)})
		}
		cur = nil
	}
	for i, w := range ws {
		if len(cur) == 0 {
			start = w.Start
		}
		cur = append(cur, strings.TrimSpace(w.Word))
		end := strings.HasSuffix(w.Word, ".") || strings.HasSuffix(w.Word, "?") || strings.HasSuffix(w.Word, "!")
		if end || len(cur) >= sentenceMax || (i+1 < len(ws) && ws[i+1].Start-w.Start > sentenceGap) {
			flush()
		}
	}
	flush()
	return out
}

// candidates are the messages line i could be answering: the most recent ones
// from the last replyLook seconds; after a scroll cue, also the questions from
// the last scrollLook seconds.
func candidates(msgs []message, ls []line, i int) []int {
	at := ls[i].Start
	cue := false
	for j := i; j >= 0 && at-ls[j].Start <= cueReach; j-- {
		cue = cue || ls[j].Cue
	}
	hi := sort.Search(len(msgs), func(k int) bool { return msgs[k].T > at-minReplyLag })
	var recent, older []int
	for k := hi - 1; k >= 0 && msgs[k].T >= at-scrollLook; k-- {
		m := msgs[k]
		if (m.Kind != "text" && m.Kind != "superchat") || len(contentWords(m.Text)) < 2 {
			continue
		}
		if m.T >= at-replyLook {
			recent = append(recent, k)
		} else if cue && strings.Contains(m.Text, "?") {
			older = append(older, k)
		}
	}
	if len(recent) > maxOptions {
		// a busy chat pushes a question out of the newest 25: keep the questions
		// ("What's on your topic list?" was answered 39 s later, 27-livestream 2026-10-08)
		for _, k := range recent[maxOptions:] {
			if strings.Contains(msgs[k].Text, "?") {
				older = append([]int{k}, older...)
			}
		}
		recent = recent[:maxOptions]
		cue = cue || len(older) > 0
	}
	if !cue {
		return recent
	}
	out := append(recent, older...)
	if len(out) > maxCueOptions {
		out = out[:maxCueOptions]
	}
	return out
}

func replyQuestion(msgs []message, ls []line, i int, cands []int) systemone.Question {
	prev := ""
	if i > 0 {
		prev = ls[i-1].Text
	}
	opts := []systemone.Option{{Key: "none", Desc: "none of them - he is talking about his own topic, not answering chat"}}
	for _, k := range cands {
		opts = append(opts, systemone.Option{Key: fmt.Sprintf("m%d", k),
			Desc: fmt.Sprintf("%s wrote %.0fs earlier: %s", msgs[k].Author, ls[i].Start-msgs[k].T, msgs[k].Text)})
	}
	return systemone.Choice(fmt.Sprintf(`Hair Jordan is livestreaming. He often answers live chat messages without reading them out loud, sometimes after a delay. Just before, he said: "%s". Then he said: "%s". Is that last line a direct answer or reaction to one of these chat messages? Pick the message it answers, or none.`, prev, ls[i].Text), opts...)
}

// findReplies asks the decision model about every line that has candidates.
// A line is a reply when the model picks a message with p >= replySure.
func findReplies(ctx context.Context, d systemone.Decider, msgs []message, ws []word, logf func(string, ...any)) ([]reply, error) {
	ls := lines(ws)
	type ask struct {
		i     int
		cands []int
	}
	var asks []ask
	for i, l := range ls {
		if len(strings.Fields(l.Text)) < 2 {
			continue
		}
		if c := candidates(msgs, ls, i); len(c) > 0 {
			asks = append(asks, ask{i, c})
		}
	}
	var out []reply
	for b := 0; b < len(asks); b += replyBatch {
		batch := asks[b:min(b+replyBatch, len(asks))]
		req := systemone.Request{State: "A livestream: a line the streamer said, and the live chat messages that arrived shortly before it.",
			Questions: map[string]systemone.Question{}}
		for _, a := range batch {
			req.Questions[fmt.Sprintf("l%d", a.i)] = replyQuestion(msgs, ls, a.i, a.cands)
		}
		c, cancel := context.WithTimeout(ctx, 3*time.Minute)
		res, err := d.Decide(c, req)
		cancel()
		if err != nil {
			return out, fmt.Errorf("lines %d-%d of %d: %w", b+1, b+len(batch), len(asks), err)
		}
		for _, a := range batch {
			if r, ok := pickReply(res.Answers[fmt.Sprintf("l%d", a.i)], msgs, ls[a.i]); ok {
				out = append(out, r)
			}
		}
		logf("replies: asked about %d of %d lines, %d answers to chat so far", b+len(batch), len(asks), len(out))
	}
	return out, nil
}

func pickReply(a systemone.Answer, msgs []message, l line) (reply, bool) {
	var k int
	if a.Choice == "" || a.Choice == "none" || a.Probabilities[a.Choice] < replySure {
		return reply{}, false
	}
	if _, err := fmt.Sscanf(a.Choice, "m%d", &k); err != nil || k < 0 || k >= len(msgs) {
		return reply{}, false
	}
	m := msgs[k]
	return reply{At: l.Start, Line: l.Text, Msg: k, Author: m.Author, Chat: m.Text, Lag: l.Start - m.T, P: a.Probabilities[a.Choice]}, true
}
