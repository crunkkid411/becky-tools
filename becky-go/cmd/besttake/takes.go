package main

// The take picker, after Paul Borg's videokit.py (hermes-research-agent,
// claude/skills/youtube-video/scripts/videokit.py:338-380), with a second,
// model-free signal beside the decision model so the two can corroborate:
//
//  1. Split the transcript into lines (sentence ends, or a pause > lineGap).
//  2. For every line, ask whether it restarts each of the previous `window`
//     lines (decision model Noul) AND measure how many opening words it shares
//     with that line (plain code). combined = modelWeight*model + (1-modelWeight)*text.
//  3. Group attempts at one thought (Paul's grouping rule).
//  4. Ask of every attempt in a multi-attempt group whether it finishes its
//     thought; keep the LAST finished attempt, else the best guess flagged unsure.

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"becky-go/internal/systemone"
)

const (
	lineGap      = 1.0 // seconds of silence that end a line even without punctuation
	noiseWords   = 2   // lines this short are never a take ("yeah", "so"): Paul's rule
	restartAt    = 0.5 // combined restart score that links two lines
	finishedAt   = 0.5 // P(finished) that counts an attempt as complete
	modelWeight  = 0.6 // share of the decision model in the combined restart score
	linesPerCall = 20  // 20 lines x window 6 = up to 120 questions per request
	openingWords = 4   // opening words compared by the text signal
	selfRestart  = 3   // a line that repeats its own first 3 words starts a new attempt there
	coveredAt    = 0.5 // combined score above which a dropped attempt's line is said by the kept take
	restartQ     = "`later` repeats the opening words of `earlier`, as a retake of the same sentence."
	finishedQ    = "Does `take` finish its thought - it ends on a complete sentence instead of trailing off, breaking off mid-sentence, or being abandoned?"
	coveredQ     = "Does `kept` already say what `line` says, so `line` adds nothing a viewer would miss?"
)

type word struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Line is one spoken line and what the picker decided about it.
type Line struct {
	I         int     `json:"i"`
	Start     float64 `json:"start"`
	End       float64 `json:"end"`
	Text      string  `json:"text"`
	Noise     bool    `json:"noise,omitempty"`
	Group     int     `json:"group"`
	Attempt   int     `json:"attempt"`
	Attempts  int     `json:"attempts"`
	Keep      bool    `json:"keep"`
	Unsure    bool    `json:"unsure,omitempty"`
	RestartOf int     `json:"restart_of"` // line it restarts, -1 none
	ModelP    float64 `json:"restart_model"`
	TextP     float64 `json:"restart_text"`
	Finished  float64 `json:"finished"`
	Covered   float64 `json:"covered,omitempty"` // dropped-attempt line: how surely the kept take repeats it
}

func splitLines(ws []word) []Line {
	var out []Line
	var cur []word
	flush := func() {
		if len(cur) == 0 {
			return
		}
		parts := make([]string, len(cur))
		for i, w := range cur {
			parts[i] = strings.TrimSpace(w.Word)
		}
		out = append(out, Line{I: len(out), Start: cur[0].Start, End: cur[len(cur)-1].End, Text: strings.Join(parts, " "), RestartOf: -1})
		cur = nil
	}
	for i, w := range ws {
		if i > 0 && w.Start-ws[i-1].End > lineGap {
			flush()
		}
		if restartsItself(cur, ws[i:]) {
			flush()
		}
		cur = append(cur, w)
		if t := strings.TrimSpace(w.Word); t != "" && strings.ContainsAny(t[len(t)-1:], ".?!") {
			flush()
		}
	}
	flush()
	for i := range out {
		out[i].Noise = len(strings.Fields(out[i].Text)) <= noiseWords
	}
	return out
}

// restartsItself: the words from here on repeat the line's own opening
// (selfRestart words), as in "Do not buy one of the Do not buy one of the AI".
func restartsItself(cur, rest []word) bool {
	if len(cur) < selfRestart || len(rest) < selfRestart {
		return false
	}
	for k := 0; k < selfRestart; k++ {
		if norm(cur[k].Word) != norm(rest[k].Word) {
			return false
		}
	}
	return true
}

func norm(w string) string { return strings.Join(tokens(w), "") }

func tokens(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\'' })
}

// openingOverlap: share of the first openingWords words of `later` that match
// `earlier` word for word from the start. "So the whole point" vs "So the whole
// point of this video" = 1.0.
func openingOverlap(earlier, later string) float64 {
	a, b := tokens(earlier), tokens(later)
	n := min(openingWords, len(a), len(b))
	if n == 0 {
		return 0
	}
	same := 0
	for i := 0; i < n && a[i] == b[i]; i++ {
		same++
	}
	return float64(same) / float64(n)
}

type pairKey struct{ i, j int }

// restartScores asks the model about every (line, earlier line) pair within
// the window, linesPerCall lines per request.
func restartScores(ctx context.Context, d systemone.Decider, lines []Line, window int) (map[pairKey]float64, error) {
	out := map[pairKey]float64{}
	for c0 := 1; c0 < len(lines); c0 += linesPerCall {
		qs := map[string]systemone.Question{}
		for i := c0; i < min(c0+linesPerCall, len(lines)); i++ {
			if lines[i].Noise {
				continue
			}
			for j := max(i-window, 0); j < i; j++ {
				if lines[j].Noise {
					continue
				}
				qs[fmt.Sprintf("r%d_%d", i, j)] = systemone.NoulWith(restartQ, map[string]any{"earlier": lines[j].Text, "later": lines[i].Text})
			}
		}
		if len(qs) == 0 {
			continue
		}
		resp, err := d.Decide(ctx, systemone.Request{State: "Transcript lines from one recording session of a single speaker.", Questions: qs})
		if err != nil {
			return nil, err
		}
		for k, a := range resp.Answers {
			var i, j int
			if _, err := fmt.Sscanf(k, "r%d_%d", &i, &j); err == nil {
				out[pairKey{i, j}] = a.Noul
			}
		}
	}
	return out, nil
}

