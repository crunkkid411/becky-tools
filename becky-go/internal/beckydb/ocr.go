package beckydb

// ocr.go — self-contained OCR-text storage for becky-ocr. This file owns the
// `ocr_text` table (one row per recognized text line from a frame) and its FTS5
// keyword mirror `ocr_text_fts`, plus the writer/reader the OCR tool needs.
//
// It is DELIBERATELY independent of the canonical schema in schema.sql / schema.go:
// it creates its own tables on demand (EnsureOCRSchema) so adding OCR never
// touches the shared embed/search/consolidate schema path or risks a migration to
// the segments/identifications/media tables those tools depend on. Search slots
// in via OCRKeywordSearch, which returns the same kind of literal-match rows that
// becky-search's keyword half already fuses — OCR text (addresses, plates, names,
// chat handles) is exact-token data, exactly what FTS5/BM25 is for.

import (
	"fmt"
	"strings"
	"time"
)

// OCRSchema is the additive DDL for the OCR-text table and its read-path indexes.
// Kept as a single CREATE-IF-NOT-EXISTS batch so EnsureOCRSchema is idempotent and
// safe to call on every becky-ocr run, mirroring EnsureSchema's contract for the
// core tables. The FTS5 mirror is applied separately (OCRSchemaFTS) so a sqlite3
// built without FTS5 degrades to keyword-less storage instead of aborting.
const OCRSchema = `
-- ocr_text: one recognized text line from one OCR'd frame, with its forensic
-- provenance (source file + SHA-256), where in the video it came from (timestamp,
-- frame_index), where on the frame (bbox), the recognition confidence, and a cheap
-- heuristic category for triage. Keyed deterministically so re-running becky-ocr
-- over the same frames is idempotent: sha12(source_file)+":"+frame_index+":"+ordinal.
CREATE TABLE IF NOT EXISTS ocr_text (
    ocr_id        TEXT PRIMARY KEY, -- deterministic: sha12(source_file)+":"+frame_index+":"+line_ordinal
    source_file   TEXT,             -- path/name of the source media (provenance)
    source_sha256 TEXT,             -- SHA-256 of the source media file (provenance; may be "")
    frame_path    TEXT,             -- the exact frame image OCR'd (the same bytes becky-osint SHA'd)
    timestamp     REAL,             -- seconds into the source video (from the osint sidecar)
    frame_index   INTEGER,          -- frame number (from the osint sidecar)
    text          TEXT,             -- the recognized line of text
    confidence    REAL DEFAULT 0,   -- 0..1 recognition confidence for this line
    category      TEXT,             -- triage hint: candidate_address|candidate_plate|candidate_business|candidate_timestamp|text
    bbox_json     TEXT,             -- [x1,y1,x2,y2] on the (orientation-corrected) frame, as JSON
    created_at    TEXT              -- RFC3339 timestamp this row was written
);

CREATE INDEX IF NOT EXISTS idx_ocr_source ON ocr_text(source_file);
CREATE INDEX IF NOT EXISTS idx_ocr_category ON ocr_text(category);
`

// OCRSchemaFTS is the FTS5 keyword index over OCR line text, applied in its own
// invocation so a no-FTS5 sqlite3 build can degrade gracefully (same isolation
// rationale as SchemaFTS for segments). ocr_id is UNINDEXED so it can be joined
// back to ocr_text; the tokenizer matches segments_fts (porter unicode61) so an
// address/name/plate folds and stems the same way across both indexes.
const OCRSchemaFTS = `
CREATE VIRTUAL TABLE IF NOT EXISTS ocr_text_fts USING fts5(
    ocr_id UNINDEXED,
    text,
    tokenize='porter unicode61'
);
`

// OCRLine is one recognized text line ready to persist. Category is a cheap triage
// hint set by becky-ocr; it is plainly a "candidate_*" label (search/ranking aid),
// never a conclusion.
type OCRLine struct {
	OCRID        string  `json:"ocr_id"`
	SourceFile   string  `json:"source_file"`
	SourceSHA256 string  `json:"source_sha256"`
	FramePath    string  `json:"frame_path"`
	Timestamp    float64 `json:"timestamp"`
	FrameIndex   int     `json:"frame_index"`
	Text         string  `json:"text"`
	Confidence   float64 `json:"confidence"`
	Category     string  `json:"category"`
	BBoxJSON     string  `json:"bbox_json"`
	CreatedAt    string  `json:"created_at"`
}

// EnsureOCRSchema creates the ocr_text table (+ indexes) and, best-effort, its FTS5
// mirror. It is idempotent and additive: it never touches the canonical schema, so
// calling it has no effect on embed/search/consolidate. An FTS5-unavailable build
// is swallowed (callers learn via FTS5Available whether keyword search is live),
// exactly like EnsureSchema.
func (db *DB) EnsureOCRSchema() error {
	if err := db.Exec(OCRSchema); err != nil {
		return fmt.Errorf("ensure ocr_text schema: %w", err)
	}
	if err := db.Exec(OCRSchemaFTS); err != nil {
		if isFTS5Unavailable(err) {
			return nil // no FTS5: store rows without the keyword mirror
		}
		return fmt.Errorf("ensure ocr_text_fts schema: %w", err)
	}
	return nil
}

