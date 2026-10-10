package main

import (
	"math/bits"
	"slices"
	"strconv"
	"strings"
)

// Port of diarizationlm.utils.transfer_llm_completion (Transcript-Preserving Speaker Transfer):
// the model rewrites the whole passage with its own speaker tags, and may drop or respell a word.
// So its WORDS are thrown away: they are only aligned (word-level Levenshtein, on lowercased text
// without punctuation) to OUR words, and each of our words takes the speaker of the model word it
// lines up with. The model's speaker numbers are then matched to ours by the assignment that
// agrees on the most words (the Hungarian step in the reference code; here an exact bitmask DP,
// at most 10 speakers per prompt). A word the model dropped keeps its old label.

// minMatched is how much of our passage the reply must reproduce word-for-word before we trust
// it. The model is trained to copy the text and only move speaker tags; a reply that rewrites
// more than this is off the rails and ignored.
const minMatched = 0.9

// punctuation the reference normalizer strips (diarizationlm.utils.PUNCTUATIONS).
var punctuation = []string{",", ".", "_", "?", "!", "-", `"`, "'"}

// transfer returns, for each of our words, the chunk-local speaker number ("1", "2", ...) the
// model gave it, or false when the reply does not match the passage well enough to trust.
func transfer(completion string, ours, oursLocal []string) ([]string, bool) {
	if i := strings.Index(completion, "[eod]"); i >= 0 {
		completion = completion[:i]
	}
	llmWords, llmSpk := extractTextAndSpk(completion)
	if len(llmWords) == 0 || len(ours) == 0 || len(ours) != len(oursLocal) {
		return nil, false
	}
	a := normalize(llmWords)
	b := normalize(ours)
	align := levenshteinAlign(a, b) // per our word: index into llm words, or -1
	matched := 0
	for j, i := range align {
		if i >= 0 && a[i] == b[j] {
			matched++
		}
	}
	if float64(matched) < minMatched*float64(len(ours)) {
		return nil, false
	}
	// Assignment: rows = model speakers, cols = our chunk-local speakers, weight = words agreeing.
	n := 0
	for _, s := range llmSpk {
		n = max(n, s)
	}
	ourNum := make([]int, len(oursLocal))
	for j, s := range oursLocal {
		ourNum[j], _ = strconv.Atoi(s)
		n = max(n, ourNum[j])
	}
	w := make([][]int, n)
	for r := range w {
		w[r] = make([]int, n)
	}
	for j, i := range align {
		if i >= 0 && ourNum[j] >= 1 {
			w[llmSpk[i]-1][ourNum[j]-1]++
		}
	}
	assign := bestAssignment(w) // model speaker r -> our speaker assign[r]
	out := make([]string, len(ours))
	for j, i := range align {
		out[j] = oursLocal[j]
		if i < 0 {
			continue // the model dropped this word: keep our label
		}
		col := assign[llmSpk[i]-1]
		if slices.Contains(ourNum, col+1) {
			out[j] = strconv.Itoa(col + 1)
		}
		// else: the model's speaker maps to one we never had — never invent a speaker.
	}
	return out, true
}

// extractTextAndSpk splits "<speaker:1> hi there <speaker:2> yo" into words and their speaker
// numbers (1..10). An unreadable tag keeps the previous speaker, like the reference code.
func extractTextAndSpk(s string) ([]string, []int) {
	var words []string
	var spk []int
	cur := 1
	for _, tok := range strings.Fields(s) {
		if rest, ok := strings.CutPrefix(tok, "<speaker:"); ok {
			v, err := strconv.Atoi(strings.TrimSuffix(rest, ">"))
			if err == nil && v >= 1 && v <= 10 {
				cur = v
			}
			continue
		}
		words = append(words, tok)
		spk = append(spk, cur)
	}
	return words, spk
}

// normalize lowercases each word and strips punctuation, unless that would leave nothing.
func normalize(words []string) []string {
	out := make([]string, len(words))
	for i, w := range words {
		w = strings.ToLower(w)
		for _, p := range punctuation {
			if r := strings.ReplaceAll(w, p, ""); len(strings.Fields(r)) == 1 {
				w = r
			}
		}
		out[i] = w
	}
	return out
}

// levenshteinAlign aligns ref (model words) to hyp (our words) and returns, for each hyp word,
// the ref index it is paired with (match or substitution), or -1 for an insertion. Ties prefer
// insertion, then deletion, then match/substitution — the reference implementation's order.
func levenshteinAlign(ref, hyp []string) []int {
	n1, n2 := len(ref), len(hyp)
	cost := make([][]int, n1+1)
	for i := range cost {
		cost[i] = make([]int, n2+1)
		cost[i][0] = i
	}
	for j := 0; j <= n2; j++ {
		cost[0][j] = j
	}
	for i := 1; i <= n1; i++ {
		for j := 1; j <= n2; j++ {
			sub := cost[i-1][j-1]
			if ref[i-1] != hyp[j-1] {
				sub++
			}
			cost[i][j] = min(cost[i][j-1]+1, cost[i-1][j]+1, sub)
		}
	}
	out := make([]int, n2)
	i, j := n1, n2
	for i > 0 || j > 0 {
		switch {
		case i == 0 || (j > 0 && cost[i][j] == cost[i][j-1]+1):
			out[j-1] = -1 // insertion: our word has no model word
			j--
		case j == 0 || cost[i][j] == cost[i-1][j]+1:
			i-- // deletion: model word with no word of ours
		default:
			out[j-1] = i - 1
			i--
			j--
		}
	}
	return out
}

// bestAssignment maximises the total weight of a one-to-one row->column assignment on a square
// matrix (n <= 10) by DP over the set of used columns. Returns row -> column.
func bestAssignment(w [][]int) []int {
	n := len(w)
	full := 1 << n
	best := make([]int, full)
	from := make([]int, full)
	for m := range best {
		best[m] = -1
	}
	best[0] = 0
	for m := 0; m < full; m++ {
		if best[m] < 0 {
			continue
		}
		r := bits.OnesCount(uint(m))
		if r == n {
			continue
		}
		for c := 0; c < n; c++ {
			if m&(1<<c) != 0 {
				continue
			}
			nm := m | 1<<c
			if v := best[m] + w[r][c]; v > best[nm] {
				best[nm], from[nm] = v, c
			}
		}
	}
	assign := make([]int, n)
	for m, r := full-1, n-1; r >= 0; r-- {
		c := from[m]
		assign[r] = c
		m &^= 1 << c
	}
	return assign
}
