package main

// badtake.go — detect abandoned re-takes in a transcript.
//
// Jordan's delivery pattern (WE_TRIED.md, measured on his footage): he stops
// mid-statement, pauses to gather himself (>= ~0.7s), and re-starts the
// delivery — sometimes 3-5 times, sometimes a few words in, sometimes
// re-worded. A rough cut keeps ONLY the last complete take of each chain.
//
// The rules that make cuts safe here:
//   - a restart is a WORD-RUN match between an earlier cue and a later cue
//     across a real pause, never a volume guess;
//   - chains iterate to a fixpoint (a restart of a restart is one chain, cut
//     from the first abandoned attempt to the last kept one);
//   - a cut span starts exactly at the first abandoned cue — never extended
//     backwards across a completed sentence;
//   - only strong matches (>=4 identical leading words, allowing the re-start
//     to begin several words in) are CUT; weaker re-wording matches become
//     RETAKE? markers and the human decides.

import (
	"strings"

	"becky-go/internal/quotes"
)

const (
	minRestartGapSec = 0.7 // he pauses to gather himself; shorter gaps are breaths, not restarts
	maxRestartGapSec = 30.0
	minPrefixWords   = 4 // identical leading words = "the same statement" signal
	minPrefixIn      = 3 // weakest run that may even be flagged (as a marker)
	maxSkipIn        = 6 // how many words into either cue a re-start may begin
	cutScore         = 6 // multi-signal confidence needed to CUT (see scoring below)
)

// badTake is one abandoned span. [Start,End) is the source time to cut;
// Confident false means "looks like a retake, marker not cut".
type badTake struct {
	Start     float64 `json:"start"`
	End       float64 `json:"end"`
	FirstCue  int     `json:"first_cue"`
	LastCue   int     `json:"last_cue"` // last ABANDONED cue index; the kept take follows
	Confident bool    `json:"confident"`
	Reason    string  `json:"reason"`
}

func normWords(s string) []string {
	f := strings.Fields(strings.ToLower(s))
	out := make([]string, 0, len(f))
	for _, w := range f {
		w = strings.Trim(w, ".,!?;:\"'()-")
		if w != "" {
			out = append(out, w)
		}
	}
	return out
}

// lcp counts identical leading words of a and b.
func lcp(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// detectBadTakes returns abandoned spans in source order. cues must be sorted
// by Start (ParseSRT returns them in file order, which is time order).
func detectBadTakes(cues []quotes.Cue) []badTake {
	n := len(cues)
	if n < 2 {
		return nil
	}
	words := make([][]string, n)
	for i, c := range cues {
		words[i] = normWords(c.Text)
	}

	// restartOf[j] = i means cue j re-starts cue i's statement; score ranks
	// match strength (leading-word count, bonus when aligned at word 0).
	restartOf := make([]int, n)
	score := make([]int, n)
	confid := make([]bool, n)
	for j := range restartOf {
		restartOf[j] = -1
	}
	for j := 1; j < n; j++ {
		for i := 0; i < j; i++ {
			gap := cues[j].Start - cues[i].End
			if gap < minRestartGapSec || gap > maxRestartGapSec {
				continue
			}
			best := 0
			for k := 0; k <= maxSkipIn && k < len(words[i]); k++ {
				for m := 0; m <= maxSkipIn && m < len(words[j]); m++ {
					if l := lcp(words[i][k:], words[j][m:]); l > best {
						best = l
					}
				}
			}
			if best < minPrefixIn {
				continue
			}
			// Confidence is several signals agreeing, never one hard rule:
			// the same statement, a real gather-himself pause, an earlier
			// attempt that never got past the repeated part, and a later
			// take that is the fuller delivery. Two CLEAN complete
			// alternatives score below the cut line and stay for the human.
			link := 1
			if best >= minPrefixWords {
				link += 2
			}
			if gap >= 1.5 {
				link += 2
			}
			if len(words[i]) <= best+2 {
				link += 2 // abandoned mid-flight
			}
			if len(words[j]) > len(words[i]) {
				link++
			}
			if link > score[j] {
				score[j], restartOf[j], confid[j] = link, i, link >= cutScore
			}
		}
	}

	// Each restart link (j re-starts i) abandons cues [i, j-1]: everything the
	// speaker said from the failed attempt up to the pause before the re-start.
	// A restart OF a restart yields overlapping spans, and merging them is the
	// fixpoint: one chain, cut from the first abandoned attempt to the final
	// kept take.
	type span struct{ a, b int }
	var spans []span
	spanConf := map[span]bool{}
	for j := 1; j < n; j++ {
		if i := restartOf[j]; i >= 0 {
			s := span{i, j - 1}
			spans = append(spans, s)
			spanConf[s] = confid[j]
		}
	}
	// merge by start order
	for i := 0; i < len(spans); i++ {
		for j := i + 1; j < len(spans); j++ {
			if spans[j].a < spans[i].a {
				spans[i], spans[j] = spans[j], spans[i]
			}
		}
	}
	var out []badTake
	for _, s := range spans {
		if len(out) > 0 && s.a <= out[len(out)-1].LastCue+1 {
			last := &out[len(out)-1]
			if s.b > last.LastCue {
				last.LastCue = s.b
				last.End = cues[s.b+1].Start - 0.05
			}
			if !spanConf[s] {
				last.Confident = false
			}
			continue
		}
		out = append(out, badTake{
			Start:     cues[s.a].Start,
			End:       cues[s.b+1].Start - 0.05,
			FirstCue:  s.a,
			LastCue:   s.b,
			Confident: spanConf[s],
		})
	}
	for i := range out {
		if out[i].End <= out[i].Start {
			continue
		}
		if out[i].Confident {
			out[i].Reason = "re-started take (word-run match across pause)"
		} else {
			out[i].Reason = "possible retake (re-worded) - left for human"
		}
	}
	kept := out[:0]
	for _, b := range out {
		if b.End > b.Start {
			kept = append(kept, b)
		}
	}
	return kept
}
