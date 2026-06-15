package hum

import (
	"fmt"
	"math"
)

// Key-aware suggestions (SPEC §3 stage 5 — the headline, the part Auto-Tune does
// NOT do). For each transcribed note, given the detected key/scale, becky produces
// a DECISION, not a silent correction: in-key notes are kept; ambiguous/off-key
// notes get an explainable suggestion fusing three independent signals — nearest
// scale tone, nearest chord tone, and melodic continuity. Corroborate-then-conclude:
// >=2 of the three agree => confident suggestion; they disagree => present
// candidates, never auto-pick. Pure, deterministic; the most important thing to test.

// SuggestOptions are the cents thresholds that set how eagerly becky flags notes
// (SPEC §3 step 2; local agent tunes these on real off-key singing).
type SuggestOptions struct {
	OnToneCents    float64 // <= this from a scale tone => in-key, no suggestion (~25)
	AmbiguousCents float64 // <= this => ambiguous (between two tones, ~75); above => out-of-key
}

// DefaultSuggestOptions are the SPEC's starting temperament (~25 / ~75 cents).
func DefaultSuggestOptions() SuggestOptions {
	return SuggestOptions{OnToneCents: 25, AmbiguousCents: 75}
}

// Suggest annotates each note in-place with in-key/distance/suggestion/needsReview
// and a fused per-note confidence, and returns the corrections-log seed (one
// Correction row per note that carries a suggestion) for the preference-learning
// substrate. composeKey is the detected key (e.g. "F#m"); chordTones are MIDI notes
// of the implied chord (optional — empty => scale tones only).
func Suggest(notes []Note, composeKey string, chordTones []int, opt SuggestOptions) []Correction {
	scalePCs := ScaleTonesPC(composeKey)
	if len(scalePCs) == 0 {
		return nil
	}
	var corrections []Correction
	for i := range notes {
		if c := annotateNote(notes, i, scalePCs, chordTones, opt); c != nil {
			corrections = append(corrections, *c)
		}
	}
	return corrections
}

// annotateNote classifies one note and, when off-tone, attaches a suggestion. It
// returns a Correction row (auto value = becky's pick, Corrected = nil until Jordan
// edits) when a suggestion was made, else nil.
func annotateNote(notes []Note, i int, scalePCs, chordTones []int, opt SuggestOptions) *Correction {
	n := &notes[i]
	scaleMidi, dist := nearestInSet(n.Midi, scalePCs)
	n.DistanceCents = round2(dist)
	switch {
	case dist <= opt.OnToneCents:
		n.InKey = true
		n.NeedsReview = false
		n.Confidence = fuseConfidence(n.Confidence, dist, true, opt)
		return nil
	case dist <= opt.AmbiguousCents:
		applySuggestion(notes, i, scaleMidi, scalePCs, chordTones, "ambiguous", opt)
	default:
		applySuggestion(notes, i, scaleMidi, scalePCs, chordTones, "out-of-key", opt)
	}
	return correctionFor(*n, scaleMidi)
}

// applySuggestion builds the explainable suggestion by fusing nearest scale tone,
// nearest chord tone, and melodic continuity, then sets the note's review state and
// confidence. The winner is the candidate with the most agreeing signals (>=2 =>
// confident); ties fall back to the nearest scale tone (deterministic).
func applySuggestion(notes []Note, i, scaleMidi int, scalePCs, chordTones []int, band string, opt SuggestOptions) {
	n := &notes[i]
	cands := suggestionCandidates(notes, i, scaleMidi, scalePCs, chordTones)
	best, votes, why := pickSuggestion(cands)
	alts := altList(cands, best)
	n.InKey = false
	n.Suggestion = &Suggestion{Midi: best, Name: NoteName(best), Reason: why, Alts: alts}
	confident := votes >= 2
	n.NeedsReview = !confident || band == "ambiguous"
	n.Confidence = fuseConfidence(n.Confidence, n.DistanceCents, confident, opt)
}

// candidate is one proposed target with a label for the reason string.
type candidate struct {
	midi  int
	label string
}

// suggestionCandidates gathers the three independent signals for note i.
func suggestionCandidates(notes []Note, i, scaleMidi int, scalePCs, chordTones []int) []candidate {
	out := []candidate{{scaleMidi, "nearest scale tone"}}
	if chord := nearestChordTone(notes[i].Midi, chordTones); chord >= 0 {
		out = append(out, candidate{chord, "nearest chord tone of the implied chord"})
	}
	if cont := continuityTarget(notes, i, scalePCs); cont >= 0 {
		out = append(out, candidate{cont, "best continues the melodic line"})
	}
	return out
}

