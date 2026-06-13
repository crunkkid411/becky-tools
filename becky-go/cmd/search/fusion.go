package main

import (
	"sort"

	"becky-go/internal/beckydb"
	"becky-go/internal/beckyio"
	"becky-go/internal/config"
)

// retrieveParams bundles the knobs retrieve() needs (keeps its signature small).
type retrieveParams struct {
	mode      string // hybrid | vector | keyword (already validated/lowercased)
	limit     int    // max final results
	minConf   float64
	rrfK      int
	verbose   bool
	model     string // friendly query embedding model (qwen3-4b | qwen3-0.6b)
	serverURL string // resident embedding server URL (qwen3-4b path)
}

// fusionInfo is the per-result verdict the output layer needs: which signal(s)
// matched a segment and its fused (RRF) score. Kept in cmd/search (not beckydb)
// because fusion is a search-layer concern; beckydb stays a plain data layer.
type fusionInfo struct {
	matched string  // "hybrid" | "vector" | "keyword"
	fused   float64 // RRF score (hybrid only); 0 for single-signal modes
}

// retrieve runs the requested retrieval mode and returns the UNIFIED ranked items
// (transcript segments AND on-screen OCR lines fused into one ranking), the
// effective mode actually run (may differ from requested after a graceful degrade),
// and a human note when it degraded. It never crashes on an empty/new DB or a
// missing FTS5 build:
//
//   - vector  : embed query → KNN (existing path). Transcript matched="vector".
//     Pure vector mode does NOT consult OCR (no keyword/FTS5 half at all).
//   - keyword : BM25 over segments_fts + OCR keyword search over ocr_text_fts,
//     merged into one ranking. Transcript matched="keyword", OCR matched="ocr".
//     No query embedding. If FTS5 is unavailable it degrades to vector-only.
//   - hybrid  : (KNN ∪ BM25) fused by RRF, THEN merged with the OCR keyword
//     ranking. Transcript matched is "hybrid"/"vector"/"keyword"; OCR is "ocr".
//     If FTS5 is unavailable it degrades to vector-only (with a note).
//
// On-screen text (addresses, plates, names, chat handles) is exact-token data — the
// keyword half — so OCR rides the keyword/hybrid paths, never the vector-only path.
func retrieve(db *beckydb.DB, cfg config.Config, query string, p retrieveParams) ([]rankedItem, string, string) {
	switch p.mode {
	case "vector":
		return vectorOnly(db, cfg, query, p)
	case "keyword":
		return keywordMode(db, cfg, query, p)
	default: // hybrid
		return hybridMode(db, cfg, query, p)
	}
}

// vectorOnly is the classic KNN path: embed the query, run KNN, tag matched. OCR is
// intentionally absent here — pure vector mode loads no keyword signal at all.
func vectorOnly(db *beckydb.DB, cfg config.Config, query string, p retrieveParams) ([]rankedItem, string, string) {
	neighbors := vectorList(db, cfg, query, p)
	items := capItems(fuseUnifiedSingleSignal(neighbors, "vector", nil, p.rrfK), p.limit)
	return items, "vector", ""
}

// keywordMode runs BM25 over the transcript AND the OCR keyword search, merging both
// into one ranking. It needs no query embedding (a big speed win — no model load).
// If this sqlite3 lacks FTS5 it degrades to vector-only with a note (OCR also rides
// FTS5, so a no-FTS5 build has no keyword signal of either kind).
func keywordMode(db *beckydb.DB, cfg config.Config, query string, p retrieveParams) ([]rankedItem, string, string) {
	if !db.FTS5Available() {
		beckyio.Logf(p.verbose, "FTS5 unavailable; --mode keyword degrading to vector-only")
		items, _, _ := vectorOnly(db, cfg, query, p)
		return items, "vector", "FTS5 not available in this sqlite3 build; keyword search degraded to vector-only"
	}
	neighbors := keywordList(db, query, p)
	ocr := ocrList(db, query, p)
	items := capItems(fuseUnifiedSingleSignal(neighbors, "keyword", ocr, p.rrfK), p.limit)
	return items, "keyword", ""
}

// hybridMode fuses the vector and keyword transcript rankings with Reciprocal Rank
// Fusion, then merges the OCR keyword ranking into the same fused order.
func hybridMode(db *beckydb.DB, cfg config.Config, query string, p retrieveParams) ([]rankedItem, string, string) {
	vec := vectorList(db, cfg, query, p)

	if !db.FTS5Available() {
		beckyio.Logf(p.verbose, "FTS5 unavailable; --mode hybrid degrading to vector-only")
		items := capItems(fuseUnifiedSingleSignal(vec, "vector", nil, p.rrfK), p.limit)
		return items, "vector", "FTS5 not available in this sqlite3 build; hybrid search degraded to vector-only"
	}
	kw := keywordList(db, query, p)
	ocr := ocrList(db, query, p)

	// Fuse transcript vector+keyword first (unchanged math), then merge OCR in. If
	// the keyword side found nothing it's effectively vector-only, but still a valid
	// hybrid run (FTS5 was consulted), so keep mode="hybrid" and no degrade note.
	fusedSeg, info := fuseRRF(vec, kw, p.rrfK)
	items := capItems(fuseUnified(fusedSeg, info, ocr, p.rrfK), p.limit)
	return items, "hybrid", ""
}

