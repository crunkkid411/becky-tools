package main

import (
	"context"
	"strings"
	"testing"

	"becky-go/internal/systemone"
)

const sampleLine = `{"replayChatItemAction":{"actions":[{"addChatItemAction":{"item":{"liveChatTextMessageRenderer":{"message":{"runs":[{"text":"where did you buy that red robot spider "},{"emoji":{"shortcuts":[":fire:"]}}]},"authorName":{"simpleText":"Bob"}}}}}],"videoOffsetTimeMsec":"12500"}}`
const paidLine = `{"replayChatItemAction":{"actions":[{"addChatItemAction":{"item":{"liveChatPaidMessageRenderer":{"message":{"runs":[{"text":"love the stream"}]},"authorName":{"simpleText":"Ann"},"purchaseAmountText":{"simpleText":"$5.00"}}}}}],"videoOffsetTimeMsec":"20000"}}`

func TestParsesTextAndSuperChat(t *testing.T) {
	m := parseLine([]byte(sampleLine))
	if len(m) != 1 || m[0].T != 12.5 || m[0].Author != "Bob" || m[0].Text != "where did you buy that red robot spider :fire:" || m[0].Kind != "text" {
		t.Fatalf("got %+v", m)
	}
	p := parseLine([]byte(paidLine))
	if len(p) != 1 || p[0].Kind != "superchat" || p[0].Amount != "$5.00" {
		t.Fatalf("got %+v", p)
	}
}

func TestFindsMessageReadAloud(t *testing.T) {
	msgs := parseLine([]byte(sampleLine))
	var ws []word
	for i, w := range []string{"okay", "so", "Bob", "asks", "where", "did", "you", "buy", "that", "red", "robot", "spider"} {
		ws = append(ws, word{Word: w, Start: 20 + float64(i)*0.3})
	}
	d := matchReadAloud(msgs, ws)
	if msgs[0].ReadAt == nil || d.N != 1 || d.Median < 8 || d.Median > 10 {
		t.Fatalf("read_at %v, delay %+v", msgs[0].ReadAt, d)
	}
}

func TestVideoIDFromFileName(t *testing.T) {
	if got := videoID(`X:\Videos\2026-09-30_Hair-Jordan's-Apology_[9T7Me7-2Aec].mp4`); got != "9T7Me7-2Aec" {
		t.Fatalf("got %q", got)
	}
	if got := videoID("https://www.youtube.com/watch?v=9T7Me7-2Aec&t=5"); got != "9T7Me7-2Aec" {
		t.Fatalf("got %q", got)
	}
}

type fakeDecider struct{ pick map[string]systemone.Answer }

func (f fakeDecider) Decide(_ context.Context, req systemone.Request) (systemone.Response, error) {
	res := systemone.Response{Answers: map[string]systemone.Answer{}}
	for k := range req.Questions {
		if a, ok := f.pick[k]; ok {
			res.Answers[k] = a
		} else {
			res.Answers[k] = systemone.Answer{Type: "choice", Choice: "none", Probabilities: map[string]float64{"none": 0.9}}
		}
	}
	return res, nil
}

func talk(start float64, text string) []word {
	var ws []word
	for i, w := range strings.Fields(text) {
		ws = append(ws, word{Word: w, Start: start + float64(i)*0.3})
	}
	return ws
}

func TestFindsUnreadAnswerToChat(t *testing.T) {
	msgs := []message{
		{T: 10, Author: "Old", Text: "what camera are you using?", Kind: "text"},
		{T: 200, Author: "Ann", Text: "what hair dye do you use?", Kind: "text"},
		{T: 205, Author: "Bo", Text: "hi from texas", Kind: "text"},
	}
	ws := append(talk(100, "So the robot spider is huge."), talk(215, "I use Manic Panic.")...)
	ws = append(ws, talk(230, "Anyway back to the spider.")...)
	// the "I use Manic Panic" line is line 1; the model picks Ann's message (index 1)
	d := fakeDecider{pick: map[string]systemone.Answer{"l1": {Choice: "m1", Probabilities: map[string]float64{"m1": 0.92}}}}
	rs, err := findReplies(context.Background(), d, msgs, ws, func(string, ...any) {})
	if err != nil || len(rs) != 1 || rs[0].Author != "Ann" || rs[0].Line != "I use Manic Panic." || rs[0].Lag != 15 {
		t.Fatalf("replies %+v, err %v", rs, err)
	}
}

func TestCandidatesWidenAfterScrollCue(t *testing.T) {
	msgs := []message{
		{T: 10, Author: "Old", Text: "what camera are you using?", Kind: "text"},
		{T: 150, Author: "Cy", Text: "nice hair today", Kind: "text"},
	}
	plain := lines(talk(200, "This is a normal line."))
	if c := candidates(msgs, plain, 0); len(c) != 1 || c[0] != 1 {
		t.Fatalf("plain line: got %v, want only the message from the last 90s", c)
	}
	cue := lines(append(talk(200, "Hang on let me scroll up."), talk(205, "It is a Sony.")...))
	if c := candidates(msgs, cue, 1); len(c) != 2 || c[1] != 0 {
		t.Fatalf("after a scroll cue: got %v, want the older question too", c)
	}
}

func TestLowConfidencePickIsNotAReply(t *testing.T) {
	msgs := []message{{T: 0, Text: "what hair dye", Kind: "text"}}
	if _, ok := pickReply(systemone.Answer{Choice: "m0", Probabilities: map[string]float64{"m0": 0.5}}, msgs, line{Start: 10}); ok {
		t.Fatal("a 0.5 pick counted as a reply")
	}
}
