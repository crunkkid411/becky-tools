package main

import (
	"math"
	"testing"

	"becky-go/internal/beckydb"
)

// ocrHit is a tiny helper to build an OCRHit with text and a 1-based rank.
func ocrHit(text string, rank int) beckydb.OCRHit {
	return beckydb.OCRHit{
		OCRLine: beckydb.OCRLine{Text: text, FramePath: text + ".jpg"},
		Rank:    rank,
	}
}

func TestRRFScore(t *testing.T) {
	const k = 60
	if got, want := rrfScore(1, k), 1.0/61.0; math.Abs(got-want) > 1e-12 {
		t.Errorf("rrfScore(1) = %v want %v", got, want)
	}
	if got, want := rrfScore(2, k), 1.0/62.0; math.Abs(got-want) > 1e-12 {
		t.Errorf("rrfScore(2) = %v want %v", got, want)
	}
	// Higher rank (further down a list) => smaller contribution.
	if rrfScore(5, k) >= rrfScore(1, k) {
		t.Error("rrfScore must decrease as rank increases")
	}
}

// TestFuseUnified_TranscriptScoresUnchanged is the core no-regression guarantee:
// adding the OCR list as a third RRF input must NOT change any transcript doc's
// fused score (the two doc sets are disjoint). The transcript verdict from fuseRRF
// is carried through verbatim.
func TestFuseUnified_TranscriptScoresUnchanged(t *testing.T) {
	const k = 60
	// Transcript fusion result (as fuseRRF would have produced it).
	segOrder := []beckydb.Neighbor{nb("a", 0.70, 1), nb("b", 0.60, 2)}
	fusedSeg := map[string]fusionInfo{
		"a": {matched: "hybrid", fused: 0.5},
		"b": {matched: "vector", fused: 0.25},
	}
	ocr := []beckydb.OCRHit{ocrHit("Greenwood", 1)}

	items := fuseUnified(segOrder, fusedSeg, ocr, k)

	// Every transcript item keeps its exact fuseRRF score + label.
	for _, it := range items {
		if it.kind != kindSegment {
			continue
		}
		want := fusedSeg[it.seg.SegmentID]
		if it.fused != want.fused {
			t.Errorf("transcript %s fused = %v, want %v (unchanged by OCR)", it.seg.SegmentID, it.fused, want.fused)
		}
		if it.matched != want.matched {
			t.Errorf("transcript %s matched = %q, want %q", it.seg.SegmentID, it.matched, want.matched)
		}
	}

	// The OCR item is present, tagged "ocr", with an RRF score from its rank.
	var sawOCR bool
	for _, it := range items {
		if it.kind == kindOCR {
			sawOCR = true
			if it.matched != "ocr" {
				t.Errorf("ocr matched = %q want ocr", it.matched)
			}
			if math.Abs(it.fused-rrfScore(1, k)) > 1e-12 {
				t.Errorf("ocr fused = %v want %v", it.fused, rrfScore(1, k))
			}
		}
	}
	if !sawOCR {
		t.Fatal("OCR hit was not present in unified results")
	}
}

// TestFuseUnified_InterleavesByScore proves an OCR hit lands in the right ranked
// position relative to transcript hits, purely by fused score. OCR rank-1
// (1/61 ≈ 0.0164) should sort between transcript fused 0.5 and 0.01.
func TestFuseUnified_InterleavesByScore(t *testing.T) {
	const k = 60
	segOrder := []beckydb.Neighbor{nb("hi", 0.9, 1), nb("lo", 0.5, 2)}
	fusedSeg := map[string]fusionInfo{
		"hi": {matched: "hybrid", fused: 0.5},
		"lo": {matched: "keyword", fused: 0.01},
	}
	ocr := []beckydb.OCRHit{ocrHit("Greenwood", 1)} // fused ≈ 0.0164

	items := fuseUnified(segOrder, fusedSeg, ocr, k)
	if len(items) != 3 {
		t.Fatalf("want 3 items, got %d", len(items))
	}
	// Expected order by score: hi (0.5), ocr (0.0164), lo (0.01).
	if items[0].kind != kindSegment || items[0].seg.SegmentID != "hi" {
		t.Errorf("items[0] should be transcript 'hi'")
	}
	if items[1].kind != kindOCR {
		t.Errorf("items[1] should be the OCR hit (0.0164 > 0.01), got kind %d", items[1].kind)
	}
	if items[2].kind != kindSegment || items[2].seg.SegmentID != "lo" {
		t.Errorf("items[2] should be transcript 'lo'")
	}
}

