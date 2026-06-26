// Package orchestrate is becky's PROTOCOL-ENFORCEMENT engine: the deterministic core that
// makes becky-tools self-regulate, so a single entry call (e.g. becky-transcribe) returns a
// FINAL, CORROBORATED result with becky's forensic protocols FORCED in code — never left as
// prose an agent can ignore (Jordan, 2026-06-26; SKILL.md "ARCHITECTURE").
//
// What it guarantees, in code (not as a suggestion):
//
//  1. Corroborate-then-conclude. A claim is "concluded" ONLY with >=2 independent agreeing
//     signals; a lone signal is a "candidate"; otherwise "unknown". Conclusions are COMPUTED
//     here, not asserted by a caller — so the protocol cannot be skipped.
//  2. Recall is for DETECTION, not NAMING. Naming is just rule 1 applied to an identity claim:
//     a name attaches only when corroborated.
//  3. Presence requires a WATCH. An "on screen" claim is never concluded unless a vision model
//     actually watched the segment; a transcript mention or a motion burst NEVER proves presence.
//  4. The confidence ladder. A claim that isn't concluded is validated (Gemma-4 E4B, level 1)
//     and escalated (12B, level 2) until it clears or the ladder is exhausted.
//
// The model/tool calls are injected via Executor so the engine stays deterministic and fully
// unit-testable; the real becky-*.exe / Gemma-4 wiring lives in the entry tools (local).
package orchestrate

import (
	"fmt"
	"sort"
	"strings"
)

// SignalKind classifies what a signal can actually prove (the SKILL.md evidence hierarchy).
type SignalKind string

const (
	KindMention SignalKind = "mention" // spoken about — a word at [t], NOT presence
	KindMotion  SignalKind = "motion"  // something moved — a candidate moment, NOT who/what
	KindText    SignalKind = "text"    // on-screen / transcript text content
	KindPrint   SignalKind = "print"   // a face/voice print matched — an identity signal
	KindWatched SignalKind = "watched" // a vision model WATCHED the segment — the only presence proof
)

// provesPresence reports whether a signal of this kind can, on its own, support a PRESENCE
// claim. Only a model that actually watched the segment qualifies.
func (k SignalKind) provesPresence() bool { return k == KindWatched }

// Signal is one piece of evidence about a claim, from one source.
type Signal struct {
	Source     string     // the tool/model that produced it, e.g. "becky-identify"
	Kind       SignalKind // what it can prove
	Confidence float64    // 0..1
}

// Claim is a single assertion under test, e.g. "speaker=Shelby" or "onscreen=cat@[10-14]".
type Claim struct {
	Key        string   // the assertion
	IsPresence bool     // true => subject to the watch rule (rule 3)
	Signals    []Signal // evidence gathered so far
}

// Rules are the deterministic thresholds. The defaults ARE becky's standing protocol.
type Rules struct {
	MinAgreeingSources    int     // >= this many distinct sources to conclude (protocol: 2)
	MinSignalConfidence   float64 // a signal below this does not count toward corroboration
	PresenceRequiresWatch bool    // an on-screen claim needs a watched signal
}

// DefaultRules encodes becky's corroborate-then-conclude protocol.
func DefaultRules() Rules {
	return Rules{MinAgreeingSources: 2, MinSignalConfidence: 0.5, PresenceRequiresWatch: true}
}

// Status is the protocol-enforced state of a claim.
type Status string

const (
	Concluded Status = "concluded" // >=2 independent signals agree (and a watch, if presence)
	Candidate Status = "candidate" // exactly one qualifying signal — not enough to state
	Unknown   Status = "unknown"   // nothing qualified
)

// Verdict is the protocol-enforced outcome for one claim. Only Concluded verdicts may be
// stated as fact; Candidate/Unknown are never dumped as "maybes" — they are held.
type Verdict struct {
	Claim   string
	Status  Status
	Sources []string // the distinct sources that counted toward the verdict
	Reason  string   // plain-language audit of WHY this status (forensic transparency)
}

