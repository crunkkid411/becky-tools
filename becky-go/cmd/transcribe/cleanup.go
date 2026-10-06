package main

// cleanup.go - --cleanup (report item #3, 2026-10-05): a local model proofreads
// the transcript for words the speech models misheard (names, slang, niche
// terms the word list does not have yet). TEXT ONLY, and the model never gets
// the last word: its answer is lined up word by word with the original and a
// change is kept only when it is
//   - a one-for-one swap of a similar-looking word (never a number), or
//   - a known term from the word list ("take in back 2007" -> TakingBack2007),
//     which becomes one word spanning the original words' times.
// A rephrase, a grammar fix, an added or a dropped word is ignored. Every word
// keeps the time the audio gave it.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/llmlocal"
)

const (
	cleanupLineWords  = 30  // a proofreading line ends at a sentence end or this many words
	cleanupBatchLines = 25  // lines per model call
	cleanupBatchWords = 600 // ...or fewer, so one call stays small
)

const cleanupSystem = `You proofread speech-recognition transcripts of Hair Jordan's livestreams. The recognizer sometimes mishears names, slang and niche words.
Fix ONLY words that were clearly misheard. Do not rephrase. Do not fix grammar. Do not remove filler words (um, uh, like, you know). Do not add or remove words. Keep the words of a line in the same order.
Return ONLY the lines that have a misheard word, corrected, each with its number n. Most lines have none: then return an empty list.`

// CleanupFix is the audit trail entry for one accepted fix.
type CleanupFix struct {
	Start float64 `json:"start"`
	Heard string  `json:"heard"`
	Meant string  `json:"meant"`
}

// cleanupAsk sends one batch of numbered lines and returns the model's raw JSON.
type cleanupAsk func(user string, lines int) (string, error)

// cleanupWithGemma runs the proofreading with Gemma-4 E4B, text only. On any
// failure the words come back unchanged with a plain note.
func cleanupWithGemma(cfg config.Config, words []Word, known []string, verbose bool) ([]Word, []CleanupFix, string) {
	model, _, _ := cfg.GemmaAVLM()
	c := llmlocal.NewWarmClientCtx(model, cfg.LlamaServer, 8192, nil)
	if err := c.Available(); err != nil {
		return words, nil, "cleanup skipped: " + err.Error()
	}
	defer c.Close()
	ask := func(user string, lines int) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		return c.Chat(ctx, cleanupSystem, user, llmlocal.Options{MaxTokens: 3*cleanupBatchWords + 20*lines + 200, ResponseFormat: cleanupSchema(lines)})
	}
	out, fixes, err := cleanupWords(words, known, ask, func(f string, a ...any) { beckyio.Logf(verbose, f, a...) })
	if err != nil {
		return out, fixes, "cleanup stopped early: " + err.Error()
	}
	return out, fixes, ""
}

func cleanupSchema(n int) map[string]any {
	line := map[string]any{"type": "object", "properties": map[string]any{
		"n": map[string]any{"type": "integer"}, "text": map[string]any{"type": "string"}},
		"required": []string{"n", "text"}}
	return map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "lines", "schema": map[string]any{
		"type":       "object",
		"properties": map[string]any{"lines": map[string]any{"type": "array", "items": line, "maxItems": n}},
		"required":   []string{"lines"}}}}
}

// cleanupLines groups word indexes into proofreading lines.
func cleanupLines(words []Word) [][2]int {
	var out [][2]int
	a := 0
	for i, w := range words {
		t := strings.TrimRight(w.Word, `"')]`)
		end := strings.HasSuffix(t, ".") || strings.HasSuffix(t, "?") || strings.HasSuffix(t, "!")
		if end || i-a+1 >= cleanupLineWords || i == len(words)-1 {
			out = append(out, [2]int{a, i + 1})
			a = i + 1
		}
	}
	return out
}

