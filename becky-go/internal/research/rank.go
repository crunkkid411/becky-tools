// Planning, fan-out scheduling, RRF ranking and near-dup collapse — the
// deterministic heart of becky-research. Everything here is pure Go over plain
// values: no network, no model, no map-order dependence. The same inputs always
// produce the same ordered output (the becky determinism invariant), which is
// what lets a captured snapshot be re-ranked identically on any machine.
package research

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strings"
)

// defaultRRFK is the standard Reciprocal Rank Fusion smoothing constant. It
// matches the value cmd/search uses so the two tools fuse rankings the same way.
const defaultRRFK = 60

// SearchResult is one ranked hit from a search backend. Rank is 1-based, rank=1
// best — exactly the contract the helper "search" op returns.
type SearchResult struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	Rank    int    `json:"rank"`
	Snippet string `json:"snippet"`
}

// SubQuestion is one planned sub-question plus the search queries that feed it.
type SubQuestion struct {
	Q       string   `json:"q"`
	Queries []string `json:"queries"`
}

// PlanQuery deterministically turns a top-level question into an ordered list of
// sub-questions WITHOUT a model. It is the degrade-safe floor used when no helper
// (model) is available: the real planner (helper "plan" op) replaces this with a
// model-generated decomposition, but the Go floor guarantees a stable, useful
// fan-out even with no model present (degrade, never crash).
//
// The decomposition is a fixed set of forensic framings ("overview", "current
// state", "evidence", "alternatives", "limitations") applied to the question,
// truncated to maxSub. Each sub-question gets up to maxPer queries derived
// deterministically. Identical input → identical plan.
func PlanQuery(question string, maxSub, maxPer int) []SubQuestion {
	q := strings.TrimSpace(question)
	if q == "" || maxSub <= 0 {
		return nil
	}
	framings := []string{
		"",
		"current state of ",
		"evidence and benchmarks for ",
		"alternatives to ",
		"limitations and risks of ",
	}
	if maxSub > len(framings) {
		maxSub = len(framings)
	}
	out := make([]SubQuestion, 0, maxSub)
	for i := 0; i < maxSub; i++ {
		subQ := strings.TrimSpace(framings[i] + q)
		out = append(out, SubQuestion{Q: subQ, Queries: deriveQueries(subQ, maxPer)})
	}
	return out
}

// deriveQueries produces up to maxPer deterministic search queries for a
// sub-question. The first is the sub-question verbatim; the rest append fixed
// qualifiers. No randomness, so a re-run reproduces the same query set.
func deriveQueries(subQ string, maxPer int) []string {
	if maxPer <= 0 {
		return nil
	}
	qualifiers := []string{"", " 2026", " comparison review"}
	out := make([]string, 0, maxPer)
	for i := 0; i < maxPer && i < len(qualifiers); i++ {
		out = append(out, strings.TrimSpace(subQ+qualifiers[i]))
	}
	return out
}

// CanonicalURL normalizes a URL so the same page fetched via slightly different
// links collapses to one citation number (LangChain's one-number-per-URL rule).
// It lowercases scheme/host, drops a default port, a trailing slash, a fragment,
// and common tracking query params, and sorts the remaining query. Unparseable
// input is returned trimmed/lowercased so it still dedups against itself.
func CanonicalURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return strings.ToLower(s)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Host = strings.TrimSuffix(u.Host, ":80")
	u.Host = strings.TrimSuffix(u.Host, ":443")
	u.Fragment = ""
	if u.Path != "/" {
		u.Path = strings.TrimSuffix(u.Path, "/")
	}
	u.RawQuery = cleanQuery(u.Query())
	return u.String()
}

