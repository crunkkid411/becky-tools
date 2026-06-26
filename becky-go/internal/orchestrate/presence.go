package orchestrate

import (
	"fmt"
	"sort"
)

// TimedSignal is a signal that carries a time span — the input to cross-tool PRESENCE
// correlation. A transcript mention, a motion burst, and a vision-model watch each become a
// TimedSignal; CorrelatePresence groups the ones that overlap in time into a presence window.
type TimedSignal struct {
	Source     string
	Kind       SignalKind
	Confidence float64
	Start      float64
	End        float64
}

// CorrelatePresence is the deterministic cross-tool chaining the agent kept doing wrong by hand:
// it groups a subject's time-overlapping (within mergeGap seconds) signals into presence WINDOWS
// and returns one IsPresence Claim per window. The claims then go through Resolve, where the
// PRESENCE RULE bites — a window is concluded ONLY if a model actually WATCHED it (a KindWatched
// signal) AND >=2 independent sources agree. A mention + a motion burst, with no watch, can never
// conclude presence; it stays a candidate to go look at. No model, no fuzz — pure time + the gate.
func CorrelatePresence(subject string, sigs []TimedSignal, mergeGap float64) []Claim {
	if len(sigs) == 0 {
		return nil
	}
	if mergeGap < 0 {
		mergeGap = 0
	}
	s := append([]TimedSignal(nil), sigs...)
	sort.Slice(s, func(i, j int) bool {
		if s[i].Start != s[j].Start {
			return s[i].Start < s[j].Start
		}
		return s[i].End < s[j].End
	})

	var claims []Claim
	winStart, winEnd := s[0].Start, s[0].End
	var cur []Signal

	flush := func() {
		if len(cur) == 0 {
			return
		}
		claims = append(claims, Claim{
			Key:        fmt.Sprintf("onscreen=%s@[%.1f-%.1f]", subject, winStart, winEnd),
			IsPresence: true,
			Signals:    cur,
		})
		cur = nil
	}

	for _, ts := range s {
		if ts.Start > winEnd+mergeGap { // a gap: start a new window
			flush()
			winStart, winEnd = ts.Start, ts.End
		}
		if ts.End > winEnd {
			winEnd = ts.End
		}
		cur = append(cur, Signal{Source: ts.Source, Kind: ts.Kind, Confidence: ts.Confidence})
	}
	flush()
	return claims
}