// cleanupWords proofreads every line and applies the accepted fixes.
func cleanupWords(words []Word, known []string, ask cleanupAsk, logf func(string, ...any)) ([]Word, []CleanupFix, error) {
	lines := cleanupLines(words)
	knownTerms, knownWords := map[string]bool{}, map[string]bool{}
	for _, k := range known {
		knownTerms[strings.Join(normAll(strings.Fields(k)), "")] = true
		for _, w := range strings.Fields(k) {
			knownWords[normWord(w)] = true
		}
	}
	terms := strings.Join(known, ", ")
	out := make([]Word, 0, len(words))
	var fixes []CleanupFix
	for b := 0; b < len(lines); {
		e, n := b, 0
		for e < len(lines) && e-b < cleanupBatchLines && (n < cleanupBatchWords || e == b) {
			n += lines[e][1] - lines[e][0]
			e++
		}
		var sb strings.Builder
		sb.WriteString("Known names and terms: " + terms + "\n\nProofread these lines. Return only the lines with a misheard word, with their number n:\n")
		for k := b; k < e; k++ {
			fmt.Fprintf(&sb, "%d| %s\n", k-b+1, wordsText(words[lines[k][0]:lines[k][1]]))
		}
		var got map[int]string
		var err error
		for attempt := 1; attempt <= 2; attempt++ {
			var raw string
			if raw, err = ask(sb.String(), e-b); err == nil {
				got, err = parseCleanup(raw, e-b)
			}
			if err == nil {
				break
			}
		}
		if err != nil { // keep the rest exactly as heard
			return append(out, words[lines[b][0]:]...), fixes, fmt.Errorf("lines %d-%d: %w", b+1, e, err)
		}
		for k := b; k < e; k++ {
			ws := words[lines[k][0]:lines[k][1]]
			if text, ok := got[k-b+1]; ok {
				var fs []CleanupFix
				ws, fs = applyCleanupLine(ws, strings.Fields(text), knownTerms, knownWords)
				fixes = append(fixes, fs...)
			}
			out = append(out, ws...)
		}
		logf("cleanup: lines %d-%d of %d", b+1, e, len(lines))
		b = e
	}
	return out, fixes, nil
}

// parseCleanup reads the corrected lines by their number (1..n); a number
// outside the batch is ignored.
func parseCleanup(raw string, n int) (map[int]string, error) {
	if i := strings.Index(raw, "{"); i > 0 {
		raw = raw[i:]
	}
	var r struct {
		Lines *[]struct {
			N    int    `json:"n"`
			Text string `json:"text"`
		} `json:"lines"`
	}
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return nil, fmt.Errorf("unreadable answer: %w", err)
	}
	if r.Lines == nil {
		return nil, fmt.Errorf("the answer has no lines list")
	}
	out := map[int]string{}
	for _, l := range *r.Lines {
		if l.N >= 1 && l.N <= n {
			out[l.N] = l.Text
		}
	}
	return out, nil
}

func normAll(ws []string) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = normWord(w)
	}
	return out
}

// diffOp pairs a heard word i with an answered word j; i < 0 is an added word,
// j < 0 a dropped one.
type diffOp struct{ i, j int }

// diffRegion is a stretch where the answer differs: a[i0:i1] was answered with
// b[j0:j1], word by word as ops.
type diffRegion struct {
	i0, i1, j0, j1 int
	ops            []diffOp
}

// diffRegions lines two word lists up (edit distance where swapping two
// look-alike words is cheaper than swapping unrelated ones, so "Harry" pairs
// with "Hair" even next to an added word) and returns where they differ.
func diffRegions(a, b []string) []diffRegion {
	n, m := len(a), len(b)
	sub := func(i, j int) float64 {
		if a[i] == b[j] {
			return 0
		}
		return 1 - likeness(a[i], b[j])
	}
	d := make([][]float64, n+1)
	for i := range d {
		d[i] = make([]float64, m+1)
		d[i][0] = float64(i)
	}
	for j := range d[0] {
		d[0][j] = float64(j)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+sub(i-1, j-1))
		}
	}
	var ops []diffOp // backwards
	for i, j := n, m; i > 0 || j > 0; {
		switch {
		case i > 0 && j > 0 && d[i][j] == d[i-1][j-1]+sub(i-1, j-1):
			i, j = i-1, j-1
			ops = append(ops, diffOp{i, j})
		case i > 0 && d[i][j] == d[i-1][j]+1:
			i--
			ops = append(ops, diffOp{i, -1})
		default:
			j--
			ops = append(ops, diffOp{-1, j})
		}
	}
	slices.Reverse(ops)
	var regs []diffRegion
	i, j := 0, 0 // position before each op
	for k := 0; k < len(ops); {
		if o := ops[k]; o.i >= 0 && o.j >= 0 && a[o.i] == b[o.j] {
			i, j, k = i+1, j+1, k+1
			continue
		}
		r := diffRegion{i0: i, j0: j}
		for ; k < len(ops) && !(ops[k].i >= 0 && ops[k].j >= 0 && a[ops[k].i] == b[ops[k].j]); k++ {
			r.ops = append(r.ops, ops[k])
			if ops[k].i >= 0 {
				i++
			}
			if ops[k].j >= 0 {
				j++
			}
		}
		r.i1, r.j1 = i, j
		regs = append(regs, r)
	}
	return regs
}