// TestFuseUnified_TieTranscriptLeadsOCR checks the deterministic tiebreak: at an
// equal fused score, the transcript (spoken) hit precedes the OCR hit.
func TestFuseUnified_TieTranscriptLeadsOCR(t *testing.T) {
	const k = 60
	// Transcript fused score set EQUAL to OCR rank-1 score so the tiebreak decides.
	tie := rrfScore(1, k)
	segOrder := []beckydb.Neighbor{nb("seg", 0.5, 1)}
	fusedSeg := map[string]fusionInfo{"seg": {matched: "keyword", fused: tie}}
	ocr := []beckydb.OCRHit{ocrHit("Greenwood", 1)}

	items := fuseUnified(segOrder, fusedSeg, ocr, k)
	if items[0].kind != kindSegment {
		t.Errorf("on a score tie, transcript must lead OCR; items[0] kind = %d", items[0].kind)
	}
	if items[1].kind != kindOCR {
		t.Errorf("items[1] should be OCR; kind = %d", items[1].kind)
	}
}

// TestFuseUnifiedSingleSignal_OCRDoesNotAlwaysWin proves the single-signal merge
// (keyword/vector mode, where transcript docs have no RRF score) puts transcript
// docs on the SAME RRF-by-rank scale as OCR, so a top transcript hit is not buried
// beneath OCR. A flat transcript score of 0 was the bug this guards against.
func TestFuseUnifiedSingleSignal_OCRDoesNotAlwaysWin(t *testing.T) {
	const k = 60
	// Transcript keyword hits at rank 1 and 2; OCR hits at rank 1 and 2.
	neighbors := []beckydb.Neighbor{nb("kw1", 0, 1), nb("kw2", 0, 2)}
	ocr := []beckydb.OCRHit{ocrHit("o1", 1), ocrHit("o2", 2)}

	items := fuseUnifiedSingleSignal(neighbors, "keyword", ocr, k)
	if len(items) != 4 {
		t.Fatalf("want 4 items, got %d", len(items))
	}
	// rank-1 transcript and rank-1 OCR tie at 1/61; transcript leads. Then rank-2
	// transcript and rank-2 OCR tie at 1/62; transcript leads. Order: kw1, o1, kw2, o2.
	wantKinds := []itemKind{kindSegment, kindOCR, kindSegment, kindOCR}
	for i, wk := range wantKinds {
		if items[i].kind != wk {
			t.Errorf("items[%d] kind = %d, want %d", i, items[i].kind, wk)
		}
	}
	if items[0].seg.SegmentID != "kw1" {
		t.Errorf("items[0] should be kw1 (rank-1 transcript), got %q", items[0].seg.SegmentID)
	}
	if items[0].matched != "keyword" {
		t.Errorf("transcript label = %q want keyword", items[0].matched)
	}
}

// TestFuseUnifiedSingleSignal_OCROnly handles the common forensic case: the query
// only matches on-screen text (no transcript hit at all).
func TestFuseUnifiedSingleSignal_OCROnly(t *testing.T) {
	const k = 60
	ocr := []beckydb.OCRHit{ocrHit("2601 Chatham", 1), ocrHit("Greenwood", 2)}
	items := fuseUnifiedSingleSignal(nil, "keyword", ocr, k)
	if len(items) != 2 {
		t.Fatalf("want 2 OCR items, got %d", len(items))
	}
	for _, it := range items {
		if it.kind != kindOCR || it.matched != "ocr" {
			t.Errorf("expected OCR item, got kind %d matched %q", it.kind, it.matched)
		}
	}
	// Higher-ranked OCR hit sorts first.
	if items[0].ocr.Text != "2601 Chatham" {
		t.Errorf("items[0] = %q want '2601 Chatham'", items[0].ocr.Text)
	}
}

func TestCapItems(t *testing.T) {
	items := []rankedItem{{}, {}, {}}
	if got := capItems(items, 2); len(got) != 2 {
		t.Errorf("capItems(.,2) len = %d want 2", len(got))
	}
	if got := capItems(items, 0); len(got) != 3 {
		t.Errorf("capItems(.,0) should not cap; len = %d want 3", len(got))
	}
	if got := capItems(items, 10); len(got) != 3 {
		t.Errorf("capItems(.,10) len = %d want 3", len(got))
	}
}
