// Package moment finds self-contained micro-stories ("moments") in a transcript —
// the hook/build/payoff windows that make a short-form clip work — and emits them
// as tight [in,out] windows on the SOURCE timeline.
//
// The split of responsibility here is load-bearing, and it is becky's
// corroborate-then-conclude rule applied to clip selection:
//
//   - THIS package decides STRUCTURE, deterministically and with no model: where a
//     thought starts, whether it completes, whether the window opens cleanly or
//     dangles mid-setup, and whether the pace/length fit short-form. Structure is
//     measurable from cue times and punctuation, so it must not be guessed by a
//     model.
//   - A MODEL decides CONTENT (is this actually interesting?) via the Judge
//     interface in judge.go. That is a genuinely fuzzy call and is the only part
//     that should cost a token.
//
// Two independent signals -> a conclusion. A candidate backed by ONLY the
// structural prior is reported as a candidate, never as a conclusion
// (FORENSIC-OUTPUT-PHILOSOPHY.md). With no judge available the tool still works
// and says so, rather than silently ranking on half the evidence.
//
// Everything here is pure: times in, windows out. No I/O, no exec, no model.
package moment

import (
	"math"
	"sort"
	"strings"
)

// Segment is one transcript cue on the SOURCE timeline. It mirrors
// sidecar.Segment / becky-transcribe's segment shape so a parsed transcript
// converts with no reshaping.
type Segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// Dur is the cue's length in seconds, clamped to >= 0.
func (s Segment) Dur() float64 {
	if d := s.End - s.Start; d > 0 {
		return d
	}
	return 0
}

// Options are the windowing knobs. Use DefaultOptions and adjust.
type Options struct {
	// MinDuration / MaxDuration bound a moment. Defaults target the short-form
	// sweet spot: long enough to land a payoff, short enough to hold attention.
	MinDuration float64
	MaxDuration float64

	// ExtendBudget is how far past MaxDuration the ending-completion pass may
	// reach to land on a cue that finishes the thought. This is the deterministic
	// half of "extend every clip ending to the beat that completes it" — a clip
	// that stops mid-sentence is worse than one that runs slightly long.
	ExtendBudget float64

	// ThoughtGap is the pause (seconds) that marks a thought boundary. Leave 0 to
	// derive it from the transcript's own pause distribution via AutoThoughtGap —
	// which is what you almost always want, because a fixed constant does not
	// transfer between ASRs (see AutoThoughtGap).
	ThoughtGap float64

	// MaxCandidates bounds the emitted set so a 3-hour transcript cannot produce
	// a judge bill (or a report) proportional to its length. 0 = DefaultOptions'.
	MaxCandidates int
}

// DefaultOptions is the shipped windowing configuration.
func DefaultOptions() Options {
	return Options{
		MinDuration:   12,
		MaxDuration:   60,
		ExtendBudget:  8,
		MaxCandidates: 120,
	}
}

// Auto thought-gap bounds. The floor keeps a densely-packed transcript from
// treating every cue break as a new thought; the ceiling stops a transcript full
// of long silences from collapsing the whole video into one moment.
const (
	minThoughtGap = 0.35
	maxThoughtGap = 2.50
)

// gapEps absorbs float noise when a gap is compared against the threshold.
// Parakeet quantises word times to 0.08s, so many gaps are nominally identical
// yet differ in their last bits; strictly-greater on those made the SAME spoken
// pause a boundary in one place and not another. This is the same class of bug
// internal/subs hit and fixed — see its gapEps comment.
const gapEps = 1e-6

