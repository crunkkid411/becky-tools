package main

// context.go - after the vote, models READ THE EDIT: only the lines still in,
// in order, with where he is looking on each, and cut what does not belong in
// the finished video. Jordan, 2026-10-08: "we need a separate llm pass AFTER
// the systemone model to determine if what remains belongs there within the
// context of the video itself... The transcript of what remains on the
// timeline alone is nonsensical and even a 4b non-vision model would
// understand that." Every cut changes what stands next to what, so the edit is
// read again until nothing changes (at most readRounds).
//
// Gemma-4 and Qwen3.5 read it on their own. Both say cut -> cut. One says cut
// -> cut only when something else agrees: a model in the vote said cut, or he
// is looking down at his screen.

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	readRounds  = 3
	readChunk   = 80 // kept lines per call
	readContext = 8  // lines before a chunk shown for context
)

const readPrompt = `You are the video editor for Hair Jordan, a YouTuber. You are checking the edit of one of his livestream recordings. You get ONLY the sentences still in the edit, in the order the viewer will hear them. "~ a part was cut here ~" shows where something was taken out between two lines.

The finished video is for people who did NOT watch the stream. It keeps only the WANTED TOPICS. Everything Jordan says about a wanted topic is the content and stays: his opinions, reasons, examples, jokes, rhetorical questions to the camera ("you know what I would do?") and short reactions that carry his point. "Topic #1" in the guidance means the first topic on his own list in the stream; what counts is the subject, not the words "topic one".

Cut a line only when it clearly is one of these:
- answering a viewer: he reacts to something a viewer wrote or answers a comment (often about his looks) without reading it out, e.g. "You're right", "There's no excuse for me to look this way", "I don't even know what that word means". He is usually looking down at his screen then. A line may show the questions viewers asked in chat just before it: when the line answers one of them ("what's on your list?" - "I do have a to-do list, here it is"), he is answering chat. Talking to "you" while looking at the camera is talking to the viewer of the video, not to chat. A topic the guidance asks for stays even when a viewer's question started it.
- the stream itself: chat being held for review, why he is streaming today, what he will cover next, sound or camera, start or end housekeeping.
- mumbling to himself or filling time, with nothing about the topic in it: "I don't know, dude", "it is what it is, man", "ugh", "okay".
- a broken or unfinished sentence, or a fragment that makes no sense next to the lines around it.
When unsure, keep the line.`

// readLine is one kept line as the reader sees it.
type readLine struct {
	idx  int  // index into ss / decisions
	gap  bool // something was cut just before it
	text string
}

// gapLine stands between two kept lines where something was cut. It is its own
// unnumbered line: as a "(cut)" prefix the models read it as the line's verdict
// ("Already cut in prompt", 2026-10-08).
const gapLine = "      ~ a part was cut here ~\n"

func readSchema(n int) map[string]any {
	item := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"n":    map[string]any{"type": "integer"},
			"keep": map[string]any{"type": "boolean"},
			"why":  map[string]any{"type": "string", "maxLength": 90},
		},
		"required": []string{"n", "keep", "why"},
	}
	return jsonSchema("lines", map[string]any{
		"type":       "object",
		"properties": map[string]any{"lines": map[string]any{"type": "array", "items": item, "minItems": n, "maxItems": n}},
		"required":   []string{"lines"},
	})
}

// keptLines lists the kept sentences in order, with "(cut)" before a line when
// something was taken out just before it.
func keptLines(ss []Sentence, ds []Decision) []readLine {
	var out []readLine
	prev := -1
	for i, d := range ds {
		if !d.Keep {
			continue
		}
		post := d.Posture
		if post == "" {
			post = "not measured"
		}
		text := fmt.Sprintf("%s | %s | %s", clock(ss[i].Start), post, ss[i].Text)
		if d.Chat != "" {
			text += fmt.Sprintf("   (a viewer had just written: \"%s\")", d.Chat)
		} else if d.Asked != "" {
			text += fmt.Sprintf("   (questions in chat just before: %s)", d.Asked)
		}
		out = append(out, readLine{idx: i, gap: prev >= 0 && i != prev+1, text: text})
		prev = i
	}
	return out
}

