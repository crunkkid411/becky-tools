package main

// Niche word list (2026-10-05): fixes names and slang the speech models
// mishear ("Harry Jordan" -> "Hair Jordan"). Deterministic and TEXT ONLY: a
// fix never moves a timestamp, so every word keeps the time the audio gave it.

import (
	_ "embed"
	"os"
	"sort"
	"strings"
	"unicode"
)

//go:embed lexicon.txt
var defaultLexicon string

type lexFix struct {
	heard []string // normalized words to match
	meant []string // replacement words, exact spelling
}

// LexiconFix is the audit trail entry for one applied fix.
type LexiconFix struct {
	Start float64 `json:"start"`
	Heard string  `json:"heard"`
	Meant string  `json:"meant"`
}

// parseLexicon reads "heard => meant" lines. Malformed lines are returned in
// bad (reported, never silently dropped).
func parseLexicon(src string) (fixes []lexFix, bad []string) {
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=>", 2)
		if len(parts) != 2 {
			bad = append(bad, line)
			continue
		}
		var heard []string
		for _, w := range strings.Fields(parts[0]) {
			if n := normWord(w); n != "" {
				heard = append(heard, n)
			}
		}
		meant := strings.Fields(parts[1])
		if len(heard) == 0 || len(meant) == 0 || (len(meant) != 1 && len(meant) != len(heard)) {
			bad = append(bad, line)
			continue
		}
		fixes = append(fixes, lexFix{heard: heard, meant: meant})
	}
	// Longest match first, so "take it back 2007" wins over any shorter fix.
	sort.SliceStable(fixes, func(i, j int) bool { return len(fixes[i].heard) > len(fixes[j].heard) })
	return fixes, bad
}

// loadLexicon returns the built-in list plus an optional extra file.
func loadLexicon(extraPath string) (fixes []lexFix, bad []string, err error) {
	src := defaultLexicon
	if extraPath != "" {
		data, rerr := os.ReadFile(extraPath)
		if rerr != nil {
			return nil, nil, rerr
		}
		src += "\n" + string(data)
	}
	fixes, bad = parseLexicon(src)
	return fixes, bad, nil
}

// splitPunct returns a word's leading and trailing non-alphanumeric runs.
func splitPunct(w string) (pre, suf string) {
	r := []rune(w)
	i, j := 0, len(r)
	for i < j && !unicode.IsLetter(r[i]) && !unicode.IsDigit(r[i]) {
		i++
	}
	for j > i && !unicode.IsLetter(r[j-1]) && !unicode.IsDigit(r[j-1]) {
		j--
	}
	return string(r[:i]), string(r[j:])
}

// applyLexicon applies the fixes to the words' text. Timestamps never move: a
// word-for-word swap keeps each word's times; a merge spans first start to last
// end. The original punctuation around the matched words is kept.
func applyLexicon(words []Word, fixes []lexFix) ([]Word, []LexiconFix) {
	if len(fixes) == 0 {
		return words, nil
	}
	out := make([]Word, 0, len(words))
	var log []LexiconFix
	for i := 0; i < len(words); {
		fix, ok := matchFix(words, i, fixes)
		if !ok {
			out = append(out, words[i])
			i++
			continue
		}
		n := len(fix.heard)
		heardText := wordsText(words[i : i+n])
		pre, _ := splitPunct(words[i].Word)
		_, suf := splitPunct(words[i+n-1].Word)
		if len(fix.meant) == 1 {
			w := words[i]
			w.Word = pre + fix.meant[0] + suf
			w.End = words[i+n-1].End
			w.Confidence = minConfidence(words[i : i+n])
			out = append(out, w)
		} else {
			for k := 0; k < n; k++ {
				w := words[i+k]
				p, s := splitPunct(w.Word)
				w.Word = p + fix.meant[k] + s
				out = append(out, w)
			}
		}
		log = append(log, LexiconFix{Start: words[i].Start, Heard: heardText, Meant: pre + strings.Join(fix.meant, " ") + suf})
		i += n
	}
	return out, log
}

func matchFix(words []Word, i int, fixes []lexFix) (lexFix, bool) {
	for _, f := range fixes {
		if i+len(f.heard) > len(words) {
			continue
		}
		ok := true
		for k, h := range f.heard {
			if normWord(words[i+k].Word) != h {
				ok = false
				break
			}
		}
		if ok {
			return f, true
		}
	}
	return lexFix{}, false
}

func minConfidence(ws []Word) *float64 {
	var m *float64
	for _, w := range ws {
		if w.Confidence != nil && (m == nil || *w.Confidence < *m) {
			v := *w.Confidence
			m = &v
		}
	}
	return m
}
