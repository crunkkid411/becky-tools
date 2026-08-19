package moment

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// The CONTENT half of moment selection. Structure is measured in score.go; this
// file is where a model is allowed to have an opinion — and where becky's
// corroborate-then-conclude rule is enforced on that opinion.
//
// The rule, applied here:
//   - structural prior ALONE  -> a CANDIDATE. Never presented as a pick.
//   - prior + judge AGREEING  -> a CONCLUSION, safe to cut.
//   - prior + judge DISAGREE  -> held as a candidate, with the disagreement
//     stated. A tool that quietly averages two contradicting signals into a
//     confident-looking number is exactly the failure mode that makes every
//     other short-form tool produce plausible garbage.
//
// The rubric the judge is asked to apply is Berger & Milkman's virality
// dimensions (JMR 2012) plus an explicit hook/build/payoff completeness check.
// The prompt lives in Prompt() so it is versioned with the code and can be
// diffed when results change.

// Judgement is one model verdict on one candidate.
type Judgement struct {
	// Index ties the verdict back to the candidate slice position it judged.
	Index int `json:"index"`
	// Score is the model's 0..99 content rating.
	Score int `json:"score"`
	// Complete reports whether the model thinks the clip contains a full
	// hook -> build -> payoff arc rather than trailing off mid-setup.
	Complete bool `json:"complete"`
	// Reason is the model's one-line justification, surfaced in the report so a
	// human can audit the call instead of trusting a bare number.
	Reason string `json:"reason"`
}

// JudgeFunc scores a batch of candidates. Batching is the interface (not
// one-call-per-candidate) so a backend can put many windows in one request.
// Returning an error must leave the pipeline working — Rank degrades to
// structure-only and says so.
type JudgeFunc func(ctx context.Context, cands []Candidate) ([]Judgement, error)

// Confidence is how a ranked moment may be reported.
type Confidence string

const (
	// Conclusion: structure and content agree. Safe to cut.
	Conclusion Confidence = "conclusion"
	// CandidateOnly: one signal only (no judge available, or no verdict for this
	// window). Report as a candidate; do not present it as a pick.
	CandidateOnly Confidence = "candidate"
	// Disputed: the two signals disagree materially. Held, with the conflict named.
	Disputed Confidence = "disputed"
	// Vetoed: the content pass says the arc does not complete — the clip trails
	// off mid-setup. This is a REFUSAL, not a low score: a vetoed moment ranks
	// below every complete one no matter how well it scores, and a renderer
	// should skip it rather than cut it. HANDOFF-SHORTS-PIPELINE.md §2.1 names
	// "a short with no payoff" as a failure that renders fine and so is never
	// noticed; this is the one place it can be caught.
	Vetoed Confidence = "vetoed"
)

// Ranked is a candidate plus its verdict and the honest confidence in it.
type Ranked struct {
	Candidate

	// Judged is the model's verdict, nil when none was obtained.
	Judged *Judgement `json:"judged,omitempty"`
	// Final is the combined 0..1 rank used for ordering. With no judge it is the
	// structural prior alone — which is why Confidence must be read alongside it.
	Final float64 `json:"final"`
	// Confidence states how much the Final score can be trusted.
	Confidence Confidence `json:"confidence"`
	// Basis is the human-readable evidence line (FORENSIC-OUTPUT-PHILOSOPHY.md:
	// every claim carries the basis for it).
	Basis string `json:"basis"`
}

// disputeThreshold is how far apart the two normalised signals may sit before
// the moment is Disputed rather than concluded. 0.45 lets ordinary noise through
// while catching a genuine "structure says great, content says worthless".
const disputeThreshold = 0.45