// pickSuggestion returns the candidate MIDI with the most votes (signals landing on
// the same pitch), the vote count, and a reason. Deterministic: scans candidates in
// fixed order; ties keep the earliest (scale tone first).
func pickSuggestion(cands []candidate) (best, votes int, reason string) {
	counts := map[int]int{}
	for _, c := range cands {
		counts[c.midi]++
	}
	best, votes = cands[0].midi, counts[cands[0].midi]
	for _, c := range cands[1:] {
		if counts[c.midi] > votes {
			best, votes = c.midi, counts[c.midi]
		}
	}
	var agree []string
	for _, c := range cands {
		if c.midi == best {
			agree = append(agree, c.label)
		}
	}
	if votes >= 2 {
		reason = fmt.Sprintf("%s agree on %s (corroborated)", joinReasons(agree), NoteName(best))
	} else {
		reason = fmt.Sprintf("%s; other signals disagree — review", cands[0].label)
	}
	return best, votes, reason
}

// altList returns the distinct non-winning candidate pitches (the runner-up
// options the producer can pick instead).
func altList(cands []candidate, best int) []int {
	seen := map[int]bool{best: true}
	var out []int
	for _, c := range cands {
		if !seen[c.midi] {
			seen[c.midi] = true
			out = append(out, c.midi)
		}
	}
	return out
}

// nearestInSet returns the MIDI note (nearest octave to p) on the nearest pitch
// class in pcs, and the cents distance to it.
func nearestInSet(p int, pcs []int) (int, float64) {
	bestPC, bestCents := pcs[0], math.MaxFloat64
	pPC := ((p % 12) + 12) % 12
	for _, pc := range pcs {
		if d := CentsBetween(pPC, pc); d < bestCents {
			bestCents, bestPC = d, pc
		}
	}
	return snapToPC(p, bestPC), bestCents
}

// nearestChordTone returns the nearest chord-tone MIDI to p, or -1 if no chord
// tones are supplied.
func nearestChordTone(p int, chordTones []int) int {
	if len(chordTones) == 0 {
		return -1
	}
	pcs := make([]int, len(chordTones))
	for i, m := range chordTones {
		pcs[i] = ((m % 12) + 12) % 12
	}
	snap, _ := nearestInSet(p, pcs)
	return snap
}

// continuityTarget picks the in-scale note nearest p that best continues the local
// contour: it extends the direction of the previous interval (a leap that resolves
// stepwise). Returns -1 when there's no previous note. Deterministic.
func continuityTarget(notes []Note, i int, scalePCs []int) int {
	if i == 0 {
		return -1
	}
	prev := notes[i-1].Midi
	dir := sign(notes[i].Midi - prev)
	bestMidi, bestScore := -1, math.MaxFloat64
	for _, pc := range scalePCs {
		cand := snapToPC(notes[i].Midi, pc)
		if dir != 0 && sign(cand-prev) != dir {
			continue
		}
		if score := math.Abs(float64(cand - notes[i].Midi)); score < bestScore {
			bestScore, bestMidi = score, cand
		}
	}
	return bestMidi
}

// snapToPC returns the MIDI note with pitch class pc that is closest to p.
func snapToPC(p, pc int) int {
	base := p - (((p % 12) + 12) % 12) + pc // same octave, target pc
	best, bestD := base, absInt(base-p)
	for _, c := range []int{base - 12, base + 12} {
		if d := absInt(c - p); d < bestD {
			best, bestD = c, d
		}
	}
	return best
}

// fuseConfidence blends model confidence with distance-to-scale and signal
// agreement into the single 0..1 per-note number (SPEC §3 step 3): an in-key or
// corroborated note keeps high confidence; a lone off-key note is pulled down.
func fuseConfidence(modelConf, distCents float64, agree bool, opt SuggestOptions) float64 {
	distScore := 1 - clamp01(distCents/(opt.AmbiguousCents*2))
	agreeBonus := 0.0
	if agree {
		agreeBonus = 0.15
	}
	c := 0.55*clamp01(modelConf) + 0.30*distScore + agreeBonus
	return round2(clamp01(c))
}

// correctionFor seeds a corrections-log row: becky's auto pitch vs Jordan's (nil
// until he edits), with the musical context a preference model learns from.
func correctionFor(n Note, autoMidi int) *Correction {
	return &Correction{
		NoteIndex: n.I,
		Field:     "note.midi",
		Auto:      float64(autoMidi),
		Corrected: nil,
		Reason:    suggestReason(n),
		Context: map[string]interface{}{
			"detectedMidi":  n.Midi,
			"distanceCents": n.DistanceCents,
			"onsetSec":      n.OnsetSec,
			"confidence":    n.Confidence,
			"needsReview":   n.NeedsReview,
		},
	}
}

func suggestReason(n Note) string {
	if n.Suggestion != nil {
		return n.Suggestion.Reason
	}
	return ""
}

func sign(x int) int {
	switch {
	case x > 0:
		return 1
	case x < 0:
		return -1
	default:
		return 0
	}
}

func joinReasons(rs []string) string {
	out := ""
	for i, r := range rs {
		if i > 0 {
			out += " + "
		}
		out += r
	}
	return out
}
