// Deterministic fakes for the network/model boundary, so the WHOLE pipeline runs
// in CI with no model and no network — proving the deterministic Go layer end to
// end (SPEC §8). These are also what cmd/research falls back to when no real
// backend is configured, giving an honest sources-only / deterministic-plan run
// instead of a crash. The real backends (research_helper.py, SearXNG, web2md) are
// wired by the local agent against the contracts in helper.go.
package research

import (
	"sort"
	"strings"
	"time"
)

// FakeSearch returns canned results from a fixed table keyed by query. Unknown
// queries return nothing (not an error) — a realistic "no hits" path.
type FakeSearch struct {
	Table map[string][]SearchResult
	Err   error // if set, every Search returns this error (tests the degrade path)
}

// Search implements Searcher deterministically.
func (f *FakeSearch) Search(query string, k int) ([]SearchResult, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	res := f.Table[query]
	if k > 0 && len(res) > k {
		res = res[:k]
	}
	return res, nil
}

// FakeFetch returns canned captures from a fixed table keyed by canonical URL.
type FakeFetch struct {
	Pages map[string]Capture // key: canonical URL
	Err   error
	Now   func() time.Time // injectable clock for a fixed fetched_at
}

// Fetch implements Fetcher deterministically (fixed fetched_at via the clock).
func (f *FakeFetch) Fetch(url string) (Capture, error) {
	if f.Err != nil {
		return Capture{}, f.Err
	}
	c, ok := f.Pages[CanonicalURL(url)]
	if !ok {
		return Capture{}, errNotFound
	}
	if c.FetchedAt == "" {
		now := time.Now
		if f.Now != nil {
			now = f.Now
		}
		c.FetchedAt = now().UTC().Format(time.RFC3339)
	}
	if c.HTTPStatus == 0 {
		c.HTTPStatus = 200
	}
	return c, nil
}

type fakeError string

func (e fakeError) Error() string { return string(e) }

const errNotFound = fakeError("fake fetch: url not in canned pages")

// FakeHelper is a deterministic stand-in for the model. It plans via the Go floor,
// extracts a single whole-text snippet per source, drafts one claim per source
// citing it, and verifies by a forced/default verdict — enough to exercise the
// supports/partial/unsupported → corroborated/candidate path without a model.
// Synthesize echoes the findings as one section.
type FakeHelper struct {
	// VerdictByClaim optionally forces a verdict for a claim text (tests the
	// supports/partial/unsupported branches precisely). Default: "supports".
	VerdictByClaim map[string]string
	// ClaimText optionally overrides the drafted claim for a source id.
	ClaimText map[int]string
}

// NewFakeHelper returns a ready FakeHelper.
func NewFakeHelper() *FakeHelper { return &FakeHelper{} }

// Plan delegates to the deterministic Go planner.
func (h *FakeHelper) Plan(question string, maxSub, maxPer int) ([]SubQuestion, error) {
	return PlanQuery(question, maxSub, maxPer), nil
}

// Extract returns one snippet spanning the whole source text.
func (h *FakeHelper) Extract(question string, sourceID int, text string) ([]Snippet, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	return []Snippet{{SourceID: sourceID, Start: 0, End: len(text), Text: text}}, nil
}

// Draft makes one claim per source id present in the snippets, citing that source.
func (h *FakeHelper) Draft(question string, snippets []Snippet) ([]DraftClaim, error) {
	ids := make([]int, 0)
	seen := map[int]bool{}
	for _, s := range snippets {
		if !seen[s.SourceID] {
			seen[s.SourceID] = true
			ids = append(ids, s.SourceID)
		}
	}
	sort.Ints(ids)
	out := make([]DraftClaim, 0, len(ids))
	for _, id := range ids {
		claim := h.ClaimText[id]
		if claim == "" {
			claim = "claim from source " + itoa(id)
		}
		out = append(out, DraftClaim{Claim: claim, Cites: []int{id}})
	}
	return out, nil
}

// Verify returns the forced verdict for a claim, defaulting to "supports".
func (h *FakeHelper) Verify(claim, snapshotText string) (Verdict, error) {
	v := h.VerdictByClaim[claim]
	if v == "" {
		v = "supports"
	}
	return Verdict{Verdict: v, Why: "fake helper"}, nil
}

// Synthesize echoes the verified findings as a single section.
func (h *FakeHelper) Synthesize(question string, claims []Finding) ([]ReportSection, error) {
	var b strings.Builder
	for _, f := range claims {
		b.WriteString("- " + f.Claim + "\n")
	}
	return []ReportSection{{Heading: "Summary", Body: b.String()}}, nil
}

// itoa is a tiny int→string without importing strconv just for this.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
