package moment

import (
	"strings"
)

// This file holds the STRUCTURAL prior: everything a moment can be scored on
// without asking a model. The deliberate boundary (see the package doc) is that
// structure is measured here and CONTENT is judged by a model — so nothing in
// this file tries to guess whether a clip is interesting, only whether it is a
// well-formed, self-contained unit of speech.
//
// Keeping it that way matters: a lexicon that tried to score "viral-ness" from
// keywords would be a lone weak signal dressed up as a measurement, which is
// exactly what FORENSIC-OUTPUT-PHILOSOPHY.md forbids.

// Weights for the structural prior. SelfContained and Payoff carry the most
// because they are the two failures that make auto-cut shorts unwatchable: a
// clip that opens mid-setup, and a clip that stops before the point lands.
const (
	wHook          = 0.20
	wPayoff        = 0.28
	wSelfContained = 0.30
	wPace          = 0.10
	wFit           = 0.12
)

// prior combines the measured signals into a 0..1 structural score.
func (s Signals) prior() float64 {
	v := wHook*s.Hook +
		wPayoff*s.Payoff +
		wSelfContained*s.SelfContained +
		wPace*s.Pace +
		wFit*s.Fit
	return clamp01(v)
}

// measure computes the structural signals for the cue range [s,e].
func measure(segs []Segment, s, e int, opt Options) Signals {
	first := strings.TrimSpace(segs[s].Text)

	var sig Signals
	sig.Hook = hookScore(segs, s, opt.ThoughtGap)
	sig.Payoff = payoffScore(segs, e, opt.ThoughtGap)
	sig.SelfContained = selfContainedScore(first)

	dur := segs[e].End - segs[s].Start
	words := 0
	for i := s; i <= e; i++ {
		words += len(strings.Fields(segs[i].Text))
	}
	if dur > 0 {
		sig.WordsPerSec = float64(words) / dur
	}
	sig.Pace = paceScore(sig.WordsPerSec)
	sig.Fit = fitScore(dur, opt)
	return sig
}

// hookScore rewards a window that ENTERS cleanly: preceded by a real pause or a
// finished sentence, and not opening on a conjunction that implies missing setup.
func hookScore(segs []Segment, s int, gap float64) float64 {
	score := 0.5
	if s == 0 {
		score = 0.8 // the very start of a recording is a clean entry by definition
	} else {
		pause := segs[s].Start - segs[s-1].End
		switch {
		case pause >= gap*2:
			score = 1.0
		case pause >= gap-gapEps:
			score = 0.8
		case endsSentence(segs[s-1].Text):
			score = 0.7
		default:
			score = 0.3
		}
	}
	if opensOnConjunction(segs[s].Text) {
		score -= 0.35
	}
	if isQuestion(segs[s].Text) {
		score += 0.15 // a question is a natural hook: it sets up its own payoff
	}
	return clamp01(score)
}

// PayoffScore is the exported form of payoffScore, for a caller that needs to
// score a completed-thought ending outside this package — e.g. becky-short
// --review, checking whether the RENDERED file's own last cue lands cleanly
// rather than re-implementing this scoring a second time.
func PayoffScore(segs []Segment, e int, gap float64) float64 { return payoffScore(segs, e, gap) }

// EndsSentence is the exported form of endsSentence, for the same reason.
func EndsSentence(text string) bool { return endsSentence(text) }

// payoffScore rewards a window that CLOSES on a completed thought.
func payoffScore(segs []Segment, e int, gap float64) float64 {
	score := 0.35
	if endsSentence(segs[e].Text) {
		score = 0.85
	}
	if e == len(segs)-1 {
		// The end of the recording completes by default.
		if score < 0.7 {
			score = 0.7
		}
		return clamp01(score)
	}
	pause := segs[e+1].Start - segs[e].End
	if pause >= gap*2 {
		score += 0.15
	} else if pause >= gap-gapEps {
		score += 0.08
	}
	if opensOnConjunction(segs[e+1].Text) {
		// The NEXT cue continues this clause -> the thought did not actually end.
		score -= 0.25
	}
	return clamp01(score)
}

// leadingReferences are openers that point at something the viewer has not seen.
// A clip beginning with one of these is the classic "starts mid-setup" failure:
// grammatical, fluent, and incomprehensible on its own.
var leadingReferences = []string{
	"that's why", "thats why", "that's the", "thats the", "that's what", "thats what",
	"this is why", "this is the", "which is why", "which means",
	"he said", "she said", "they said", "he was", "she was", "they were",
	"it was", "it's the", "its the", "those are", "these are",
	"and then he", "and then she", "and then they",
	"like i said", "as i said", "as i mentioned", "going back to",
	"the other thing", "another thing", "the second one", "the third one",
	"anyway", "so yeah", "but yeah",
}

