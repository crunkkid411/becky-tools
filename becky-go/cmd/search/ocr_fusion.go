package main

// ocr_fusion.go — folds on-screen OCR text into the SAME unified search as spoken
// transcript text. becky-ocr writes recognized frame text into ocr_text (+ its
// FTS5 mirror ocr_text_fts) via the becky-ocr build; this file consumes
// beckydb.OCRKeywordSearch (read-only) and fuses its hits into the same
// Reciprocal Rank Fusion becky-search already runs over the transcript vector +
// keyword lists.
//
// WHY this preserves transcript ranking exactly:
// RRF scores a doc as Σ 1/(rrfK + rank) over the ranked lists it appears in.
// Transcript segments (keyed by segment_id) and OCR lines (keyed by ocr_id) are
// DISJOINT doc sets — an OCR line never appears in the vector/keyword transcript
// lists and a segment never appears in the OCR list. So adding the OCR list as a
// third RRF input adds OCR rows to the merged ranking WITHOUT changing any
// transcript doc's score. An address read off a frame surfaces alongside a spoken
// phrase, each labelled by source (matched:"vector|keyword|hybrid" for transcript,
// matched:"ocr" for on-screen text), and the existing transcript order is intact.
//
// OCR participates in hybrid and keyword modes (it is exact-token, BM25-shaped
// data — the keyword half). Pure --mode vector deliberately skips it, mirroring
// how the transcript keyword half is also skipped in vector-only mode (no FTS5
// load, no keyword signal at all).

import (
	"sort"

	"becky-go/internal/beckydb"
	"becky-go/internal/beckyio"
)

// itemKind tags whether a ranked item came from a spoken transcript segment or an
// on-screen OCR line, so the output layer can render each correctly.
type itemKind int

const (
	kindSegment itemKind = iota // a transcript segment (vector/keyword/hybrid)
	kindOCR                     // an on-screen OCR text line (matched:"ocr")
)

// rankedItem is one fused, ranked search hit in the UNIFIED result set: either a
// transcript segment or an OCR line, carrying its RRF fused score and the matched
// signal label. Exactly one of seg/ocr is populated (per kind). Keeping both shapes
// in one slice lets transcript and OCR hits interleave by score in a single ranking.
type rankedItem struct {
	kind    itemKind
	seg     beckydb.Neighbor // populated when kind == kindSegment
	ocr     beckydb.OCRHit   // populated when kind == kindOCR
	matched string           // "hybrid" | "vector" | "keyword" | "ocr"
	fused   float64          // RRF fused score (the cross-source comparable scale)
}

// ocrList runs the OCR keyword search (the on-screen-text half) and returns its
// hits, capped at limit. It needs no query embedding. Like the transcript keyword
// half, a missing FTS5 build / missing ocr table yields an empty slice (not an
// error) so search degrades gracefully — OCRKeywordSearch already swallows those.
// A genuine query error is fatal (the query truly can't be served).
func ocrList(db *beckydb.DB, query string, p retrieveParams) []beckydb.OCRHit {
	beckyio.Logf(p.verbose, "OCR keyword search (k=%d)...", p.limit)
	hits, err := db.OCRKeywordSearch(query, p.limit)
	if err != nil {
		beckyio.Fatalf("ocr keyword search: %v", err)
	}
	beckyio.Logf(p.verbose, "OCR keyword search returned %d result(s)", len(hits))
	return hits
}

// rrfScore is the Reciprocal Rank Fusion contribution of a hit at 1-based rank
// within a list: 1 / (rrfK + rank). The SAME formula fuseRRF uses for transcript
// docs, so OCR and transcript hits live on one comparable scale.
func rrfScore(rank, rrfK int) float64 {
	return 1.0 / float64(rrfK+rank)
}

// fuseUnified merges the transcript ranking (already fused across vector+keyword by
// fuseRRF) with the OCR ranking into ONE score-sorted result list. It does NOT
// re-fuse the transcript docs (they keep the score fuseRRF gave them); it adds each
// OCR line with its own RRF score (rrfScore over its OCR-list rank). Because the two
// doc sets are disjoint, this is a pure merge — transcript scores are untouched.
//
// fusedSeg maps segment_id -> the transcript fusion verdict from fuseRRF. segOrder
// is the transcript neighbors in fuseRRF's sorted order (so transcript ties keep
// their deterministic resolution). ocrHits is the OCR ranking.
//
// The merged list is sorted by fused score desc; ties break deterministically:
// transcript before OCR at equal score (spoken evidence leads), then by the stable
// per-source order, so identical inputs always produce identical output.
func fuseUnified(segOrder []beckydb.Neighbor, fusedSeg map[string]fusionInfo, ocrHits []beckydb.OCRHit, rrfK int) []rankedItem {
	items := make([]rankedItem, 0, len(segOrder)+len(ocrHits))

	// Transcript items keep their fuseRRF score + matched label verbatim.
	for _, n := range segOrder {
		fi := fusedSeg[n.SegmentID]
		label := fi.matched
		if label == "" {
			label = "vector"
		}
		items = append(items, rankedItem{
			kind:    kindSegment,
			seg:     n,
			matched: label,
			fused:   fi.fused,
		})
	}

	// OCR items: each gets its RRF score from its 1-based OCR-list rank.
	for _, h := range ocrHits {
		items = append(items, rankedItem{
			kind:    kindOCR,
			ocr:     h,
			matched: "ocr",
			fused:   rrfScore(h.Rank, rrfK),
		})
	}

	return sortItems(items)
}

// fuseUnifiedSingleSignal is the merge for single-signal transcript modes (vector
// or keyword), where fuseRRF did not run and transcript docs have no RRF score.
// To keep ONE comparable scale, it assigns transcript docs an RRF score from their
// own list rank (rrfScore over 1-based position) — the same scale the OCR list uses
// — then merges. This keeps the transcript order within itself intact (rank is
// monotonic) while letting OCR hits interleave fairly instead of always sorting
// first (which a flat fused=0 on the transcript side would cause).
func fuseUnifiedSingleSignal(neighbors []beckydb.Neighbor, label string, ocrHits []beckydb.OCRHit, rrfK int) []rankedItem {
	items := make([]rankedItem, 0, len(neighbors)+len(ocrHits))
	for i, n := range neighbors {
		rank := n.Rank
		if rank <= 0 {
			rank = i + 1 // defensive: fall back to position if rank wasn't stamped
		}
		items = append(items, rankedItem{
			kind:    kindSegment,
			seg:     n,
			matched: label,
			fused:   rrfScore(rank, rrfK),
		})
	}
	for _, h := range ocrHits {
		items = append(items, rankedItem{
			kind:    kindOCR,
			ocr:     h,
			matched: "ocr",
			fused:   rrfScore(h.Rank, rrfK),
		})
	}
	return sortItems(items)
}

// sortItems applies the deterministic unified ordering: fused score desc; at equal
// score transcript (kindSegment) leads OCR (kindOCR); within a kind the input order
// (already deterministic per source) is preserved by SliceStable. Identical inputs
// therefore always produce identical output.
func sortItems(items []rankedItem) []rankedItem {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].fused != items[j].fused {
			return items[i].fused > items[j].fused
		}
		return items[i].kind < items[j].kind // kindSegment(0) before kindOCR(1)
	})
	return items
}

// capItems truncates the unified list to limit (limit <= 0 means no cap).
func capItems(items []rankedItem, limit int) []rankedItem {
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}
