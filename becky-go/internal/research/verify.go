// The fetch→extract→draft→verify→status path (SPEC R3, R5–R8). The load-bearing,
// anti-hallucination half of becky-research: a working link is NOT support, so we
// separate "the link resolved" (R3) from "the captured text actually backs this
// claim" (R7 entailment). Unsupported claims are dropped and listed; a lone verified
// source is a candidate; ≥2 independent verified sources is corroborated. All status
// math is pure Go (deterministic, table-tested); the model only judges entailment.
package research

import (
	"sort"
	"strings"
)

// fetchAll runs R3: for each ranked result, reuse the cached capture if present
// (write-once → never re-fetch), else fetch live via the backend and cache it. A
// fetch failure marks that URL link_ok=false and drops it (degrade per-source, not
// the whole run). Offline/no-fetch uses only what is already cached.
func fetchAll(cfg Config, be Backends, cache *Cache, results []SearchResult, degrades *[]string) []Capture {
	out := make([]Capture, 0, len(results))
	fetchedLive := false
	for _, r := range results {
		if len(out) >= cfg.MaxSources {
			break
		}
		if c, ok := cache.Get(r.URL); ok {
			out = append(out, c)
			continue
		}
		if cfg.Offline || be.Fetch == nil {
			continue // nothing cached and forbidden to fetch — skip this URL
		}
		c, err := be.Fetch.Fetch(r.URL)
		if err != nil {
			*degrades = append(*degrades, "a source could not be fetched")
			continue
		}
		c.URL = CanonicalURL(r.URL)
		if c.Title == "" {
			c.Title = r.Title
		}
		c.LinkOK = c.HTTPStatus >= 200 && c.HTTPStatus < 400
		stored, err := cache.Put(c)
		if err != nil {
			*degrades = append(*degrades, "a source could not be cached")
			continue
		}
		fetchedLive = true
		out = append(out, stored)
	}
	if !fetchedLive && (cfg.Offline || be.Fetch == nil) && len(out) == 0 && len(results) > 0 {
		*degrades = append(*degrades, "no-network; nothing cached")
	}
	return out
}

// buildFindings runs R5–R8: extract snippets per source, draft claims tied to those
// snippets, verify each claim against its cited snapshot, and assign a forensic
// status. With no helper it returns a sources-only degrade (the captures stand as
// raw candidate material). Returns (findings, dropped).
func buildFindings(cfg Config, be Backends, captures []Capture, sources []Source, degrades *[]string) ([]Finding, []DroppedClaim) {
	if be.Helper == nil {
		*degrades = append(*degrades, "no-model; sources only")
		return nil, nil
	}
	idByURL := map[string]int{}
	textByID := map[int]string{}
	for i, c := range captures {
		idByURL[c.URL] = sources[i].ID
		textByID[sources[i].ID] = c.Text
	}

	snippets := extractAll(be, cfg, captures, idByURL, degrades)
	if len(snippets) == 0 {
		return nil, nil
	}
	drafts, err := be.Helper.Draft(questionOf(cfg), snippets)
	if err != nil {
		*degrades = append(*degrades, "draft helper failed")
		return nil, nil
	}
	return verifyDrafts(be, drafts, textByID, degrades)
}

// extractAll runs R5 across captures in source-id order (deterministic), keeping
// snippets and re-stamping each with the orchestrator's trusted source id.
func extractAll(be Backends, cfg Config, captures []Capture, idByURL map[string]int, degrades *[]string) []Snippet {
	all := make([]Snippet, 0)
	for _, c := range captures {
		id := idByURL[c.URL]
		snips, err := be.Helper.Extract(questionOf(cfg), id, c.Text)
		if err != nil {
			*degrades = append(*degrades, "extract helper failed for a source")
			continue
		}
		for _, s := range snips {
			s.SourceID = id // trust the orchestrator's id, not the helper's
			all = append(all, s)
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].SourceID != all[j].SourceID {
			return all[i].SourceID < all[j].SourceID
		}
		return all[i].Start < all[j].Start
	})
	return all
}