// vectorList embeds the query and returns the KNN neighbors (similarity-filtered
// by minConf, capped at limit). A fatal embed/KNN error stops the tool (the
// query genuinely can't be served); an empty DB simply yields no rows.
func vectorList(db *beckydb.DB, cfg config.Config, query string, p retrieveParams) []beckydb.Neighbor {
	beckyio.Logf(p.verbose, "embedding query %q with model %s ...", query, p.model)
	vec, err := embedQuery(cfg, query, p.model, p.serverURL, p.verbose)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	beckyio.Logf(p.verbose, "query embedded to %d-dim vector; KNN (k=%d, min-confidence=%g)...",
		len(vec), p.limit, p.minConf)
	neighbors, err := db.KNN(vecJSON(vec), p.limit, p.minConf)
	if err != nil {
		beckyio.Fatalf("knn search: %v", err)
	}
	beckyio.Logf(p.verbose, "KNN returned %d result(s)", len(neighbors))
	return neighbors
}

// keywordList runs the BM25 keyword search (capped at limit). FTS5 must already
// be known available (callers gate on FTS5Available); a query error is fatal,
// but a no-hit / sanitized-empty query just yields no rows.
func keywordList(db *beckydb.DB, query string, p retrieveParams) []beckydb.Neighbor {
	beckyio.Logf(p.verbose, "BM25 keyword search (k=%d)...", p.limit)
	rows, err := db.KeywordSearch(query, p.limit)
	if err != nil {
		beckyio.Fatalf("keyword search: %v", err)
	}
	beckyio.Logf(p.verbose, "keyword search returned %d result(s)", len(rows))
	return rows
}

// tagAll builds a fusionInfo map tagging every neighbor with one signal and a
// zero fused score (single-signal modes don't fuse).
func tagAll(neighbors []beckydb.Neighbor, signal string) map[string]fusionInfo {
	info := make(map[string]fusionInfo, len(neighbors))
	for _, n := range neighbors {
		info[n.SegmentID] = fusionInfo{matched: signal}
	}
	return info
}

// fuseRRF fuses the vector and keyword rankings by Reciprocal Rank Fusion:
//
//	score(d) = Σ over lists d appears in of 1 / (rrfK + rank_d)
//
// where rank_d is the 1-based rank within that list. It returns the fused
// neighbors sorted by score descending (the caller re-numbers rank via
// buildResults) plus the per-segment verdict (matched signal + fused score).
//
// Each fused neighbor carries the VECTOR row when the doc was in the vector list
// (so it keeps its cosine similarity), else the keyword row (similarity 0). The
// sort is deterministic: ties on fused score break by higher similarity, then by
// segment_id ascending, so identical inputs always produce identical output.
func fuseRRF(vec, kw []beckydb.Neighbor, rrfK int) ([]beckydb.Neighbor, map[string]fusionInfo) {
	type acc struct {
		n        beckydb.Neighbor
		score    float64
		inVector bool
		inKw     bool
	}
	byID := make(map[string]*acc)
	order := make([]string, 0, len(vec)+len(kw))

	add := func(n beckydb.Neighbor, fromVector bool) {
		a, ok := byID[n.SegmentID]
		if !ok {
			a = &acc{n: n}
			byID[n.SegmentID] = a
			order = append(order, n.SegmentID)
		}
		a.score += 1.0 / float64(rrfK+n.Rank)
		if fromVector {
			a.inVector = true
			a.n = n // prefer the vector row (carries cosine similarity)
		} else {
			a.inKw = true
			if !a.inVector {
				a.n = n // keyword-only: use the keyword row (similarity 0)
			}
		}
	}
	for _, n := range vec {
		add(n, true)
	}
	for _, n := range kw {
		add(n, false)
	}

	// Deterministic sort: fused score desc, then similarity desc, then id asc.
	sort.SliceStable(order, func(i, j int) bool {
		ai, aj := byID[order[i]], byID[order[j]]
		if ai.score != aj.score {
			return ai.score > aj.score
		}
		if ai.n.Similarity != aj.n.Similarity {
			return ai.n.Similarity > aj.n.Similarity
		}
		return order[i] < order[j]
	})

	fused := make([]beckydb.Neighbor, 0, len(order))
	info := make(map[string]fusionInfo, len(order))
	for _, id := range order {
		a := byID[id]
		fused = append(fused, a.n)
		info[id] = fusionInfo{matched: matchedLabel(a.inVector, a.inKw), fused: a.score}
	}
	return fused, info
}

// matchedLabel names which signal(s) hit a fused doc.
func matchedLabel(inVector, inKw bool) string {
	switch {
	case inVector && inKw:
		return "hybrid"
	case inKw:
		return "keyword"
	default:
		return "vector"
	}
}
