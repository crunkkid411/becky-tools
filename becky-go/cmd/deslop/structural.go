// structural.go — document-level AI-tell heuristics that no single regex can
// catch. These are detect-only: they report Findings but never rewrite text,
// because the fix requires editorial judgement.
//
// Covers task categories:
//  5. Structural monotony   — 5+ consecutive sentences within a tight word band.
//  13. Synonym cycling       — 3+ synonyms for one concept in a single paragraph.
//  16. Rule-of-three overuse — repeated "X, Y, and Z" tricolons.
package main

import (
	"regexp"
	"strings"
)

const (
	monotonyWindow    = 5 // consecutive sentences to inspect
	monotonyMaxSpread = 5 // flag when the band is within this many words
	synonymThreshold  = 3 // distinct synonyms of one concept in a paragraph
	ruleOfThreeMax    = 2 // flag the 3rd+ tricolon in the document
)

// structuralFindings runs all document-level heuristics. Skip zones are honoured
// by checking each reported offset against them.
func structuralFindings(text string, skip []skipRange) []Finding {
	var out []Finding
	out = append(out, monotonyFindings(text, skip)...)
	out = append(out, synonymCyclingFindings(text, skip)...)
	out = append(out, ruleOfThreeFindings(text, skip)...)
	return out
}

// sentenceSpan holds one sentence's text and its byte offset in the document.
type sentenceSpan struct {
	text  string
	start int
	words int
}

// splitSentences returns sentence spans with byte offsets, skipping protected
// zones. Coarse splitter on ./!/? followed by whitespace.
func splitSentences(text string, skip []skipRange) []sentenceSpan {
	var spans []sentenceSpan
	idx := sentenceSplit.FindAllStringIndex(text, -1)
	bounds := make([]int, 0, len(idx)+1)
	for _, m := range idx {
		bounds = append(bounds, m[1])
	}
	bounds = append(bounds, len(text))
	prev := 0
	for _, b := range bounds {
		seg := text[prev:b]
		trimmed := strings.TrimSpace(seg)
		if trimmed != "" && !inSkip(prev, b, skip) {
			spans = append(spans, sentenceSpan{text: trimmed, start: prev, words: countWords(trimmed)})
		}
		prev = b
	}
	return spans
}

// monotonyFindings flags windows of consecutive sentences whose word counts are
// all within monotonyMaxSpread of each other.
func monotonyFindings(text string, skip []skipRange) []Finding {
	spans := splitSentences(text, skip)
	if len(spans) < monotonyWindow {
		return nil
	}
	var out []Finding
	flagged := map[int]bool{}
	for i := 0; i+monotonyWindow <= len(spans); i++ {
		minW, maxW := spans[i].words, spans[i].words
		for j := i; j < i+monotonyWindow; j++ {
			if spans[j].words < minW {
				minW = spans[j].words
			}
			if spans[j].words > maxW {
				maxW = spans[j].words
			}
		}
		if maxW-minW <= monotonyMaxSpread && minW >= 4 && !flagged[i] {
			flagged[i] = true
			line, col := lineCol(text, spans[i].start)
			out = append(out, Finding{
				Category:   catMonotony,
				Line:       line,
				Col:        col,
				Match:      truncate(spans[i].text, 40),
				Suggestion: "vary sentence length; 5 sentences here are nearly the same length",
			})
		}
	}
	return out
}

// synonymGroups: clusters where 3+ distinct members in one paragraph signal AI
// synonym cycling. The fix is to repeat one word.
var synonymGroups = [][]string{
	{"challenge", "challenges", "obstacle", "obstacles", "hurdle", "hurdles", "difficulty", "difficulties"},
	{"issue", "issues", "problem", "problems", "defect", "defects", "anomaly", "anomalies", "bug", "bugs"},
	{"important", "crucial", "vital", "essential", "critical", "pivotal", "key"},
	{"use", "utilize", "leverage", "employ", "harness"},
	{"create", "build", "construct", "craft", "forge", "establish"},
	{"improve", "enhance", "elevate", "boost", "optimize", "refine"},
	{"show", "showcase", "demonstrate", "illustrate", "highlight", "underscore"},
}

