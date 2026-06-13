// engine.go — the detection + replacement engine for becky-deslop.
//
// Flow (deterministic, single pass per concern):
//  1. computeSkipRanges() finds markdown code/frontmatter zones to protect.
//  2. For each active rule, find all regex matches outside skip zones, record a
//     Finding (with 1-based line/col), and — unless the rule is detect-only —
//     splice the replacement into a working buffer.
//  3. Run structural heuristics (monotony, rule-of-three, synonym cycling) that
//     report findings but never rewrite text.
//
// Replacement is applied right-to-left within each rule pass so earlier byte
// offsets stay valid. Skip ranges are recomputed before each rewriting rule so a
// rule never edits inside a zone. Detection findings come from the ORIGINAL text
// so line/col always reference the user's input.
package main

import (
	"regexp"
	"strings"
)

// Finding is one reported AI-tell occurrence. JSON tags match the spec exactly.
type Finding struct {
	Category   string `json:"category"`
	Line       int    `json:"line"`
	Col        int    `json:"col"`
	Match      string `json:"match"`
	Suggestion string `json:"suggestion"`
}

// Result bundles everything process() produces.
type Result struct {
	Cleaned  string
	Findings []Finding
	Counts   map[string]int
	Score    int
	Clean    bool
}

// process runs the full pipeline against text and returns cleaned output plus
// findings. format is one of "minimal" | "full" | "aggressive".
func process(text string, rules []Rule, format string) Result {
	// --- Detection pass over ORIGINAL text (stable line/col for reporting) ---
	skip := computeSkipRanges(text)
	var findings []Finding

	for _, r := range rules {
		if !activeForFormat(r, format) {
			continue
		}
		for _, loc := range r.re.FindAllStringIndex(text, -1) {
			start, end := loc[0], loc[1]
			if inSkip(start, end, skip) {
				continue
			}
			match := text[start:end]
			line, col := lineCol(text, start)
			findings = append(findings, Finding{
				Category:   r.Category,
				Line:       line,
				Col:        col,
				Match:      match,
				Suggestion: suggestionFor(r, match),
			})
		}
	}

	// --- Structural heuristics (detect-only) -------------------------------
	findings = append(findings, structuralFindings(text, skip)...)

	// --- Rewrite pass (skips detect-only + structural rules) ---------------
	cleaned := rewrite(text, rules, format)

	counts := tallyCounts(findings)
	score := tallyScore(findings, rules)

	return Result{
		Cleaned:  cleaned,
		Findings: findings,
		Counts:   counts,
		Score:    score,
		Clean:    len(findings) == 0,
	}
}

// rewrite applies every non-flag rewriting rule to text, protecting skip zones,
// and returns the cleaned string. Rules apply in declaration order; matches
// within a rule are spliced right-to-left.
func rewrite(text string, rules []Rule, format string) string {
	buf := text
	for _, r := range rules {
		if !activeForFormat(r, format) || isFlagOnly(r.Replacement) {
			continue
		}
		skip := computeSkipRanges(buf) // recompute: edits can shift zones
		locs := r.re.FindAllStringSubmatchIndex(buf, -1)
		for i := len(locs) - 1; i >= 0; i-- {
			loc := locs[i]
			start, end := loc[0], loc[1]
			if inSkip(start, end, skip) {
				continue
			}
			repl := expandReplacement(r, buf, loc)
			buf = spliceClean(buf, start, end, repl)
		}
	}
	return buf
}

// expandReplacement builds the replacement text for one match, applying $1-style
// group references via the regexp engine's Expand.
func expandReplacement(r Rule, src string, loc []int) string {
	if !strings.Contains(r.Replacement, "$") {
		return r.Replacement
	}
	return string(r.re.ExpandString(nil, r.Replacement, src, loc))
}

// spliceClean replaces src[start:end] with repl and tidies whitespace and
// sentence capitalization. A removal (repl == "") collapses the gap it leaves;
// a replacement that introduces a sentence boundary (a ". " inside repl, e.g.
// the dash-separator rewrite "$1. $2") promotes the following word's case so
// "fast. it" becomes "fast. It".
func spliceClean(src string, start, end int, repl string) string {
	out := src[:start] + repl + src[end:]
	if repl == "" {
		return cleanupAt(out, start)
	}
	// Promote case after any sentence-terminator+space introduced by the repl.
	for i := 0; i+1 < len(repl); i++ {
		if (repl[i] == '.' || repl[i] == '!' || repl[i] == '?') && repl[i+1] == ' ' {
			out = promoteSentenceCase(out, start+i+2)
		}
	}
	return out
}

