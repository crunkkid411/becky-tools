package main

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const (
	readWindow   = 90.0 // seconds after a message in which reading it aloud counts
	minReadWords = 3    // content words of a message he must say for it to count as read
	minReadShare = 0.6  // ...and at least this share of the message's content words
	// ...of which at least minRare are words he says rarely in this stream: in the
	// apology stream "sorry" (248x) and "john" (45x) made coincidental matches.
	rareMax = 15
	minRare = 2
)

type message struct {
	T         float64  `json:"t"`
	Author    string   `json:"author"`
	Text      string   `json:"text"`
	Kind      string   `json:"kind"` // text | superchat | sticker | membership
	Amount    string   `json:"amount,omitempty"`
	ReadAt    *float64 `json:"read_at,omitempty"`
	ReadWords int      `json:"read_words,omitempty"`
}

type word struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
}

type delay struct {
	Median float64 `json:"median"`
	N      int     `json:"n"`
}

type runs struct {
	SimpleText string `json:"simpleText"`
	Runs       []struct {
		Text  string `json:"text"`
		Emoji *struct {
			Shortcuts []string `json:"shortcuts"`
		} `json:"emoji"`
	} `json:"runs"`
}

func (r runs) String() string {
	if r.SimpleText != "" {
		return r.SimpleText
	}
	var b strings.Builder
	for _, x := range r.Runs {
		if x.Emoji != nil && len(x.Emoji.Shortcuts) > 0 {
			b.WriteString(x.Emoji.Shortcuts[0])
		} else {
			b.WriteString(x.Text)
		}
	}
	return b.String()
}

type renderer struct {
	Message        runs `json:"message"`
	AuthorName     runs `json:"authorName"`
	PurchaseAmount runs `json:"purchaseAmountText"`
	HeaderSubtext  runs `json:"headerSubtext"`
}

func parseLine(line []byte) []message {
	var l struct {
		Replay struct {
			Actions []struct {
				Add struct {
					Item map[string]renderer `json:"item"`
				} `json:"addChatItemAction"`
			} `json:"actions"`
			Offset string `json:"videoOffsetTimeMsec"`
		} `json:"replayChatItemAction"`
	}
	if json.Unmarshal(line, &l) != nil {
		return nil
	}
	ms, err := strconv.ParseFloat(l.Replay.Offset, 64)
	if err != nil {
		return nil
	}
	var out []message
	for _, a := range l.Replay.Actions {
		for kind, r := range a.Add.Item {
			m := message{T: ms / 1000, Author: r.AuthorName.String(), Text: r.Message.String()}
			switch kind {
			case "liveChatTextMessageRenderer":
				m.Kind = "text"
			case "liveChatPaidMessageRenderer":
				m.Kind, m.Amount = "superchat", r.PurchaseAmount.String()
			case "liveChatPaidStickerRenderer":
				m.Kind, m.Amount = "sticker", r.PurchaseAmount.String()
			case "liveChatMembershipItemRenderer":
				m.Kind = "membership"
				if m.Text == "" {
					m.Text = r.HeaderSubtext.String()
				}
			default:
				continue
			}
			out = append(out, m)
		}
	}
	return out
}

var stop = map[string]bool{"the": true, "a": true, "an": true, "and": true, "or": true, "to": true, "of": true, "is": true,
	"it": true, "i": true, "you": true, "in": true, "on": true, "for": true, "that": true, "this": true, "be": true, "are": true,
	"was": true, "so": true, "do": true, "my": true, "me": true, "your": true, "lol": true, "what": true, "at": true}

func contentWords(s string) []string {
	var out []string
	for _, t := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\'' }) {
		if !stop[t] && len(t) > 1 {
			out = append(out, t)
		}
	}
	return out
}

// matchReadAloud finds, for each message, the first moment within readWindow
// after it arrived where Jordan's next words contain most of its content
// words, at least two of them words he rarely says. Plain code, no model: one
// signal about "talking to chat", alongside gaze/posture and the transcript.
func matchReadAloud(msgs []message, ws []word) delay {
	norm := make([]string, len(ws))
	said := map[string]int{}
	for i, w := range ws {
		norm[i] = strings.Join(contentWords(w.Word), "")
		said[norm[i]]++
	}
	var lags []float64
	for mi := range msgs {
		m := &msgs[mi]
		want := contentWords(m.Text)
		if len(want) < minReadWords {
			continue
		}
		start := sort.Search(len(ws), func(i int) bool { return ws[i].Start >= m.T })
		for i := start; i < len(ws) && ws[i].Start <= m.T+readWindow; i++ {
			if !contains(want, norm[i]) {
				continue
			}
			got := map[string]bool{}
			for k := i; k < len(ws) && ws[k].Start <= ws[i].Start+float64(len(want))*0.8+2; k++ {
				if contains(want, norm[k]) {
					got[norm[k]] = true
				}
			}
			rare := 0
			for g := range got {
				if said[g] <= rareMax {
					rare++
				}
			}
			if len(got) >= minReadWords && rare >= minRare && float64(len(got)) >= minReadShare*float64(len(uniq(want))) {
				t := ws[i].Start
				m.ReadAt, m.ReadWords = &t, len(got)
				lags = append(lags, t-m.T)
				break
			}
		}
	}
	if len(lags) == 0 {
		return delay{}
	}
	sort.Float64s(lags)
	return delay{Median: lags[len(lags)/2], N: len(lags)}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s && s != "" {
			return true
		}
	}
	return false
}

func uniq(xs []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}