// synonymCyclingFindings flags paragraphs with 3+ distinct members of one group.
func synonymCyclingFindings(text string, skip []skipRange) []Finding {
	var out []Finding
	for _, para := range paragraphs(text) {
		if inSkip(para.start, para.start+len(para.text), skip) {
			continue
		}
		words := wordSet(strings.ToLower(para.text))
		for _, group := range synonymGroups {
			distinct := map[string]bool{}
			for _, syn := range group {
				if words[syn] {
					distinct[canonical(syn)] = true
				}
			}
			if len(distinct) >= synonymThreshold {
				line, col := lineCol(text, para.start)
				out = append(out, Finding{
					Category:   catSynonymCycling,
					Line:       line,
					Col:        col,
					Match:      truncate(strings.TrimSpace(para.text), 50),
					Suggestion: "pick one word and repeat it instead of cycling synonyms",
				})
				break
			}
		}
	}
	return out
}

// canonical strips a trailing plural so plural/singular count as one synonym.
func canonical(w string) string {
	switch {
	case strings.HasSuffix(w, "ies"):
		return strings.TrimSuffix(w, "ies") + "y"
	case strings.HasSuffix(w, "es"):
		return strings.TrimSuffix(w, "es")
	case strings.HasSuffix(w, "s"):
		return strings.TrimSuffix(w, "s")
	}
	return w
}

// ruleOfThree matches a tricolon: "a, b, and c" / "a, b, or c".
var ruleOfThree = regexp.MustCompile(`(?i)\b[\w'-]+,\s+[\w'-]+,?\s+(?:and|or)\s+[\w'-]+\b`)

// ruleOfThreeFindings flags overuse (3+ tricolons in the document).
func ruleOfThreeFindings(text string, skip []skipRange) []Finding {
	locs := ruleOfThree.FindAllStringIndex(text, -1)
	var hits [][2]int
	for _, m := range locs {
		if !inSkip(m[0], m[1], skip) {
			hits = append(hits, [2]int{m[0], m[1]})
		}
	}
	if len(hits) <= ruleOfThreeMax {
		return nil
	}
	var out []Finding
	for _, h := range hits[ruleOfThreeMax:] {
		line, col := lineCol(text, h[0])
		out = append(out, Finding{
			Category:   catRuleOfThree,
			Line:       line,
			Col:        col,
			Match:      truncate(text[h[0]:h[1]], 50),
			Suggestion: "rule-of-three overused; vary list structure or cut a clause",
		})
	}
	return out
}

// paragraph holds a paragraph's text and byte offset.
type paragraph struct {
	text  string
	start int
}

// paragraphs splits text on blank lines, tracking byte offsets.
func paragraphs(text string) []paragraph {
	var out []paragraph
	start := 0
	i := 0
	n := len(text)
	for i < n {
		if text[i] == '\n' {
			j := i + 1
			for j < n && (text[j] == ' ' || text[j] == '\t' || text[j] == '\r') {
				j++
			}
			if j < n && text[j] == '\n' {
				if seg := text[start:i]; strings.TrimSpace(seg) != "" {
					out = append(out, paragraph{text: seg, start: start})
				}
				start = j + 1
				i = j + 1
				continue
			}
		}
		i++
	}
	if seg := text[start:]; strings.TrimSpace(seg) != "" {
		out = append(out, paragraph{text: seg, start: start})
	}
	return out
}

// countWords counts whitespace-delimited tokens that contain a letter or digit.
func countWords(s string) int {
	n := 0
	for _, f := range strings.Fields(s) {
		if hasAlnum(f) {
			n++
		}
	}
	return n
}

// wordSet returns the set of lowercase alphanumeric word tokens in s.
func wordSet(s string) map[string]bool {
	set := map[string]bool{}
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '\'' && r != '-'
	})
	for _, f := range fields {
		if f != "" {
			set[f] = true
		}
	}
	return set
}

// hasAlnum reports whether s contains at least one letter or digit.
func hasAlnum(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return true
		}
	}
	return false
}

// truncate shortens s to n runes with an ellipsis when needed.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