// Rank combines the structural prior with the judge's content score and labels
// each result with an honest confidence. Judgements may be nil or partial; every
// candidate still comes back, ordered best-first.
func Rank(cands []Candidate, judgements []Judgement) []Ranked {
	byIndex := make(map[int]Judgement, len(judgements))
	for _, j := range judgements {
		if j.Index >= 0 && j.Index < len(cands) {
			byIndex[j.Index] = j
		}
	}

	out := make([]Ranked, 0, len(cands))
	for i, c := range cands {
		r := Ranked{Candidate: c}
		// Fold the audio signal into the structural prior before anything else
		// looks at it. Structure says the thought is well-formed; audio says
		// something actually landed. They are independent, and a clip needs both
		// to be worth posting - but audio measures ENERGY, not quality, so it
		// moves the ORDER and never on its own promotes a moment to a conclusion.
		if c.Audio > 0 {
			c.Score = clamp01(0.72*c.Score + 0.28*c.Audio)
			r.Candidate = c
		}
		j, ok := byIndex[i]
		if !ok {
			r.Final = c.Score
			r.Confidence = CandidateOnly
			if c.AudioBasis != "" {
				r.Basis = fmt.Sprintf("structure %.2f (%s) + audio: %s; no content verdict yet",
					c.Score, c.Signals.basis(), c.AudioBasis)
			} else {
				r.Basis = fmt.Sprintf(
					"structure only (%s); no content verdict — needs a second independent signal before this is a pick",
					c.Signals.basis())
			}
			out = append(out, r)
			continue
		}

		jn := float64(j.Score) / 99.0
		r.Judged = &j
		switch {
		case !j.Complete:
			// The model says the arc does not complete. That VETOES the moment:
			// it is ordered below every complete clip regardless of score (see
			// the sort below), not merely discounted. A discount is not a veto —
			// a high-scoring clip that trails off would still float to the top,
			// which is exactly the "no payoff" short this tool exists to avoid.
			r.Final = minF(jn, c.Score)
			r.Confidence = Vetoed
			r.Basis = fmt.Sprintf(
				"VETOED — the content pass says the arc does not complete (%s); "+
					"structure %.2f. Ranked below every complete moment; do not cut it",
				strings.TrimSpace(j.Reason), c.Score)
		case absDiff(jn, c.Score) > disputeThreshold:
			// Hold the lower of the two: a disputed moment must not be promoted
			// by its optimistic half.
			r.Final = minF(jn, c.Score)
			r.Confidence = Disputed
			r.Basis = fmt.Sprintf(
				"DISPUTED — structure %.2f (%s) vs content %d/99 (%s); held at the lower signal",
				c.Score, c.Signals.basis(), j.Score, strings.TrimSpace(j.Reason))
		default:
			r.Final = 0.5*c.Score + 0.5*jn
			r.Confidence = Conclusion
			r.Basis = fmt.Sprintf(
				"structure %.2f (%s) AND content %d/99 agree (%s)",
				c.Score, c.Signals.basis(), j.Score, strings.TrimSpace(j.Reason))
		}
		out = append(out, r)
	}

	// Complete arcs first, THEN score. This is what makes the veto a veto: no
	// score a trailing-off clip can reach lets it overtake one that lands.
	sort.SliceStable(out, func(i, j int) bool {
		vi, vj := out[i].Confidence == Vetoed, out[j].Confidence == Vetoed
		if vi != vj {
			return vj // a non-vetoed moment always sorts first
		}
		if out[i].Final != out[j].Final {
			return out[i].Final > out[j].Final
		}
		return out[i].Start < out[j].Start
	})
	return out
}

// basis renders the measured signals as a compact evidence string.
func (s Signals) basis() string {
	return fmt.Sprintf("hook %.2f, payoff %.2f, self-contained %.2f, %.1f w/s",
		s.Hook, s.Payoff, s.SelfContained, s.WordsPerSec)
}

// Prompt builds the judge request for a batch of candidates. It is exported so
// the exact text a model saw is inspectable and diffable, not buried in an HTTP
// call — when results shift, the prompt is the first thing to check.
//
// The rubric is Berger & Milkman (JMR 2012): high-arousal emotion (awe, anger,
// anxiety, amusement) travels; practical utility travels; low-arousal sadness
// does not. The completeness check is separate and is a veto, not a score.
func Prompt(cands []Candidate) string {
	var b strings.Builder
	b.WriteString(`You are selecting short-form video clips from a transcript.

For EACH numbered candidate below, return one JSON object per line:
{"index": <n>, "score": <0-99>, "complete": <true|false>, "reason": "<one short line>"}

score  — how likely this is to hold a scrolling viewer's attention, using the
         virality dimensions from Berger & Milkman (JMR 2012): high-arousal
         emotion (awe, anger, anxiety, amusement) and practical utility travel;
         low-arousal sadness does not. Judge the CONTENT only — do not reward or
         punish a clip for its length or pacing, which are measured separately.
complete — true ONLY if the clip contains a self-contained hook -> build ->
         payoff. Set false if it starts mid-setup, or ends before the point
         lands. This is a veto, so be strict: a fascinating clip that trails off
         mid-sentence is false.
reason — one short line naming the specific thing that decided it.

Output ONLY those JSON lines, one per candidate, no other text.

CANDIDATES:
`)
	for i, c := range cands {
		fmt.Fprintf(&b, "\n[%d] (%.1fs)\n%s\n", i, c.Dur(), strings.TrimSpace(c.Text))
	}
	return b.String()
}

// ParseJudgements reads the model's JSON-lines reply. Unparseable lines are
// skipped rather than failing the batch: a partial verdict set degrades to
// CandidateOnly for the windows it missed, which is honest, instead of throwing
// away every good verdict because one line was malformed.
func ParseJudgements(raw string) []Judgement {
	var out []Judgement
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			// Tolerate fenced or prefixed output by finding the object on the line.
			if i := strings.Index(line, "{"); i >= 0 {
				line = line[i:]
			} else {
				continue
			}
		}
		if j, ok := parseJudgementLine(line); ok {
			out = append(out, j)
		}
	}
	return out
}

// parseJudgementLine decodes ONE JSON object into a Judgement. It trims any
// trailing prose after the object (models sometimes append a comma or a note),
// and rejects a verdict whose score is out of range rather than clamping it —
// a model returning 500 has misunderstood the rubric, and silently rescaling
// that to 99 would fabricate agreement.
func parseJudgementLine(line string) (Judgement, bool) {
	end := strings.LastIndex(line, "}")
	if end < 0 {
		return Judgement{}, false
	}
	var j Judgement
	if err := json.Unmarshal([]byte(line[:end+1]), &j); err != nil {
		return Judgement{}, false
	}
	if j.Index < 0 || j.Score < 0 || j.Score > 99 {
		return Judgement{}, false
	}
	return j, true
}

func absDiff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
