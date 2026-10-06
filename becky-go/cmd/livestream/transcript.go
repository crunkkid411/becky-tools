package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
)

// Word is one word of becky-transcribe's JSON (only the fields used here).
type Word struct {
	Word   string  `json:"word"`
	Start  float64 `json:"start"`
	End    float64 `json:"end"`
	Source string  `json:"source,omitempty"`
}

// Sentence is a run of words the model decides about as one unit. W0..W1 are
// inclusive word indices.
type Sentence struct {
	ID    int     `json:"id"`
	W0    int     `json:"w0"`
	W1    int     `json:"w1"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

const (
	sentenceGap      = 1.2 // a pause this long ends a sentence even without punctuation
	sentenceMaxWords = 50  // run-ons are split so one decision never covers a minute
)

func loadWords(path string) ([]Word, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var t struct {
		Words []Word `json:"words"`
	}
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("transcript %s: %w", path, err)
	}
	if len(t.Words) == 0 {
		return nil, fmt.Errorf("transcript %s has no words", path)
	}
	return t.Words, nil
}

// endsSentence: the word closes with . ? or ! (quotes and brackets after it allowed).
func endsSentence(w string) bool {
	w = strings.TrimRight(w, `"')]`)
	return strings.HasSuffix(w, ".") || strings.HasSuffix(w, "?") || strings.HasSuffix(w, "!")
}

// splitSentences cuts the word list at sentence punctuation, at long pauses, and
// every sentenceMaxWords words.
func splitSentences(words []Word) []Sentence {
	var out []Sentence
	start := 0
	flush := func(end int) {
		if end < start {
			return
		}
		var b strings.Builder
		for i := start; i <= end; i++ {
			if i > start {
				b.WriteByte(' ')
			}
			b.WriteString(strings.TrimSpace(words[i].Word))
		}
		out = append(out, Sentence{ID: len(out), W0: start, W1: end,
			Start: words[start].Start, End: words[end].End, Text: b.String()})
		start = end + 1
	}
	for i := range words {
		last := i == len(words)-1
		switch {
		case last:
			flush(i)
		case endsSentence(words[i].Word),
			words[i+1].Start-words[i].End >= sentenceGap,
			i-start+1 >= sentenceMaxWords:
			flush(i)
		}
	}
	return out
}

// clock formats seconds as m:ss (or h:mm:ss) for prompts and reports.
func clock(t float64) string {
	s := int(math.Max(0, t))
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