// readEdit asks one model for a keep/cut on every kept line, chunk by chunk.
func (m localModel) readEdit(lines []readLine, brief string, logf func(string, ...any)) (map[int]vote, error) {
	out := map[int]vote{}
	for a := 0; a < len(lines); a += readChunk {
		b := min(a+readChunk, len(lines))
		var sb strings.Builder
		sb.WriteString(brief)
		if a > 0 {
			sb.WriteString("JUST BEFORE (already checked, context only):\n")
			for _, l := range lines[max(0, a-readContext):a] {
				sb.WriteString("   " + l.text + "\n")
			}
			sb.WriteString("\n")
		}
		sb.WriteString("CHECK THESE LINES (time | where he is looking | what he says):\n")
		for k, l := range lines[a:b] {
			if l.gap {
				sb.WriteString(gapLine)
			}
			fmt.Fprintf(&sb, "[%d] %s\n", k+1, l.text)
		}
		fmt.Fprintf(&sb, "\nAnswer for every line from 1 to %d, in order: keep true or false, and why in a few words. Write the JSON compactly, on one line.", b-a)
		var got struct {
			Lines []struct {
				N    int    `json:"n"`
				Keep bool   `json:"keep"`
				Why  string `json:"why"`
			} `json:"lines"`
		}
		var err error
		for attempt := 1; attempt <= 2; attempt++ {
			var raw string
			if raw, err = m.chat(readPrompt, sb.String(), readSchema(b-a), min((60*(b-a)+300)*attempt, 8000)); err == nil {
				if err = json.Unmarshal([]byte(jsonPart(raw)), &got); err == nil && len(got.Lines) != b-a {
					err = fmt.Errorf("%d answers for %d lines", len(got.Lines), b-a)
				}
				if err == nil {
					break
				}
			}
			logf("  %s reading lines %d-%d, attempt %d: %v", m.name, a+1, b, attempt, err)
		}
		if err != nil {
			return out, err
		}
		for k, g := range got.Lines { // by position: the schema fixes the count, not the numbering
			out[lines[a+k].idx] = vote{Model: m.name + " reading the edit", Keep: g.Keep, Why: strings.TrimSpace(g.Why)}
		}
	}
	return out, nil
}

// readCut: does this line go? Every signal counts: a reader's cut twice (it
// saw the whole edit), a voter's cut once (it saw the line in the stream),
// his reading or answering chat (posture or becky-livechat) once. Half or more of the most it could
// score cuts the line: with 3 readers and 3 voters, all three readers alone,
// two readers and one voter, or two readers and his posture. (2026-10-08: a
// two-reader cut alone took "Calling someone bald is not an insult" out of
// the baldness topic; the three voters had all kept it.)
//
// Then (same day): with the topics and the chat questions in front of them, the
// only kept lines two of the three readers cut were "I do have a to-do list...
// here it is... I'll just do it live" - his answer to "What's on your topic
// list?", which Jordan had flagged and which every voter had kept. The
// "Calling someone bald" cut had come from the "(cut)" prefix bug and readers
// that did not know topic #1 was baldness. So a majority of the readers
// decides on its own.
func readCut(d Decision, reads []vote, reading bool) bool {
	score, most, readerCuts := 0, 2*len(reads)+len(d.Votes)+1, 0
	for _, v := range reads {
		if !v.Keep {
			score += 2
			readerCuts++
		}
	}
	if len(reads) > 0 && 2*readerCuts > len(reads) {
		return true
	}
	for _, v := range d.Votes {
		if !v.Keep {
			score++
		}
	}
	if reading {
		score++
	}
	return score > 0 && 2*score >= most
}

// contextPass reads the edit up to readRounds times and cuts what the readers
// (corroborated) say does not belong. Notes say what could not run.
// After round 1 only the lines next to a new cut are judged again (their
// neighbours changed); the rest keep their verdict.
func contextPass(readers []localModelSpec, ss []Sentence, ds []Decision, brief string, post *postureFrames, logf func(string, ...any)) ([]Decision, []string) {
	out := append([]Decision(nil), ds...)
	var notes []string
	var moved map[int]bool // round 2+: lines whose neighbour was cut
	for round := 1; round <= readRounds; round++ {
		lines := keptLines(ss, out)
		if len(lines) == 0 {
			break
		}
		reads := map[int][]vote{}
		for _, spec := range readers {
			m, err := spec.open()
			if err != nil {
				notes = append(notes, fmt.Sprintf("%s could not read the edit: %v", spec.name, err))
				continue
			}
			logf("reading the edit, round %d: %s reads %d kept lines...", round, spec.name, len(lines))
			got, err := m.readEdit(lines, brief, logf)
			m.client.Close()
			if err != nil {
				notes = append(notes, fmt.Sprintf("%s could not finish reading the edit in round %d: %v", spec.name, round, err))
			}
			for i, v := range got {
				reads[i] = append(reads[i], v)
			}
		}
		cut, judged := 0, 0
		next := map[int]bool{}
		for k, l := range lines {
			if moved != nil && !moved[l.idx] {
				continue
			}
			judged++
			d := &out[l.idx]
			d.Read = reads[l.idx]
			reading := d.Chat != "" || (post != nil && post.during(ss[l.idx].Start, ss[l.idx].End).reading())
			if readCut(*d, d.Read, reading) {
				d.Keep, d.Topic = false, 0
				d.Note = fmt.Sprintf("cut when the edit was read (round %d)", round)
				logf("    cut %s \"%s\": %s", clock(ss[l.idx].Start), short(ss[l.idx].Text, 60), voteText(*d))
				cut++
				if k > 0 {
					next[lines[k-1].idx] = true
				}
				if k+1 < len(lines) {
					next[lines[k+1].idx] = true
				}
			}
		}
		logf("  round %d: %d of the %d lines judged were cut", round, cut, judged)
		for i := range next {
			if !out[i].Keep {
				delete(next, i)
			}
		}
		if len(next) == 0 {
			break
		}
		moved = next
	}
	return out, notes
}

// readBrief is what the readers know besides the lines: the guidance, the
// wanted topics and an outline of the whole stream.
func readBrief(guidance string, topics, outline []string) string {
	return "GUIDANCE: " + guidance + "\n\n" + wantedText(topics) + outlineText(outline)
}
