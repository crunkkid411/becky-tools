// segment.go — sentence segmentation over the transcript, each sentence mapped
// to the cue range that contains it (SPEC §5.1). This is the unit the recursive
// context-expansion loop grows/shrinks: "include the prior/next SENTENCE."
//
// ASR text (Parakeet) carries punctuation + casing, so terminal-punctuation
// segmentation is feasible — but imperfect (run-ons, missing terminals, "...",
// decimals, abbreviations). We guard the common abbreviations and decimals, and
// expose SegmentationPoor so the caller can fall back to cue-level units
// (one sentence == one cue) when the text clearly lacks sentence structure
// (SPEC §13.3).
package quotes

import "strings"

// Sentence is a contiguous run of transcript text mapped to a cue range. A
// sentence may begin mid-cue and end mid-cue; FirstCue/LastCue are the enclosing
// source cues, which is what region snapping (SPEC §6) consumes.
type Sentence struct {
	Text     string // the sentence's spoken text (cleaned, verbatim words)
	FirstCue int    // index into cues of the cue holding the first word
	LastCue  int    // index into cues of the cue holding the last word
}

// abbreviations are tokens whose trailing "." must NOT end a sentence. Lowercased
// for comparison. Kept deliberately small/common — over-guarding hurts recall of
// real boundaries more than the occasional false split.
var abbreviations = map[string]bool{
	"mr": true, "mrs": true, "ms": true, "dr": true, "prof": true, "sr": true,
	"jr": true, "st": true, "vs": true, "etc": true, "inc": true, "ltd": true,
	"co": true, "no": true, "fig": true, "al": true, "ave": true, "blvd": true,
	"dept": true, "gov": true, "sen": true, "rep": true, "u.s": true, "u.k": true,
	"a.m": true, "p.m": true, "i.e": true, "e.g": true,
}

// tok is one word tagged with the source cue it came from.
type tok struct {
	word string
	cue  int
}

// Segment splits cues into sentences with cue ranges. It walks the concatenated
// text token by token (preserving which cue each token came from) and closes a
// sentence on terminal punctuation .?! that is NOT a guarded abbreviation or a
// decimal point between digits. Any trailing words with no terminal form a final
// sentence. Cues with empty text are skipped for word emission but never break a
// sentence.
func Segment(cues []Cue) []Sentence {
	var toks []tok
	for i, c := range cues {
		for _, w := range strings.Fields(c.Text) {
			toks = append(toks, tok{word: w, cue: i})
		}
	}
	if len(toks) == 0 {
		return nil
	}

	var sentences []Sentence
	var words []string
	first := toks[0].cue
	last := toks[0].cue
	flush := func() {
		if len(words) == 0 {
			return
		}
		sentences = append(sentences, Sentence{
			Text:     strings.Join(words, " "),
			FirstCue: first,
			LastCue:  last,
		})
		words = nil
	}

	for i, t := range toks {
		if len(words) == 0 {
			first = t.cue
		}
		words = append(words, t.word)
		last = t.cue
		next := ""
		if i+1 < len(toks) {
			next = toks[i+1].word
		}
		if endsSentence(t.word, next) {
			flush()
		}
	}
	flush()
	return sentences
}

// endsSentence reports whether word terminates a sentence. next is the following
// word (or "") used to allow a decimal point / version-number continuation check.
func endsSentence(word, next string) bool {
	trimmed := strings.TrimRight(word, `"'”’)]`)
	if trimmed == "" {
		return false
	}
	last := trimmed[len(trimmed)-1]
	if last != '.' && last != '!' && last != '?' {
		return false
	}
	if last == '.' {
		core := strings.TrimRight(trimmed, ".")
		// decimal like "3.14" or version "1.2" — the '.' is internal, not terminal.
		if isDigitWord(core) && isDigitStart(next) {
			return false
		}
		if abbreviations[strings.ToLower(core)] {
			return false
		}
		// single trailing initial like "J." — treat as non-terminal.
		if len(core) == 1 && isLetter(rune(core[0])) {
			return false
		}
	}
	return true
}

func isDigitWord(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isDigitStart(s string) bool {
	if s == "" {
		return false
	}
	return s[0] >= '0' && s[0] <= '9'
}

func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// SegmentationPoor reports whether sentence detection over these cues looks
// unreliable, so the caller should fall back to cue-level expansion units
// (SPEC §13.3). Heuristic: if almost no terminal punctuation exists in the source
// text, OR the average sentence spans many cues (run-on heavy), segmentation is
// "poor".
func SegmentationPoor(cues []Cue, sentences []Sentence) bool {
	if len(cues) == 0 || len(sentences) == 0 {
		return true
	}
	terminals := 0
	totalWords := 0
	for _, c := range cues {
		for _, w := range strings.Fields(c.Text) {
			totalWords++
			if endsSentence(w, "") {
				terminals++
			}
		}
	}
	if totalWords == 0 {
		return true
	}
	// Fewer than one terminal per ~60 words => effectively unpunctuated.
	if terminals == 0 || totalWords/terminals > 60 {
		return true
	}
	// Average sentence spanning more than 12 cues => run-on heavy.
	spanSum := 0
	for _, s := range sentences {
		spanSum += s.LastCue - s.FirstCue + 1
	}
	avgSpan := float64(spanSum) / float64(len(sentences))
	return avgSpan > 12
}

// cueLevelSentences is the fallback unit set: one "sentence" per non-empty cue.
// Used when SegmentationPoor is true so expansion still has well-defined
// neighbors (the cue before / the cue after).
func cueLevelSentences(cues []Cue) []Sentence {
	var out []Sentence
	for i, c := range cues {
		if strings.TrimSpace(c.Text) == "" {
			continue
		}
		out = append(out, Sentence{Text: c.Text, FirstCue: i, LastCue: i})
	}
	return out
}