func likeness(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 && len(rb) == 0 {
		return 1
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			c := 1
			if ra[i-1] == rb[j-1] {
				c = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+c)
		}
		prev, cur = cur, prev
	}
	return 1 - float64(prev[len(rb)])/float64(max(len(ra), len(rb)))
}

func hasDigit(s string) bool { return strings.IndexFunc(s, unicode.IsDigit) >= 0 }

// acceptSwap: a one-for-one word change the model may make.
func acceptSwap(heard, meant string, knownWords map[string]bool) bool {
	if heard == meant || meant == "" || hasDigit(heard) || hasDigit(meant) {
		return false
	}
	l := likeness(heard, meant)
	if knownWords[meant] {
		return l >= 0.3
	}
	return max(len([]rune(heard)), len([]rune(meant))) >= 4 && l >= 0.5
}

// core strips the punctuation around a word.
func core(w string) string {
	pre, suf := splitPunct(w)
	r := []rune(w)
	return string(r[len([]rune(pre)) : len(r)-len([]rune(suf))])
}

// applyCleanupLine applies the accepted changes of one proofread line.
func applyCleanupLine(ws []Word, answer []string, knownTerms, knownWords map[string]bool) ([]Word, []CleanupFix) {
	if len(answer) == 0 {
		return ws, nil
	}
	a, b := normAll(wordStrings(ws)), normAll(answer)
	out := make([]Word, 0, len(ws))
	var fixes []CleanupFix
	next := 0
	for _, r := range diffRegions(a, b) {
		out = append(out, ws[next:r.i0]...)
		next = r.i1
		heard := ws[r.i0:r.i1]
		meant := answer[r.j0:r.j1]
		joined := strings.Join(b[r.j0:r.j1], "")
		merge := len(heard) > 0 && len(meant) > 0 && len(heard) != len(meant) &&
			knownTerms[joined] && likeness(strings.Join(a[r.i0:r.i1], ""), joined) >= 0.6
		if !merge { // word by word: only look-alike swaps; added or dropped words are ignored
			for _, op := range r.ops {
				if op.i < 0 {
					continue
				}
				w := ws[op.i]
				if op.j >= 0 && acceptSwap(a[op.i], b[op.j], knownWords) {
					pre, suf := splitPunct(w.Word)
					fixes = append(fixes, CleanupFix{Start: w.Start, Heard: w.Word, Meant: pre + core(answer[op.j]) + suf})
					w.Word = pre + core(answer[op.j]) + suf
				}
				out = append(out, w)
			}
			continue
		}
		// a known term in a different number of words: one word, spanning them
		parts := make([]string, len(meant))
		for k, m := range meant {
			parts[k] = core(m)
		}
		pre, _ := splitPunct(heard[0].Word)
		_, suf := splitPunct(heard[len(heard)-1].Word)
		w := heard[0]
		w.Word = pre + strings.Join(parts, " ") + suf
		w.End = heard[len(heard)-1].End
		w.Confidence = minConfidence(heard)
		fixes = append(fixes, CleanupFix{Start: w.Start, Heard: wordsText(heard), Meant: w.Word})
		out = append(out, w)
	}
	return append(out, ws[next:]...), fixes
}

func wordStrings(ws []Word) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.Word
	}
	return out
}

// knownTerms are the word list's "meant" sides: names the proofreader is told about.
func knownTerms(fixes []lexFix) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range fixes {
		t := strings.Join(f.meant, " ")
		if !seen[strings.ToLower(t)] {
			seen[strings.ToLower(t)] = true
			out = append(out, t)
		}
	}
	return out
}
