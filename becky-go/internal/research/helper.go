// The network/model boundary — defined as small interfaces the orchestrator
// drives, with a faked implementation for unit tests. The REAL implementations
// (research_helper.py over llama-server + a SearXNG/Tavily search backend, and a
// web2md fetcher) are wired by the local agent; everything in this package runs
// against the fakes so CI never touches the network or a model.
//
// This is the single, explicit, logged place becky reaches the web — exactly how
// becky-freshness isolates ITS one network call. becky's offline forensic tools
// never call any of this at runtime.
package research

// Searcher issues one query against a pluggable search backend and returns a
// ranked result list (rank=1 best). The real backend is SearXNG/Tavily/Brave;
// the fake returns canned rows.
//
// Local-agent contract (helper op "search"):
//
//	in : {"query": string, "k": int}
//	out: {"results": [{"url": str, "title": str, "rank": int, "snippet": str}, ...]}
type Searcher interface {
	Search(query string, k int) ([]SearchResult, error)
}

// Fetcher fetches one URL once and returns the cleaned capture (raw snapshot). The
// real backend reuses web2md; ContentSHA256 may be left empty (the cache fills it).
//
// Local-agent contract (helper op "fetch", or reuse web2md):
//
//	in : {"url": string}
//	out: {"url": str, "http_status": int, "title": str, "text": str, "content_sha256": str}
type Fetcher interface {
	Fetch(url string) (Capture, error)
}

// Snippet is one piece of evidence extracted from a source: its source id plus the
// character span [start,end] into that source's cached text. No snippet = no
// citable evidence (SPEC R5).
type Snippet struct {
	SourceID int    `json:"source_id"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
	Text     string `json:"text"`
}

// DraftClaim is a composed claim that MUST reference source ids drawn only from the
// R5 snippets it was given. A claim with no valid cite is rejected by R7.
type DraftClaim struct {
	Claim string `json:"claim"`
	Cites []int  `json:"cites"`
}

// Verdict is the R7 entailment judgment of a claim against its cited snapshot text.
type Verdict struct {
	Verdict string `json:"verdict"` // supports | partial | unsupported
	Why     string `json:"why"`
}

// ReportSection is one rendered section of the synthesized report.
type ReportSection struct {
	Heading string `json:"heading"`
	Body    string `json:"body"` // may contain inline [n] cites
}

// Helper is the model side of the pipeline: plan, extract, draft, verify,
// synthesize. The orchestrator owns all bookkeeping; the Helper only reasons.
// Every method maps 1:1 to a research_helper.py op (SPEC §5). The fake (NewFakeHelper)
// implements all of these deterministically so the whole pipeline runs model-free.
//
// Local-agent contract per op:
//
//	plan       in {question, max_subquestions, max_queries_per}
//	           out {subquestions:[{q, queries:[...]}]}
//	extract    in {question, source_id, text}
//	           out {snippets:[{source_id, span:[start,end], text}]}
//	draft      in {question, snippets:[{source_id, span, text}]}
//	           out {claims:[{claim, cites:[source_id...]}]}   // cites MUST be from input
//	verify     in {claim, snapshot_text}
//	           out {verdict:"supports|partial|unsupported", why}
//	synthesize in {question, verified_claims:[...]}
//	           out {report_sections:[{heading, body_with_[n]_cites}]}
type Helper interface {
	Plan(question string, maxSub, maxPer int) ([]SubQuestion, error)
	Extract(question string, sourceID int, text string) ([]Snippet, error)
	Draft(question string, snippets []Snippet) ([]DraftClaim, error)
	Verify(claim, snapshotText string) (Verdict, error)
	Synthesize(question string, claims []Finding) ([]ReportSection, error)
}

// Backends bundles the three injectable boundaries. A nil field means "that
// capability is unavailable" and the orchestrator degrades around it (SPEC §7):
// nil Search/Fetch → run offline over the cache; nil Helper → sources-only result.
type Backends struct {
	Search Searcher
	Fetch  Fetcher
	Helper Helper
}