// cleanQuery drops tracking params and returns the remaining query sorted, so
// param order never changes the canonical form.
func cleanQuery(v url.Values) string {
	tracking := map[string]bool{
		"utm_source": true, "utm_medium": true, "utm_campaign": true,
		"utm_term": true, "utm_content": true, "fbclid": true, "gclid": true,
	}
	keys := make([]string, 0, len(v))
	for k := range v {
		if !tracking[strings.ToLower(k)] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := url.Values{}
	for _, k := range keys {
		out[k] = v[k]
	}
	return out.Encode()
}

// fusedDoc is the accumulator for one canonical URL across all result lists.
type fusedDoc struct {
	canon   string
	result  SearchResult // representative (best original rank seen)
	score   float64
	bestRk  int
	listHit int // how many lists this URL appeared in (tie-break signal)
}

// FuseRRF fuses multiple ranked result lists into one deduped ranking using
// Reciprocal Rank Fusion:
//
//	score(d) = Σ over lists d appears in of 1 / (rrfK + rank_d)
//
// Dedup is by CanonicalURL: the same page in several lists contributes to one
// fused score and appears once, keeping the representative with the best (lowest)
// original rank. The returned slice is sorted deterministically — fused score
// desc, then more-list-hits, then best original rank asc, then canonical URL asc —
// so identical inputs always yield byte-identical output and stable citation
// numbering downstream. rrfK <= 0 falls back to defaultRRFK.
func FuseRRF(lists [][]SearchResult, rrfK int) []SearchResult {
	if rrfK <= 0 {
		rrfK = defaultRRFK
	}
	byCanon := make(map[string]*fusedDoc)
	order := make([]string, 0)
	for _, list := range lists {
		for _, r := range list {
			canon := CanonicalURL(r.URL)
			if canon == "" {
				continue
			}
			rank := r.Rank
			if rank <= 0 {
				rank = 1
			}
			d, ok := byCanon[canon]
			if !ok {
				d = &fusedDoc{canon: canon, result: r, bestRk: rank}
				byCanon[canon] = d
				order = append(order, canon)
			}
			d.score += 1.0 / float64(rrfK+rank)
			d.listHit++
			if rank < d.bestRk {
				d.bestRk = rank
				d.result = r // representative = best-ranked occurrence
			}
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := byCanon[order[i]], byCanon[order[j]]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.listHit != b.listHit {
			return a.listHit > b.listHit
		}
		if a.bestRk != b.bestRk {
			return a.bestRk < b.bestRk
		}
		return a.canon < b.canon
	})
	out := make([]SearchResult, 0, len(order))
	for newRank, canon := range order {
		d := byCanon[canon]
		r := d.result
		r.URL = canon
		r.Rank = newRank + 1 // re-number into the fused order
		out = append(out, r)
	}
	return out
}

// shingleSet builds the set of word-level k-shingles (k=3) of a text, lowercased.
// It is the cheap near-dup signal: two captures whose shingle sets overlap heavily
// are the same article republished, even when their bytes (and so content hash)
// differ. Returned keys are sha256-hashed shingles to bound memory.
func shingleSet(text string) map[string]struct{} {
	words := strings.Fields(strings.ToLower(text))
	set := make(map[string]struct{})
	const k = 3
	if len(words) < k {
		if len(words) > 0 {
			set[hashWord(strings.Join(words, " "))] = struct{}{}
		}
		return set
	}
	for i := 0; i+k <= len(words); i++ {
		set[hashWord(strings.Join(words[i:i+k], " "))] = struct{}{}
	}
	return set
}

func hashWord(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// jaccard returns the Jaccard similarity of two shingle sets in [0,1]. Two empty
// sets are treated as identical (1.0) so empty captures collapse together.
func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1.0
	}
	inter := 0
	for s := range a {
		if _, ok := b[s]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// CollapseNearDups removes near-duplicate captures from an already-ranked list,
// keeping the higher-ranked representative. Exact duplicates (same ContentSHA256)
// collapse first; then a cheap word-shingle Jaccard >= threshold collapses
// republished/mirror copies. Input order is preserved for survivors, so the upstream
// RRF ranking is respected. Deterministic: a fixed scan order, no maps over output.
func CollapseNearDups(caps []Capture, threshold float64) []Capture {
	if len(caps) == 0 {
		return caps
	}
	kept := make([]Capture, 0, len(caps))
	keptShingles := make([]map[string]struct{}, 0, len(caps))
	seenHash := make(map[string]bool)
	for _, c := range caps {
		if c.ContentSHA256 != "" && seenHash[c.ContentSHA256] {
			continue // exact dup of an already-kept capture
		}
		sh := shingleSet(c.Text)
		isDup := false
		for _, ks := range keptShingles {
			if jaccard(sh, ks) >= threshold {
				isDup = true
				break
			}
		}
		if isDup {
			continue
		}
		if c.ContentSHA256 != "" {
			seenHash[c.ContentSHA256] = true
		}
		kept = append(kept, c)
		keptShingles = append(keptShingles, sh)
	}
	return kept
}