// group links restarts (Paul's rule): a line that restarts a sentence begun up
// to `window` lines back closes the attempt it restarts. Returns attempts as
// [first,last] line spans, grouped per thought.
func group(lines []Line, window int, model map[pairKey]float64) [][][2]int {
	var groups [][][2]int
	var grp [][2]int
	cand := 0
	for i := 1; i < len(lines); i++ {
		hit := -1
		for j := max(i-window, 0); j < i; j++ {
			if lines[i].Noise || lines[j].Noise {
				continue
			}
			text := openingOverlap(lines[j].Text, lines[i].Text)
			m := model[pairKey{i, j}]
			if modelWeight*m+(1-modelWeight)*text > restartAt {
				if hit < 0 {
					hit = j
					lines[i].RestartOf, lines[i].ModelP, lines[i].TextP = j, m, text
				}
			}
		}
		if hit < 0 {
			continue
		}
		if hit > cand { // restarted a later sentence: the open attempt is done, a new thought began at hit
			grp = append(grp, [2]int{cand, hit - 1})
			groups = append(groups, grp)
			grp, cand = nil, hit
		}
		grp = append(grp, [2]int{cand, i - 1})
		cand = i
	}
	grp = append(grp, [2]int{cand, len(lines) - 1})
	return append(groups, grp)
}

func spanText(lines []Line, s [2]int) string {
	parts := make([]string, 0, s[1]-s[0]+1)
	for k := s[0]; k <= s[1]; k++ {
		parts = append(parts, lines[k].Text)
	}
	return strings.Join(parts, " ")
}

// pick marks every line keep / cut. Single-attempt groups keep everything but
// noise; multi-attempt groups keep only the chosen attempt.
func pick(ctx context.Context, d systemone.Decider, lines []Line, groups [][][2]int) error {
	for gi, g := range groups {
		for ai, s := range g {
			for k := s[0]; k <= s[1]; k++ {
				lines[k].Group, lines[k].Attempt, lines[k].Attempts = gi, ai, len(g)
				lines[k].Keep = len(g) == 1 && !lines[k].Noise
			}
		}
		if len(g) == 1 {
			continue
		}
		qs := map[string]systemone.Question{}
		for ai, s := range g {
			qs[fmt.Sprintf("c%d", ai)] = systemone.NoulWith(finishedQ, map[string]any{"take": spanText(lines, s)})
		}
		resp, err := d.Decide(ctx, systemone.Request{State: "Attempts at one sentence from a recording session.", Questions: qs})
		if err != nil {
			return err
		}
		best, chosen := -1.0, -1
		for ai := range g {
			p := resp.Answers[fmt.Sprintf("c%d", ai)].Noul
			for k := g[ai][0]; k <= g[ai][1]; k++ {
				lines[k].Finished = p
			}
			if p > finishedAt {
				chosen = ai // last finished attempt wins
			}
			if chosen < 0 && p > best {
				best = p
			}
		}
		unsure := chosen < 0
		if unsure {
			for ai := range g {
				if lines[g[ai][0]].Finished == best {
					chosen = ai
				}
			}
		}
		for k := g[chosen][0]; k <= g[chosen][1]; k++ {
			lines[k].Keep, lines[k].Unsure = !lines[k].Noise, unsure
		}
		if err := keepUncovered(ctx, d, lines, g, chosen); err != nil {
			return err
		}
	}
	return nil
}

// keepUncovered puts back lines of the dropped attempts that the chosen take
// does not repeat: a redone passage often has asides in between that Jordan
// keeps ("I've done all the research for you"). Two signals: the decision
// model, and the share of the line's words that the kept take contains.
func keepUncovered(ctx context.Context, d systemone.Decider, lines []Line, g [][2]int, chosen int) error {
	kept := spanText(lines, g[chosen])
	qs := map[string]systemone.Question{}
	for ai, s := range g {
		if ai == chosen {
			continue
		}
		for k := s[0]; k <= s[1]; k++ {
			if !lines[k].Noise {
				qs[fmt.Sprintf("v%d", k)] = systemone.NoulWith(coveredQ, map[string]any{"kept": kept, "line": lines[k].Text})
			}
		}
	}
	if len(qs) == 0 {
		return nil
	}
	resp, err := d.Decide(ctx, systemone.Request{State: "A speaker redid part of a recording; `kept` is the take that stays in the edit.", Questions: qs})
	if err != nil {
		return err
	}
	for k := range lines {
		a, ok := resp.Answers[fmt.Sprintf("v%d", k)]
		if !ok {
			continue
		}
		text := containment(lines[k].Text, kept)
		lines[k].Covered = modelWeight*a.Noul + (1-modelWeight)*text
		if lines[k].Covered <= coveredAt {
			lines[k].Keep = true
		}
	}
	return nil
}

// containment: share of the line's words that also appear in kept.
func containment(line, kept string) float64 {
	in := map[string]bool{}
	for _, t := range tokens(kept) {
		in[t] = true
	}
	ts := tokens(line)
	if len(ts) == 0 {
		return 1
	}
	n := 0
	for _, t := range ts {
		if in[t] {
			n++
		}
	}
	return float64(n) / float64(len(ts))
}