// Corroborate is the LOAD-BEARING gate. It applies becky's protocols to a claim's signals and
// returns a verdict that CANNOT be Concluded unless the rules pass. This is what makes the
// protocol impossible to ignore: the conclusion is computed here, never asserted by the caller.
func Corroborate(c Claim, r Rules) Verdict {
	distinct := map[string]bool{}
	watched := false
	for _, s := range c.Signals {
		if s.Confidence < r.MinSignalConfidence {
			continue // below the floor: does not count
		}
		distinct[s.Source] = true
		if s.Kind.provesPresence() {
			watched = true
		}
	}
	sources := make([]string, 0, len(distinct))
	for s := range distinct {
		sources = append(sources, s)
	}
	sort.Strings(sources)

	// Rule 3: a presence claim with no watch is never concluded, regardless of count.
	if c.IsPresence && r.PresenceRequiresWatch && !watched {
		st := Unknown
		if len(sources) > 0 {
			st = Candidate
		}
		return Verdict{Claim: c.Key, Status: st, Sources: sources,
			Reason: "no vision model watched this segment; a mention or motion never proves presence"}
	}

	// Rules 1 & 2: corroborate-then-conclude (naming included).
	switch {
	case len(sources) >= r.MinAgreeingSources:
		return Verdict{Claim: c.Key, Status: Concluded, Sources: sources,
			Reason: fmt.Sprintf("%d independent signals agree (%s)", len(sources), strings.Join(sources, ", "))}
	case len(sources) == 1:
		return Verdict{Claim: c.Key, Status: Candidate, Sources: sources,
			Reason: "only one signal; needs a second independent source to conclude"}
	default:
		return Verdict{Claim: c.Key, Status: Unknown, Sources: sources,
			Reason: "no signal cleared the confidence floor"}
	}
}

// Executor performs the real work. It is injected so the engine is deterministic and testable;
// the real implementation (in the entry tools) shells becky-*.exe and calls Gemma-4 locally.
type Executor interface {
	// Validate re-checks a not-yet-concluded claim with a model and returns an additional
	// signal. level 1 = Gemma-4 E4B; level 2 = Gemma-4 12B. For a presence claim the model
	// WATCHES the segment, so the returned signal is KindWatched.
	Validate(claim Claim, level int) (Signal, error)
}

// ResolveClaim enforces the confidence ladder (rule 4): while the claim is not Concluded and
// the ladder is not exhausted, validate (level 1) then escalate (level 2), folding each model
// signal back in and re-applying Corroborate. It returns the final verdict plus a step-by-step
// audit. Because every loop re-runs Corroborate, the ladder can never produce a Concluded
// verdict that the protocol would reject.
func ResolveClaim(c Claim, r Rules, exec Executor, maxLevel int) (Verdict, []string) {
	audit := []string{}
	v := Corroborate(c, r)
	audit = append(audit, "initial: "+string(v.Status)+" ("+v.Reason+")")
	for level := 1; v.Status != Concluded && level <= maxLevel && exec != nil; level++ {
		sig, err := exec.Validate(c, level)
		if err != nil {
			audit = append(audit, fmt.Sprintf("validate L%d failed: %v", level, err))
			break
		}
		c.Signals = append(c.Signals, sig)
		v = Corroborate(c, r)
		audit = append(audit, fmt.Sprintf("after validate L%d (%s/%s conf %.2f): %s",
			level, sig.Source, sig.Kind, sig.Confidence, v.Status))
	}
	return v, audit
}

// Result is the final, protocol-enforced output of an orchestrated request: the things becky
// can STATE (concluded), the things it is still chasing (candidates), and the full audit.
type Result struct {
	Concluded  []Verdict
	Candidates []Verdict
	Unknown    []Verdict
	Audit      []string
}

// Resolve runs the full ladder over every claim and partitions the verdicts by status — the
// "final corroborated output" an entry tool returns. Candidates/Unknown are reported but kept
// out of the stated conclusions (no flood of maybes).
func Resolve(claims []Claim, r Rules, exec Executor, maxLevel int) Result {
	var res Result
	for _, c := range claims {
		v, audit := ResolveClaim(c, r, exec, maxLevel)
		res.Audit = append(res.Audit, audit...)
		switch v.Status {
		case Concluded:
			res.Concluded = append(res.Concluded, v)
		case Candidate:
			res.Candidates = append(res.Candidates, v)
		default:
			res.Unknown = append(res.Unknown, v)
		}
	}
	return res
}