// conjunctionOpeners imply the clause began before this cue.
var conjunctionOpeners = []string{
	"and", "but", "so", "because", "or", "nor", "yet",
	"which", "while", "whereas", "although", "though", "unless", "until",
}

// selfContainedScore penalises an opening that depends on unseen context.
//
// The back-reference checks run against the text with any leading conjunction
// STRIPPED, not against the raw opener. "So he said the whole thing was a
// write-off" is two failures stacked — it continues a prior clause AND points at
// an unintroduced "he" — and scoring only the conjunction (because "so" occupies
// the first-word slot) let the worst class of opener through at a mild penalty.
func selfContainedScore(first string) float64 {
	l := strings.ToLower(strings.TrimSpace(first))
	l = strings.TrimLeft(l, "-–— \t\"'")
	if l == "" {
		return 0.3
	}

	score := 1.0
	rest := l
	if opensOnConjunction(first) {
		score -= 0.3
		if fields := strings.Fields(l); len(fields) > 1 {
			rest = strings.Join(fields[1:], " ")
		}
	}

	for _, ref := range leadingReferences {
		if strings.HasPrefix(rest, ref) {
			score -= 0.55
			break
		}
	}
	// A bare pronoun subject with no antecedent ("he", "she", "they", "it") is
	// the same failure in miniature.
	if fields := strings.Fields(rest); len(fields) > 0 {
		switch strings.Trim(fields[0], ",.;:!?") {
		case "he", "she", "they", "it", "him", "her", "them", "this", "that", "those", "these":
			score -= 0.2
		}
	}
	return clamp01(score)
}

// opensOnConjunction reports whether the text begins with a coordinating or
// subordinating conjunction.
func opensOnConjunction(text string) bool {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(text)))
	if len(fields) == 0 {
		return false
	}
	w := strings.Trim(fields[0], ",.;:!?\"'-–—")
	for _, c := range conjunctionOpeners {
		if w == c {
			return true
		}
	}
	return false
}

// isQuestion reports whether the cue reads as a question.
func isQuestion(text string) bool {
	return strings.HasSuffix(strings.TrimRight(strings.TrimSpace(text), "\"')]"), "?")
}

// endsSentence reports whether the cue closes a sentence. Trailing quotes and
// brackets are stripped first so `he said "no."` still counts.
func endsSentence(text string) bool {
	t := strings.TrimRight(strings.TrimSpace(text), "\"')] \t")
	if t == "" {
		return false
	}
	r := rune(t[len(t)-1])
	// Multi-byte safety: re-scan for the true last rune.
	for _, c := range t {
		r = c
	}
	switch r {
	case '.', '!', '?', '…':
		// An ellipsis is a trail-off, not a completion.
		return r != '…' && !strings.HasSuffix(t, "...")
	}
	return false
}

// paceScore rewards a comfortable speaking rate. Both extremes are bad for
// short-form: too slow drags, too fast is unintelligible without rewinding.
// The plateau (2.2-4.0 w/s) covers ordinary conversational English.
func paceScore(wps float64) float64 {
	switch {
	case wps <= 0:
		return 0
	case wps < 1.2:
		return clamp01(wps / 1.2 * 0.5)
	case wps < 2.2:
		return 0.5 + (wps-1.2)/1.0*0.5
	case wps <= 4.0:
		return 1.0
	case wps <= 5.5:
		return clamp01(1.0 - (wps-4.0)/1.5*0.5)
	default:
		return 0.3
	}
}

// fitScore rewards a duration in the short-form sweet spot. It peaks in the
// middle of the configured band rather than at either edge: a clip at exactly
// MinDuration usually has no room for a payoff, and one at MaxDuration usually
// has slack that should have been trimmed.
func fitScore(dur float64, opt Options) float64 {
	lo, hi := opt.MinDuration, opt.MaxDuration
	if hi <= lo {
		return 1
	}
	if dur < lo || dur > hi+opt.ExtendBudget {
		return 0
	}
	// Peak at 40% into the band — short-form rewards brevity, so the ideal sits
	// below the midpoint.
	peak := lo + 0.4*(hi-lo)
	var d float64
	if dur <= peak {
		d = (peak - dur) / (peak - lo + 1e-9)
	} else {
		d = (dur - peak) / (hi + opt.ExtendBudget - peak + 1e-9)
	}
	return clamp01(1 - d*0.8)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