// AutoThoughtGap derives the thought-boundary pause from the transcript's own
// inter-cue gap distribution (the p75, clamped to [minThoughtGap, maxThoughtGap]).
//
// Why derived and not a constant: becky learned this the hard way on captions.
// 49% of Parakeet's words carry end == start, so ordinary speech reads as a
// 0.16-0.24s "gap" and a borrowed constant tuned on a different ASR turned every
// word into its own caption (STATE-OF-MASTER.md, 2026-07-19). The same trap
// applies here at cue scale: a constant tuned on YouTube auto-subs would shatter
// a Parakeet transcript into fragments. Deriving from the transcript's own
// distribution is ASR-agnostic by construction.
//
// Returns minThoughtGap for a transcript with too few gaps to have a
// distribution.
func AutoThoughtGap(segs []Segment) float64 {
	gaps := interCueGaps(segs)
	if len(gaps) < 4 {
		return minThoughtGap
	}
	sort.Float64s(gaps)
	g := percentile(gaps, 0.75)
	if g < minThoughtGap {
		return minThoughtGap
	}
	if g > maxThoughtGap {
		return maxThoughtGap
	}
	return g
}

// interCueGaps returns the silent gap after each cue except the last. Negative
// gaps (overlapping cues, common in rolling auto-subs) count as 0.
func interCueGaps(segs []Segment) []float64 {
	if len(segs) < 2 {
		return nil
	}
	gaps := make([]float64, 0, len(segs)-1)
	for i := 0; i+1 < len(segs); i++ {
		g := segs[i+1].Start - segs[i].End
		if g < 0 {
			g = 0
		}
		gaps = append(gaps, g)
	}
	return gaps
}

// percentile returns the p-quantile (0..1) of an ALREADY-SORTED slice using
// nearest-rank. sorted must be non-empty.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// Candidate is one self-contained window, with the structural evidence behind it.
// Score is the deterministic prior only — a judge's content score lands in
// Judged (see judge.go) and the two are combined by Rank.
type Candidate struct {
	// Source is the file this candidate came from. Find does not set it — the
	// caller does, when it merges candidates from several transcripts into one
	// slice. It is carried on the candidate (rather than kept in a parallel
	// slice) because ranking reorders and truncates, and recovering the owner
	// afterwards by matching timestamps silently mislabels any two files that
	// happen to share a window. That bug shipped once; this field is the fix.
	Source string `json:"source,omitempty"`

	Start    float64 `json:"start"`
	End      float64 `json:"end"`
	FirstCue int     `json:"first_cue"`
	LastCue  int     `json:"last_cue"`
	Text     string  `json:"text"`

	// Audio is 0..1: how much LANDED here in the soundtrack - punchline-shaped
	// loudness spikes and pitch rises. Set by the caller (internal/audiosig), not
	// by Find, because it needs the media and Find only reads a transcript. It is
	// an INDEPENDENT third signal: his humour is in the delivery, so a deadpan
	// line that reads flat on the page can land hard out loud.
	Audio      float64 `json:"audio,omitempty"`
	AudioBasis string  `json:"audio_basis,omitempty"`

	// Face is 0..1: the DENSITY of a talking head across this window, not the
	// span between outermost sightings (internal/facetrack.Track.CoverageIn).
	// Set by the caller (internal/facesig, a coarse whole-video pass), not by
	// Find, for the same reason Audio is caller-set — Find only reads a
	// transcript. It is the third independent signal: the top-ranked moment
	// can read perfectly on the page and sound great on the soundtrack and
	// still have the subject bent out of shot; only a frame shows that.
	//
	// FaceBasis is set whenever the signal was actually computed, even when
	// Face is exactly 0 — unlike Audio, a zero here ("nobody on screen") is
	// the exact case this signal exists to catch, so it cannot double as "no
	// signal available" the way near-silent audio safely can.
	Face      float64 `json:"face,omitempty"`
	FaceBasis string  `json:"face_basis,omitempty"`

	Signals Signals `json:"signals"`
	Score   float64 `json:"score"` // structural prior, 0..1

	// Extended records that the ending-completion pass moved End past the
	// window's natural last cue to land on a completing beat.
	Extended bool `json:"extended"`

	// Snapped records that the in/out points were moved onto real silence rather
	// than left on the transcript's cue boundaries. Set by the caller, which is
	// the only place the audio is available.
	Snapped bool `json:"snapped,omitempty"`
}

// Dur is the candidate's length in seconds.
func (c Candidate) Dur() float64 { return c.End - c.Start }

