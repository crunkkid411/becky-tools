package main

// sentences.go - the transcript as whole sentences (Jordan, 2026-10-11: "transcript line
// breaks at whole sentences"). "segments" stay caption-sized (cut at a 0.6 s pause or 80
// characters, which can split a sentence in half) because subtitles need short lines;
// "sentences" is the same words cut where a sentence ends, for reading and for anything
// that reasons about what was said.

import "strings"

// sentenceGap ends an unpunctuated run: a pause this long is a new thought even when the
// recognizer wrote no full stop.
const sentenceGap = 1.5

// notAnEnd are words whose full stop does not end a sentence.
var notAnEnd = map[string]bool{"mr.": true, "mrs.": true, "ms.": true, "dr.": true, "st.": true,
	"jr.": true, "sr.": true, "vs.": true, "mt.": true}

// endsSentence says whether a word closes a sentence (pure).
func endsSentence(w string) bool {
	w = strings.TrimRight(strings.TrimSpace(w), `"')]”’`)
	if w == "" || notAnEnd[strings.ToLower(w)] {
		return false
	}
	if len(w) == 2 && w[1] == '.' && w[0] >= 'A' && w[0] <= 'Z' { // an initial: "J."
		return false
	}
	switch w[len(w)-1] {
	case '.', '?', '!':
		return true
	}
	return strings.HasSuffix(w, "…")
}

// sentencize cuts words into sentences: at the end of a sentence, when the speaker
// changes, or at a long pause. Words inside a segment the VAD gate dropped stay out (pure).
func sentencize(words []Word, dropped []DroppedSegment) []Segment {
	out := []Segment{}
	var cur []string
	var start, end float64
	speaker := ""
	flush := func() {
		if len(cur) > 0 {
			out = append(out, Segment{Start: round3(start), End: round3(end), Text: strings.Join(cur, " "), Speaker: speaker})
			cur = nil
		}
	}
	for _, w := range words {
		mid := (w.Start + w.End) / 2
		skip := false
		for _, d := range dropped {
			if mid >= d.Start && mid <= d.End {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		if len(cur) > 0 && (w.Speaker != speaker || w.Start-end > sentenceGap) {
			flush()
		}
		if len(cur) == 0 {
			start, speaker = w.Start, w.Speaker
		}
		cur = append(cur, w.Word)
		end = w.End
		if endsSentence(w.Word) {
			flush()
		}
	}
	flush()
	return out
}