// InsertOCRLine inserts or replaces one OCR line keyed by OCRID, and refreshes its
// FTS5 mirror row when keyword indexing is live. INSERT OR REPLACE on the base
// table plus DELETE-then-INSERT on the FTS table (FTS5 has no upsert) keeps
// re-running becky-ocr over the same frames idempotent — the same deterministic
// OCRID always lands one row. CreatedAt defaults to now (RFC3339) when empty.
//
// ftsLive should be the caller's cached FTS5Available() result so a no-FTS5 build
// stores the row (base table only) instead of erroring on the missing mirror.
func (db *DB) InsertOCRLine(l OCRLine, ftsLive bool) error {
	if l.CreatedAt == "" {
		l.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	sql := fmt.Sprintf(
		"INSERT OR REPLACE INTO ocr_text "+
			"(ocr_id, source_file, source_sha256, frame_path, timestamp, frame_index, "+
			"text, confidence, category, bbox_json, created_at) "+
			"VALUES (%s, %s, %s, %s, %s, %d, %s, %s, %s, %s, %s);",
		q(l.OCRID), q(l.SourceFile), nullableStr(l.SourceSHA256), q(l.FramePath),
		num(l.Timestamp), l.FrameIndex, q(l.Text), num(l.Confidence),
		nullableStr(l.Category), nullableStr(l.BBoxJSON), q(l.CreatedAt),
	)
	if ftsLive {
		sql += fmt.Sprintf(
			"\nDELETE FROM ocr_text_fts WHERE ocr_id = %s;"+
				"\nINSERT INTO ocr_text_fts(ocr_id, text) VALUES (%s, %s);",
			q(l.OCRID), q(l.OCRID), q(l.Text),
		)
	}
	return db.Exec(sql)
}

// CountOCRLines returns the number of rows in ocr_text. Returns 0 (no error) when
// the table is absent, so a tool's summary is robust on a DB that never ran OCR.
func (db *DB) CountOCRLines() (int, error) {
	n, err := db.scalarInt("SELECT COUNT(*) AS n FROM ocr_text;")
	if err != nil && isMissingOCRTable(err) {
		return 0, nil
	}
	return n, err
}

// OCRHit is one OCR keyword-search result: the stored line plus the raw bm25()
// score (more negative = more relevant) and its 1-based rank in the result list,
// so becky-search can fuse OCR hits into the same Reciprocal Rank Fusion it uses
// for segment keyword hits. Tagged source: ocr by the caller.
type OCRHit struct {
	OCRLine
	BM25 float64 `json:"bm25"`
	Rank int     `json:"rank"`
}

// OCRKeywordSearch runs a BM25 full-text search over ocr_text_fts (the keyword half
// for OCR text) and joins back to ocr_text so a hit on "Chatham" returns the FRAME
// to look at (frame_path + timestamp + source). query is sanitized into a safe FTS5
// MATCH the same way segment keyword search is, so punctuation/operators can't error.
// A query with no usable tokens, or a DB without FTS5 / without the ocr table, yields
// an empty slice (not an error) so search degrades gracefully.
func (db *DB) OCRKeywordSearch(query string, k int) ([]OCRHit, error) {
	if k <= 0 {
		k = 10
	}
	match := sanitizeFTSQuery(query)
	if match == "" {
		return nil, nil
	}
	sql := fmt.Sprintf(
		"SELECT t.ocr_id, t.source_file, COALESCE(t.source_sha256,'') AS source_sha256, "+
			"t.frame_path, t.timestamp, t.frame_index, t.text, t.confidence, "+
			"COALESCE(t.category,'') AS category, COALESCE(t.bbox_json,'') AS bbox_json, "+
			"COALESCE(t.created_at,'') AS created_at, bm25(ocr_text_fts) AS bm25 "+
			"FROM ocr_text_fts f JOIN ocr_text t ON t.ocr_id = f.ocr_id "+
			"WHERE ocr_text_fts MATCH %s "+
			"ORDER BY bm25(ocr_text_fts) LIMIT %d;",
		q(match), k,
	)
	var rows []OCRHit
	if err := db.queryJSON(sql, &rows); err != nil {
		if isMissingOCRFTSTable(err) || isMissingOCRTable(err) {
			return nil, nil
		}
		return nil, err
	}
	for i := range rows {
		rows[i].Rank = i + 1
	}
	return rows, nil
}

// isMissingOCRTable matches "no such table: ocr_text" so reads degrade to empty on
// a DB that never ran becky-ocr instead of failing the calling tool.
func isMissingOCRTable(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "no such table: ocr_text")
}

// isMissingOCRFTSTable matches "no such table: ocr_text_fts" (no-FTS5 build) so the
// OCR keyword search degrades to empty rather than erroring.
func isMissingOCRFTSTable(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "no such table: ocr_text_fts")
}
