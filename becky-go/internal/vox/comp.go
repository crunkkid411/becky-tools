package vox

// comp.go is the comping model (SPEC §6): for each phrase, score every take by a
// weighted blend of pitch stability, timing tightness, and level/SNR, then choose
// the winner and record the runner-up. The comp decision list is a declared,
// repeatable artifact — same takes + same weights => same comp — not a mouse-history.
// becky shows the score and the runner-up so a producer can override one phrase
// without re-auditioning every take (the slow part of manual comping).

// MetricWeights weight the comp score (SPEC §6 "balanced" default).
type MetricWeights struct {
	PitchStability float64
	TimingTight    float64
	Level          float64
}

// WeightsForMetric maps a named metric to its weights. Unknown => balanced.
func WeightsForMetric(name string) MetricWeights {
	switch name {
	case "pitch":
		return MetricWeights{PitchStability: 0.7, TimingTight: 0.2, Level: 0.1}
	case "timing":
		return MetricWeights{PitchStability: 0.2, TimingTight: 0.7, Level: 0.1}
	default: // "balanced"
		return MetricWeights{PitchStability: 0.4, TimingTight: 0.4, Level: 0.2}
	}
}

// TakeAnalysis is one take's per-phrase analysis (the Phrases from Align) plus an
// optional level/SNR signal per phrase, used to score the comp.
type TakeAnalysis struct {
	Name    string
	Phrases []Phrase
	Levels  []float64 // 0..1 level/SNR per phrase (optional; defaults to neutral 0.5)
}

// Comp builds the per-phrase comp decision list across takes. Phrase boundaries are
// taken from the FIRST take (the guide-aligned reference); each take is scored for
// every phrase it has. Deterministic: ties break toward the lowest take index.
func Comp(takes []TakeAnalysis, metric string) CompResult {
	res := CompResult{
		Tool: "becky-vox", SchemaVersion: SchemaVersion, Metric: metric,
		Deterministic: true,
	}
	for _, t := range takes {
		res.Takes = append(res.Takes, t.Name)
	}
	if len(takes) == 0 || len(takes[0].Phrases) == 0 {
		res.Degraded = true
		res.Reason = "no takes/phrases to comp"
		return res
	}
	w := WeightsForMetric(metric)
	nPhrases := len(takes[0].Phrases)
	for p := 0; p < nPhrases; p++ {
		res.Choices = append(res.Choices, scorePhrase(takes, p, w))
	}
	return res
}

// scorePhrase scores every take for phrase p and returns the winner + runner-up.
// Ties break toward the lowest take index (a strict > keeps the earlier winner).
func scorePhrase(takes []TakeAnalysis, p int, w MetricWeights) CompChoice {
	best, bestScore := -1, -1.0
	runner, runnerScore := -1, -1.0
	var startMs, endMs float64
	for ti, t := range takes {
		if p >= len(t.Phrases) {
			continue
		}
		if best < 0 && runner < 0 {
			startMs, endMs = t.Phrases[p].StartMs, t.Phrases[p].EndMs
		}
		s := takeScore(t, p, w)
		switch {
		case s > bestScore:
			runner, runnerScore = best, bestScore
			best, bestScore = ti, s
		case s > runnerScore:
			runner, runnerScore = ti, s
		}
	}
	return CompChoice{
		Phrase: p, StartMs: round2(startMs), EndMs: round2(endMs),
		ChosenTake: best, Score: round3(bestScore),
		RunnerUp: runner, RunnerScore: round3(nonNeg(runnerScore)),
	}
}

// takeScore is the weighted blend for one take's phrase p.
func takeScore(t TakeAnalysis, p int, w MetricWeights) float64 {
	ph := t.Phrases[p]
	level := 0.5
	if p < len(t.Levels) {
		level = clamp01(t.Levels[p])
	}
	return w.PitchStability*ph.PitchStability + w.TimingTight*ph.TimingTightness + w.Level*level
}

func nonNeg(s float64) float64 {
	if s < 0 {
		return 0
	}
	return s
}