// Signals is the measured structural evidence for a candidate. Every field is
// derived from cue times or punctuation — nothing here is a model's opinion, so
// a low score can always be explained by pointing at a number.
type Signals struct {
	// Hook: does the window OPEN cleanly — after a real pause, and not mid-clause
	// on a conjunction. 0..1.
	Hook float64 `json:"hook"`
	// Payoff: does the window CLOSE on a completed thought (terminal punctuation
	// and/or a real pause after). 0..1.
	Payoff float64 `json:"payoff"`
	// SelfContained: does the opening avoid dangling back-references ("that's
	// why...", "so he said...") that make a clip incomprehensible on its own.
	// 0..1. This is the direct measure of the "trails off / starts mid-setup"
	// failure that makes most auto-cut shorts unwatchable.
	SelfContained float64 `json:"self_contained"`
	// Pace: words per second, scored against a comfortable speaking band. 0..1.
	Pace float64 `json:"pace"`
	// Fit: how well the duration sits in the short-form sweet spot. 0..1.
	Fit float64 `json:"fit"`

	// WordsPerSec is the raw measurement behind Pace, kept so a report can cite
	// the number rather than the score.
	WordsPerSec float64 `json:"words_per_sec"`
}

// Find returns candidate moments in transcript order. It never returns
// overlapping-identical windows, and it is deterministic: the same segments and
// options always produce the same candidates in the same order.
//
// Windows are anchored on THOUGHT BOUNDARIES (a real pause or terminal
// punctuation) at both ends, so a candidate never starts or stops mid-clause
// unless the transcript itself offers no better boundary.
func Find(segs []Segment, opt Options) []Candidate {
	segs = cleaned(segs)
	if len(segs) == 0 {
		return nil
	}
	opt = withDefaults(opt, segs)

	starts := thoughtStarts(segs, opt.ThoughtGap)
	ends := thoughtEnds(segs, opt.ThoughtGap)

	var out []Candidate
	seen := make(map[[2]int]bool)
	for _, s := range starts {
		for e := s; e < len(segs); e++ {
			dur := segs[e].End - segs[s].Start
			if dur < opt.MinDuration {
				continue
			}
			if dur > opt.MaxDuration {
				break
			}
			if !ends[e] {
				continue
			}
			key := [2]int{s, e}
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, build(segs, s, e, false, opt))
		}
	}

	// Windows that reached MaxDuration without ever landing on a completing beat
	// still deserve a shot: extend within the budget to the next completing cue
	// rather than dropping the moment or cutting it mid-sentence.
	out = append(out, extendedCandidates(segs, starts, ends, seen, opt)...)

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Start != out[j].Start {
			return out[i].Start < out[j].Start
		}
		return out[i].End < out[j].End
	})
	if opt.MaxCandidates > 0 && len(out) > opt.MaxCandidates {
		out = topN(out, opt.MaxCandidates)
	}
	return out
}

// extendedCandidates handles starts whose thought does not complete inside
// MaxDuration: it reaches up to ExtendBudget seconds further for the first
// completing cue. This is the deterministic "extend the ending to the beat that
// finishes the thought" pass.
func extendedCandidates(segs []Segment, starts []int, ends map[int]bool, seen map[[2]int]bool, opt Options) []Candidate {
	var out []Candidate
	for _, s := range starts {
		if completesWithin(segs, s, ends, opt) {
			continue
		}
		for e := 0; e < len(segs); e++ {
			if e < s {
				continue
			}
			dur := segs[e].End - segs[s].Start
			if dur <= opt.MaxDuration {
				continue
			}
			if dur > opt.MaxDuration+opt.ExtendBudget {
				break
			}
			if !ends[e] {
				continue
			}
			key := [2]int{s, e}
			if seen[key] {
				break
			}
			seen[key] = true
			out = append(out, build(segs, s, e, true, opt))
			break // the FIRST completing beat past the limit, not every one
		}
	}
	return out
}