// verifyDrafts runs R7+R8 status assignment over drafted claims. Each claim's cites
// are validated against captured snapshots; the verdict + independent-source count
// decide status. Output is sorted for determinism.
func verifyDrafts(be Backends, drafts []DraftClaim, textByID map[int]string, degrades *[]string) ([]Finding, []DroppedClaim) {
	findings := make([]Finding, 0, len(drafts))
	dropped := make([]DroppedClaim, 0)
	for _, d := range drafts {
		cites := validCites(d.Cites, textByID)
		if len(cites) == 0 {
			dropped = append(dropped, DroppedClaim{Claim: d.Claim, Verify: "unsupported",
				Reason: "no cite resolves to a captured snapshot"})
			continue
		}
		verdict := worstVerdict(be, d.Claim, cites, textByID, degrades)
		if verdict == "unsupported" {
			dropped = append(dropped, DroppedClaim{Claim: d.Claim, Verify: verdict,
				Reason: "cited source does not state this"})
			continue
		}
		findings = append(findings, makeFinding(d.Claim, verdict, cites))
	}
	sortFindings(findings)
	sortDropped(dropped)
	return findings, dropped
}

// validCites keeps only cites that resolve to a captured snapshot, sorted+deduped.
func validCites(cites []int, textByID map[int]string) []int {
	seen := map[int]bool{}
	out := make([]int, 0, len(cites))
	for _, id := range cites {
		if _, ok := textByID[id]; ok && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Ints(out)
	return out
}

// worstVerdict re-checks the claim against EACH cited snapshot and returns the
// weakest verdict seen (unsupported < partial < supports): a claim is only as
// strong as its weakest cited support. A helper error on a cite is treated as
// partial (degrade, not crash) and noted.
func worstVerdict(be Backends, claim string, cites []int, textByID map[int]string, degrades *[]string) string {
	worst := "supports"
	for _, id := range cites {
		v, err := be.Helper.Verify(claim, textByID[id])
		verdict := "partial"
		if err != nil {
			*degrades = append(*degrades, "verify helper failed for a cite")
		} else {
			verdict = normVerdict(v.Verdict)
		}
		if verdictRank(verdict) < verdictRank(worst) {
			worst = verdict
		}
	}
	return worst
}

func normVerdict(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "supports":
		return "supports"
	case "partial":
		return "partial"
	default:
		return "unsupported"
	}
}

func verdictRank(v string) int {
	switch v {
	case "unsupported":
		return 0
	case "partial":
		return 1
	default: // supports
		return 2
	}
}

// makeFinding applies the forensic status rule (FORENSIC-OUTPUT-PHILOSOPHY):
//   - supports + ≥2 independent cites → corroborated (state it plainly).
//   - supports + 1 cite, or partial   → candidate (verify before trusting).
//
// Confidence is a deterministic function of verdict + corroboration, NOT a model
// score, so it is reproducible.
func makeFinding(claim, verdict string, cites []int) Finding {
	f := Finding{Claim: claim, Verify: verdict, Cites: cites}
	switch {
	case verdict == "supports" && len(cites) >= 2:
		f.Status = StatusCorroborated
		f.Confidence = 0.9
		f.Basis = "two or more independent sources agree; each re-checked against captured text"
	case verdict == "supports":
		f.Status = StatusCandidate
		f.Confidence = 0.6
		f.Basis = "single source; supported by captured text but not corroborated — verify before trusting"
	default: // partial
		f.Status = StatusCandidate
		f.Confidence = 0.45
		f.Basis = "partial support in the cited source; not corroborated — verify before trusting"
	}
	return f
}

// sortFindings orders findings deterministically: corroborated first, then by
// confidence desc, then claim asc.
func sortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		ci, cj := statusRank(fs[i].Status), statusRank(fs[j].Status)
		if ci != cj {
			return ci < cj
		}
		if fs[i].Confidence != fs[j].Confidence {
			return fs[i].Confidence > fs[j].Confidence
		}
		return fs[i].Claim < fs[j].Claim
	})
}

func sortDropped(ds []DroppedClaim) {
	sort.SliceStable(ds, func(i, j int) bool { return ds[i].Claim < ds[j].Claim })
}

func statusRank(s string) int {
	switch s {
	case StatusCorroborated:
		return 0
	case StatusCandidate:
		return 1
	default:
		return 2
	}
}
