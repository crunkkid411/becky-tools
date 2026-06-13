package beckydb

import (
	"path/filepath"
	"testing"

	"becky-go/internal/config"
)

// TestOCRRoundTrip exercises the OCR storage path end to end against the real
// sqlite3 CLI: EnsureOCRSchema (creates ocr_text + ocr_text_fts), InsertOCRLine
// (idempotent INSERT OR REPLACE + delete/insert FTS), CountOCRLines, and
// OCRKeywordSearch (BM25 rank, join back to ocr_text so a hit returns the frame).
// Skips gracefully if the CLI/extension are absent or this sqlite3 lacks FTS5 —
// mirroring TestKeywordSearchRoundTrip.
func TestOCRRoundTrip(t *testing.T) {
	cfg := config.Load()
	if cfg.Sqlite3 == "" || cfg.SqliteVecExt == "" {
		t.Skip("sqlite3 CLI or vec0 extension not configured")
	}
	dbPath := filepath.Join(t.TempDir(), "ocr.db")
	db, err := Open(cfg, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.EnsureOCRSchema(); err != nil {
		t.Skipf("ocr schema unavailable: %v", err)
	}
	ftsLive := db.FTS5Available()
	if !ftsLive {
		t.Skip("FTS5 not available in this sqlite3 build")
	}

	lines := []OCRLine{
		{OCRID: "f:0:0", SourceFile: "clip.mp4", SourceSHA256: "abc", FramePath: "osint/loc_3s.jpg",
			Timestamp: 3.0, FrameIndex: 90, Text: "2601 CHATHAM CIR", Confidence: 0.93,
			Category: "candidate_address", BBoxJSON: "[12,40,318,96]"},
		{OCRID: "f:0:1", SourceFile: "clip.mp4", SourceSHA256: "abc", FramePath: "osint/loc_3s.jpg",
			Timestamp: 3.0, FrameIndex: 90, Text: "ya cat is gonna be mine", Confidence: 0.99,
			Category: "text", BBoxJSON: "[22,110,290,164]"},
	}
	for _, l := range lines {
		if err := db.InsertOCRLine(l, ftsLive); err != nil {
			t.Fatalf("InsertOCRLine: %v", err)
		}
	}
	// Re-insert identical rows: count must stay at 2 (INSERT OR REPLACE + FTS
	// delete/insert idempotency on the deterministic ocr_id).
	for _, l := range lines {
		if err := db.InsertOCRLine(l, ftsLive); err != nil {
			t.Fatalf("InsertOCRLine(2): %v", err)
		}
	}
	if n, err := db.CountOCRLines(); err != nil || n != 2 {
		t.Fatalf("CountOCRLines = %d (err %v), want 2", n, err)
	}

	// `becky find "Chatham"` -> the address line ranks #1 and the row (frame path,
	// timestamp, confidence) is joined back.
	got, err := db.OCRKeywordSearch("Chatham", 5)
	if err != nil {
		t.Fatalf("OCRKeywordSearch: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("OCRKeywordSearch returned no rows for 'Chatham'")
	}
	if got[0].OCRID != "f:0:0" {
		t.Errorf("top hit = %q, want f:0:0", got[0].OCRID)
	}
	if got[0].FramePath != "osint/loc_3s.jpg" {
		t.Errorf("joined frame_path = %q, want osint/loc_3s.jpg", got[0].FramePath)
	}
	if got[0].Rank != 1 {
		t.Errorf("top hit Rank = %d, want 1", got[0].Rank)
	}
	if got[0].Confidence != 0.93 {
		t.Errorf("joined confidence = %v, want 0.93", got[0].Confidence)
	}

	// A query with FTS5 special chars must NOT error (sanitized, like segments).
	if _, err := db.OCRKeywordSearch(`a-b OR "unbalanced`, 5); err != nil {
		t.Errorf("OCRKeywordSearch(special chars) errored: %v", err)
	}
}

// TestOCRDegradesWithoutTable proves OCR reads degrade to empty (not an error)
// when ocr_text / ocr_text_fts are absent — the shape on a DB that never ran
// becky-ocr (or a no-FTS5 build). We drop the tables, then assert CountOCRLines
// and OCRKeywordSearch return empty/no-error.
func TestOCRDegradesWithoutTable(t *testing.T) {
	cfg := config.Load()
	if cfg.Sqlite3 == "" || cfg.SqliteVecExt == "" {
		t.Skip("sqlite3 CLI or vec0 extension not configured")
	}
	dbPath := filepath.Join(t.TempDir(), "ocr-degrade.db")
	db, err := Open(cfg, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.EnsureOCRSchema(); err != nil {
		t.Skipf("ocr schema unavailable: %v", err)
	}
	if err := db.Exec("DROP TABLE IF EXISTS ocr_text_fts; DROP TABLE IF EXISTS ocr_text;"); err != nil {
		t.Fatalf("drop ocr tables: %v", err)
	}
	if n, err := db.CountOCRLines(); err != nil || n != 0 {
		t.Errorf("CountOCRLines without table = %d (err %v), want 0/nil", n, err)
	}
	rows, err := db.OCRKeywordSearch("anything", 5)
	if err != nil {
		t.Errorf("OCRKeywordSearch without table errored: %v (want graceful empty)", err)
	}
	if len(rows) != 0 {
		t.Errorf("OCRKeywordSearch without table = %d rows, want 0", len(rows))
	}
}

// TestOCRErrorClassifiers checks the OCR graceful-degrade error matchers.
func TestOCRErrorClassifiers(t *testing.T) {
	if !isMissingOCRTable(errString("no such table: ocr_text")) {
		t.Error("isMissingOCRTable should match the missing-table error")
	}
	if isMissingOCRTable(errString("no such table: segments")) {
		t.Error("isMissingOCRTable must be specific to ocr_text")
	}
	if !isMissingOCRFTSTable(errString("no such table: ocr_text_fts")) {
		t.Error("isMissingOCRFTSTable should match the missing-fts-table error")
	}
	if isMissingOCRFTSTable(nil) {
		t.Error("isMissingOCRFTSTable(nil) must be false")
	}
}
