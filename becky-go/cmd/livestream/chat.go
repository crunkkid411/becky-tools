package main

// chat.go - the lines where Jordan answers a live-chat message, from
// becky-livechat (one call: it downloads the chat replay and finds the answers,
// System One at 0.7+). Jordan, 2026-10-08: "I was responding to someone in chat
// commenting about how I look". An answer is one signal, like looking down at
// his screen: it never cuts a line alone.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// chatAnswers maps a sentence index to the message he answers there
// ("@Lonnie: Hair Jordan wins again?").
func (r *run) chatAnswers(ss []Sentence) (answers, asked map[int]string, err error) {
	path := filepath.Join(r.work, r.stem+".chat.json")
	if _, err := os.Stat(path); err != nil {
		bin, err := beckyBin("becky-livechat")
		if err != nil {
			return nil, nil, err
		}
		r.logf("live chat: downloading the chat replay and finding the lines where he answers it (becky-livechat)...")
		out, err := exec.Command(bin, r.media, "--transcript", filepath.Join(r.work, r.stem+".transcript.json"), "--out", path).CombinedOutput()
		if err != nil {
			_ = os.Remove(path)
			return nil, nil, fmt.Errorf("becky-livechat: %v (%s)", err, lastLines(string(out), 2))
		}
	}
	var c struct {
		Messages []struct {
			T      float64 `json:"t"`
			Author string  `json:"author"`
			Text   string  `json:"text"`
		} `json:"messages"`
		Replies []struct {
			At     float64 `json:"at"`
			Author string  `json:"author"`
			Chat   string  `json:"chat"`
		} `json:"replies"`
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	out := map[int]string{}
	for _, rp := range c.Replies {
		if i := lineAt(ss, rp.At); i >= 0 {
			out[i] = short(rp.Author+": "+rp.Chat, 120)
		}
	}
	// the questions viewers asked just before each line, for the readers: System
	// One did not link "What's on your topic list?" to "I do have a to-do list"
	// 39 s later (27-livestream, 2026-10-08); a reader seeing both does.
	asked = map[int]string{}
	for i, s := range ss {
		var qs []string
		for k := len(c.Messages) - 1; k >= 0 && len(qs) < askedMax; k-- {
			m := c.Messages[k]
			if m.T < s.Start-askedLook {
				break
			}
			if m.T <= s.Start-1 && strings.Contains(m.Text, "?") {
				qs = append(qs, short(m.Author+": "+m.Text, 90))
			}
		}
		if len(qs) > 0 {
			asked[i] = strings.Join(qs, " / ")
		}
	}
	return out, asked, nil
}

const (
	askedLook = 60.0 // seconds of chat before a line shown to the readers
	askedMax  = 4    // newest questions shown
)

// lineAt is the sentence that starts closest to t (becky-livechat's "at" is
// where his line starts; the one before can end a moment after it).
func lineAt(ss []Sentence, t float64) int {
	best, bestD := -1, 1.0
	for i, s := range ss {
		if d := math.Abs(s.Start - t); d < bestD {
			best, bestD = i, d
		}
	}
	return best
}
