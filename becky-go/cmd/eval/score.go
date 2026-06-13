// score.go — the deterministic recall scorer and config ranking.
//
// Scoring is RECALL-FIRST per the spec: a fact is recalled when ANY of its
// aliases appears (case-insensitively, as a substring) in the tool's flattened
// output text. Weighted recall = sum(weight of recalled facts) / sum(all weights).
// False positives are NOT penalized (a human + cross-corroboration filter them);
// we only record output size as a coarse over-reporting proxy. Configs are ranked
// by mean recall over the non-holdout cases; the best config is then re-measured
// on the held-out cases to check generalization.
package main

import (
	"sort"
	"strings"
)

// scoreFacts returns per-fact hits and the weighted recall for one output text.
func scoreFacts(outputText string, facts []Fact) ([]FactHit, float64) {
	hay := strings.ToLower(outputText)
	hits := make([]FactHit, 0, len(facts))
	var got, total float64
	for _, f := range facts {
		w := f.effectiveWeight()
		total += w
		matched := firstAliasMatch(hay, f.Aliases)
		if matched != "" {
			got += w
		}
		hits = append(hits, FactHit{
			ID:       f.ID,
			Category: f.Category,
			Hit:      matched != "",
			Matched:  matched,
			Weight:   w,
		})
	}
	if total == 0 {
		return hits, 0
	}
	return hits, round4(got / total)
}

// firstAliasMatch returns the first alias (original casing) that appears in the
// already-lowercased haystack, or "" if none match.
func firstAliasMatch(lowerHay string, aliases []string) string {
	for _, a := range aliases {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if strings.Contains(lowerHay, strings.ToLower(a)) {
			return a
		}
	}
	return ""
}

// hitCount counts the recalled facts (unweighted, for reporting).
func hitCount(hits []FactHit) int {
	n := 0
	for _, h := range hits {
		if h.Hit {
			n++
		}
	}
	return n
}

// rankConfigs aggregates the per-(case x config) results into mean-recall scores
// per config over the chosen split (holdout == wantHoldout), sorted descending by
// mean recall (ties broken by config name for determinism).
func rankConfigs(results []CaseResult, wantHoldout bool) []ConfigScore {
	type acc struct {
		sum    float64
		cases  int
		ok     int
		failed int
	}
	byConfig := map[string]*acc{}
	var order []string
	for _, r := range results {
		if r.Holdout != wantHoldout {
			continue
		}
		a := byConfig[r.Config]
		if a == nil {
			a = &acc{}
			byConfig[r.Config] = a
			order = append(order, r.Config)
		}
		a.cases++
		if r.Status == "ok" {
			a.ok++
			a.sum += r.Recall
		} else {
			a.failed++
			// A failed run scores 0 recall (it surfaced nothing) — this keeps the
			// metric honest: a config that crashes is worse than one that runs.
		}
	}
	scores := make([]ConfigScore, 0, len(order))
	for _, name := range order {
		a := byConfig[name]
		mean := 0.0
		if a.cases > 0 {
			mean = a.sum / float64(a.cases)
		}
		scores = append(scores, ConfigScore{
			Config:     name,
			MeanRecall: round4(mean),
			Cases:      a.cases,
			OK:         a.ok,
			Failed:     a.failed,
		})
	}
	sort.SliceStable(scores, func(i, j int) bool {
		if scores[i].MeanRecall != scores[j].MeanRecall {
			return scores[i].MeanRecall > scores[j].MeanRecall
		}
		return scores[i].Config < scores[j].Config
	})
	return scores
}

func round4(f float64) float64 { return float64(int(f*10000+0.5)) / 10000 }