// cleanupAt collapses the artifacts a deletion leaves at position p:
//   - "word  word"   -> "word word"   (double space)
//   - "word , word"  -> "word, word"  (orphan space before punctuation)
//   - " text" at line start after a leading-phrase removal -> "text"
//   - ", text" / ". text" at a line or sentence start (a leading phrase that
//     ended in punctuation was removed) -> "text"
//   - " ." (a whole sentence removed) -> "."  collapsed away to nothing
//   - lowercase first letter promoted to uppercase if the deletion exposed a
//     new sentence start.
func cleanupAt(s string, p int) string {
	b := []byte(s)

	// Collapse a double space straddling the splice.
	if p > 0 && p < len(b) && b[p-1] == ' ' && b[p] == ' ' {
		b = append(b[:p-1], b[p:]...)
		p--
	}
	// Drop a space now sitting before a comma/period/etc. If that exposed an
	// orphan terminator left by removing a full sentence (e.g. "clear. ."),
	// drop the orphan terminator+space too.
	if p < len(b) && b[p] == ' ' && p+1 < len(b) && isCloserPunct(b[p+1]) {
		if atSentenceOrLineStart(b, p) && isSentenceEnd(b[p+1]) {
			// " ." after a sentence boundary: remove the space and the orphan.
			b = removeRange(b, p, p+2)
		} else {
			b = append(b[:p], b[p+1:]...)
		}
	}
	// Trim a leading space at line/text start (phrase removed from the head).
	for p < len(b) && b[p] == ' ' && (p == 0 || b[p-1] == '\n') {
		b = append(b[:p], b[p+1:]...)
	}
	// Drop a leading orphan comma/terminator + space at a line/sentence start.
	if p < len(b) && isCloserPunct(b[p]) && atSentenceOrLineStart(b, p) {
		q := p + 1
		for q < len(b) && b[q] == ' ' {
			q++
		}
		b = removeRange(b, p, q)
	}
	return promoteSentenceCase(string(b), p)
}

// atSentenceOrLineStart reports whether position p sits at the start of text, a
// line, or just after a sentence terminator (ignoring intervening spaces).
func atSentenceOrLineStart(b []byte, p int) bool {
	k := p - 1
	for k >= 0 && b[k] == ' ' {
		k--
	}
	return k < 0 || b[k] == '\n' || b[k] == '.' || b[k] == '!' || b[k] == '?'
}

// isSentenceEnd reports whether c terminates a sentence.
func isSentenceEnd(c byte) bool { return c == '.' || c == '!' || c == '?' }

// removeRange deletes b[from:to] and returns the result.
func removeRange(b []byte, from, to int) []byte {
	if from < 0 {
		from = 0
	}
	if to > len(b) {
		to = len(b)
	}
	if from >= to {
		return b
	}
	return append(b[:from], b[to:]...)
}

// isCloserPunct reports characters that should hug the previous word.
func isCloserPunct(c byte) bool {
	switch c {
	case ',', '.', ';', ':', '!', '?', ')':
		return true
	}
	return false
}

// promoteSentenceCase uppercases the first letter at/after p when it begins a
// new sentence (preceded by start-of-text, newline, or ". "/"! "/"? ").
func promoteSentenceCase(s string, p int) string {
	if p < 0 || p >= len(s) {
		return s
	}
	q := p
	for q < len(s) && s[q] == ' ' {
		q++
	}
	if q >= len(s) {
		return s
	}
	c := s[q]
	if c < 'a' || c > 'z' {
		return s
	}
	startsSentence := q == 0
	if !startsSentence {
		k := q - 1
		for k >= 0 && s[k] == ' ' {
			k--
		}
		if k < 0 || s[k] == '\n' || s[k] == '.' || s[k] == '!' || s[k] == '?' {
			startsSentence = true
		}
	}
	if !startsSentence {
		return s
	}
	b := []byte(s)
	b[q] = c - 'a' + 'A'
	return string(b)
}

// suggestionFor produces the human-facing suggestion string for a finding. For
// grouped rewrites it expands $1-style refs against the actual match so the
// suggestion reads "is a" rather than the raw "is $1" template.
func suggestionFor(r Rule, match string) string {
	if isFlagOnly(r.Replacement) {
		if r.Note != "" {
			return r.Note
		}
		return "[rewrite manually]"
	}
	if r.Replacement == "" {
		if r.Note != "" {
			return "[remove] " + r.Note
		}
		return "[remove]"
	}
	if strings.Contains(r.Replacement, "$") {
		if loc := r.re.FindStringSubmatchIndex(match); loc != nil {
			return string(r.re.ExpandString(nil, r.Replacement, match, loc))
		}
	}
	return r.Replacement
}

// lineCol converts a byte offset into 1-based line and column numbers.
func lineCol(text string, off int) (int, int) {
	if off > len(text) {
		off = len(text)
	}
	line := 1
	col := 1
	for i := 0; i < off; i++ {
		if text[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

// tallyCounts groups findings by category, seeding every known category at 0 so
// the JSON "counts" object is always complete and stable.
func tallyCounts(findings []Finding) map[string]int {
	counts := map[string]int{}
	for _, c := range categoryList() {
		counts[c] = 0
	}
	for _, f := range findings {
		counts[f.Category]++
	}
	return counts
}

// tallyScore sums severity weights. Rule-driven findings use their rule weight;
// structural findings carry an implicit weight of 2.
func tallyScore(findings []Finding, rules []Rule) int {
	weightByCat := map[string]int{}
	for _, r := range rules {
		if _, ok := weightByCat[r.Category]; !ok {
			weightByCat[r.Category] = r.Weight
		}
	}
	total := 0
	for _, f := range findings {
		if w, ok := weightByCat[f.Category]; ok {
			total += w
		} else {
			total += 2 // structural default
		}
	}
	return total
}

// sentenceSplit is a coarse sentence splitter used by structural heuristics.
var sentenceSplit = regexp.MustCompile(`[.!?]+\s+`)
