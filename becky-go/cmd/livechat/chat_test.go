package main

import "testing"

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