// completesWithin reports whether the thought starting at s reaches a completing
// cue inside MaxDuration — i.e. whether Find's main loop already emitted for it.
func completesWithin(segs []Segment, s int, ends map[int]bool, opt Options) bool {
	for e := s; e < len(segs); e++ {
		dur := segs[e].End - segs[s].Start
		if dur > opt.MaxDuration {
			return false
		}
		if dur >= opt.MinDuration && ends[e] {
			return true
		}
	}
	return false
}

// build assembles a Candidate for the cue range [s,e] and scores it.
func build(segs []Segment, s, e int, extended bool, opt Options) Candidate {
	var sb strings.Builder
	for i := s; i <= e; i++ {
		if t := strings.TrimSpace(segs[i].Text); t != "" {
			if sb.Len() > 0 {
				sb.WriteByte(' ')
			}
			sb.WriteString(t)
		}
	}
	c := Candidate{
		Start:    segs[s].Start,
		End:      segs[e].End,
		FirstCue: s,
		LastCue:  e,
		Text:     sb.String(),
		Extended: extended,
	}
	c.Signals = measure(segs, s, e, opt)
	c.Score = c.Signals.prior()
	return c
}

// topN keeps the n highest-scoring candidates but returns them in TRANSCRIPT
// order, so a report reads chronologically while still being the best n.
func topN(cands []Candidate, n int) []Candidate {
	byScore := make([]Candidate, len(cands))
	copy(byScore, cands)
	sort.SliceStable(byScore, func(i, j int) bool { return byScore[i].Score > byScore[j].Score })
	kept := byScore[:n]
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].Start != kept[j].Start {
			return kept[i].Start < kept[j].Start
		}
		return kept[i].End < kept[j].End
	})
	return kept
}

// withDefaults fills unset options, deriving ThoughtGap from the transcript when
// the caller left it at 0.
func withDefaults(opt Options, segs []Segment) Options {
	d := DefaultOptions()
	if opt.MinDuration <= 0 {
		opt.MinDuration = d.MinDuration
	}
	if opt.MaxDuration <= 0 {
		opt.MaxDuration = d.MaxDuration
	}
	if opt.MaxDuration < opt.MinDuration {
		opt.MaxDuration = opt.MinDuration
	}
	if opt.ExtendBudget < 0 {
		opt.ExtendBudget = 0
	}
	if opt.ExtendBudget == 0 {
		opt.ExtendBudget = d.ExtendBudget
	}
	if opt.MaxCandidates == 0 {
		opt.MaxCandidates = d.MaxCandidates
	}
	if opt.ThoughtGap <= 0 {
		opt.ThoughtGap = AutoThoughtGap(segs)
	}
	return opt
}

// cleaned drops empty/degenerate cues and sorts by start time, so a transcript
// stitched from several sidecars is still monotonic.
func cleaned(segs []Segment) []Segment {
	out := make([]Segment, 0, len(segs))
	for _, s := range segs {
		if strings.TrimSpace(s.Text) == "" {
			continue
		}
		if s.End < s.Start {
			s.End = s.Start
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

// thoughtStarts returns cue indices where a new thought begins: the first cue,
// or any cue preceded by a real pause or a completed sentence.
func thoughtStarts(segs []Segment, gap float64) []int {
	var out []int
	for i := range segs {
		if i == 0 {
			out = append(out, i)
			continue
		}
		if segs[i].Start-segs[i-1].End >= gap-gapEps || endsSentence(segs[i-1].Text) {
			out = append(out, i)
		}
	}
	return out
}

// thoughtEnds returns the set of cue indices that CLOSE a thought: the last cue,
// or any cue that ends a sentence or is followed by a real pause.
func thoughtEnds(segs []Segment, gap float64) map[int]bool {
	out := make(map[int]bool, len(segs))
	for i := range segs {
		if i == len(segs)-1 {
			out[i] = true
			continue
		}
		if endsSentence(segs[i].Text) || segs[i+1].Start-segs[i].End >= gap-gapEps {
			out[i] = true
		}
	}
	return out
}
