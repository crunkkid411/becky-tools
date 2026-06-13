package beckydb

import (
	"path/filepath"
	"strconv"
	"testing"

	"becky-go/internal/config"
)

// TestSanitizeFTSQuery covers the FTS5 query sanitizer: arbitrary user text ->
// a safe MATCH expression (each alnum token double-quoted, OR-joined). This is
// the guard that keeps special characters from erroring or being read as FTS5
// operators, so it is a pure unit test (no CLI needed).
func TestSanitizeFTSQuery(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"pound of sand", `"pound" OR "of" OR "sand"`},
		{"a-b OR c", `"a" OR "b" OR "OR" OR "c"`}, // FTS5 'OR' operator neutralized as a phrase
		{`pound"`, `"pound"`},                     // unbalanced quote can't leak
		{"  spaced   out  ", `"spaced" OR "out"`},
		{"plate ABC-123", `"plate" OR "ABC" OR "123"`},
		{"under_score", `"under_score"`}, // underscore is kept inside a token
		{"!!!", ""},                      // punctuation-only -> nothing searchable
		{"", ""},
	}
	for _, c := range cases {
		if got := sanitizeFTSQuery(c.in); got != c.want {
			t.Errorf("sanitizeFTSQuery(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFTSErrorClassifiers checks the graceful-degrade error matchers.
func TestFTSErrorClassifiers(t *testing.T) {
	if !isFTS5Unavailable(errString("no such module: fts5")) {
		t.Error("isFTS5Unavailable should match 'no such module: fts5'")
	}
	if !isFTS5Unavailable(errString("unknown tokenizer: porter")) {
		t.Error("isFTS5Unavailable should match unknown tokenizer")
	}
	if isFTS5Unavailable(errString("syntax error near 'SELECT'")) {
		t.Error("isFTS5Unavailable must NOT mask a real SQL error")
	}
	if isFTS5Unavailable(nil) {
		t.Error("isFTS5Unavailable(nil) must be false")
	}
	if !isMissingFTSTable(errString("no such table: segments_fts")) {
		t.Error("isMissingFTSTable should match the missing-table error")
	}
	if isMissingFTSTable(errString("no such table: segments")) {
		t.Error("isMissingFTSTable must be specific to segments_fts")
	}
}

// errString is a tiny error helper for the classifier tests.
type errString string

func (e errString) Error() string { return string(e) }

// TestKeywordSearchRoundTrip exercises the FTS5 keyword path end to end against
// the real sqlite3 CLI: EnsureSchema (creates segments_fts), FTS5Available,
// InsertFTS (idempotent delete+insert), CountFTS, and KeywordSearch (BM25 rank,
// stemming, join back to segments, sanitized special-char query). Skips
// gracefully if the CLI/extension are absent or this sqlite3 lacks FTS5.
func TestKeywordSearchRoundTrip(t *testing.T) {
	cfg := config.Load()
	if cfg.Sqlite3 == "" || cfg.SqliteVecExt == "" {
		t.Skip("sqlite3 CLI or vec0 extension not configured")
	}
	dbPath := filepath.Join(t.TempDir(), "fts.db")
	db, err := Open(cfg, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.EnsureSchema(); err != nil {
		t.Skipf("schema unavailable: %v", err)
	}
	if !db.FTS5Available() {
		t.Skip("FTS5 not available in this sqlite3 build")
	}

	rows := []Segment{
		{SegmentID: "s:0", SourceFile: "v.mp4", SourceSHA256: "abc", StartTime: 0, EndTime: 1, Text: "eat a pound of sand", NeedsReview: 1},
		{SegmentID: "s:1", SourceFile: "v.mp4", SourceSHA256: "abc", StartTime: 1, EndTime: 2, Text: "the cash was in the drawer", NeedsReview: 1},
		{SegmentID: "s:2", SourceFile: "v.mp4", SourceSHA256: "abc", StartTime: 2, EndTime: 3, Text: "I'm buying it today", NeedsReview: 1},
	}
	for _, r := range rows {
		if err := db.UpsertSegment(r); err != nil {
			t.Fatalf("UpsertSegment: %v", err)
		}
		if err := db.InsertFTS(r.SegmentID, r.Text); err != nil {
			t.Fatalf("InsertFTS: %v", err)
		}
	}
	// Re-insert identical FTS rows: count must stay at 3 (delete+insert idempotency).
	for _, r := range rows {
		if err := db.InsertFTS(r.SegmentID, r.Text); err != nil {
			t.Fatalf("InsertFTS(2): %v", err)
		}
	}
	if n, err := db.CountFTS(); err != nil || n != 3 {
		t.Fatalf("CountFTS = %d (err %v), want 3", n, err)
	}

	// Exact phrase -> the matching segment ranks #1 and the row is joined back.
	got, err := db.KeywordSearch("pound of sand", 5)
	if err != nil {
		t.Fatalf("KeywordSearch: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("KeywordSearch returned no rows for an exact phrase")
	}
	if got[0].SegmentID != "s:0" {
		t.Errorf("top keyword hit = %q, want s:0", got[0].SegmentID)
	}
	if got[0].Rank != 1 {
		t.Errorf("top hit Rank = %d, want 1", got[0].Rank)
	}
	if got[0].Text != "eat a pound of sand" {
		t.Errorf("joined text = %q, want the s:0 text", got[0].Text)
	}

	// Porter stemming: querying "buy" must match the stored "buying".
	stem, err := db.KeywordSearch("buy", 5)
	if err != nil {
		t.Fatalf("KeywordSearch(stem): %v", err)
	}
	if len(stem) == 0 || stem[0].SegmentID != "s:2" {
		t.Errorf("stem search = %v, want s:2 first", neighborIDs(stem))
	}

	// A query with FTS5 special chars must NOT error (sanitized).
	if _, err := db.KeywordSearch(`a-b OR c "unbalanced`, 5); err != nil {
		t.Errorf("KeywordSearch(special chars) errored: %v", err)
	}

	// A punctuation-only query sanitizes to empty -> no rows, no error.
	none, err := db.KeywordSearch("!!! ???", 5)
	if err != nil || len(none) != 0 {
		t.Errorf("KeywordSearch(punct) = %v,%v want nil,nil", neighborIDs(none), err)
	}
}

// neighborIDs is a test helper to render a neighbor slice as its ids.
func neighborIDs(ns []Neighbor) []string {
	ids := make([]string, len(ns))
	for i, n := range ns {
		ids[i] = n.SegmentID
	}
	return ids
}

// TestKNNStampsRank confirms KNN now stamps a 1-based Rank (needed for RRF).
func TestKNNStampsRank(t *testing.T) {
	cfg := config.Load()
	if cfg.Sqlite3 == "" || cfg.SqliteVecExt == "" {
		t.Skip("sqlite3 CLI or vec0 extension not configured")
	}
	dbPath := filepath.Join(t.TempDir(), "rank.db")
	db, err := Open(cfg, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.EnsureSchema(); err != nil {
		t.Skipf("schema/vec0 unavailable: %v", err)
	}
	for i := 0; i < 3; i++ {
		r := Segment{SegmentID: "r:" + strconv.Itoa(i), SourceFile: "v.mp4", SourceSHA256: "abc", Text: "t", NeedsReview: 1}
		if err := db.UpsertSegment(r); err != nil {
			t.Fatalf("UpsertSegment: %v", err)
		}
		if err := db.InsertVector(r.SegmentID, vecJSONTest(VectorDim, i)); err != nil {
			t.Fatalf("InsertVector: %v", err)
		}
	}
	got, err := db.KNN(vecJSONTest(VectorDim, 0), 3, 0.0)
	if err != nil {
		t.Fatalf("KNN: %v", err)
	}
	for i, n := range got {
		if n.Rank != i+1 {
			t.Errorf("KNN rank[%d] = %d, want %d", i, n.Rank, i+1)
		}
	}
}

// TestKeywordSearchDegradesWithoutFTSTable proves the read path degrades to empty
// (not an error) when segments_fts is absent — the shape a no-FTS5 sqlite3 build
// produces (EnsureSchema swallowed the failed CREATE). We simulate it by dropping
// the table after creation, then asserting CountFTS and KeywordSearch both return
// empty/no-error so becky-search can fall back to vector-only.
func TestKeywordSearchDegradesWithoutFTSTable(t *testing.T) {
	cfg := config.Load()
	if cfg.Sqlite3 == "" || cfg.SqliteVecExt == "" {
		t.Skip("sqlite3 CLI or vec0 extension not configured")
	}
	dbPath := filepath.Join(t.TempDir(), "degrade.db")
	db, err := Open(cfg, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.EnsureSchema(); err != nil {
		t.Skipf("schema unavailable: %v", err)
	}
	// Drop the FTS table to mimic a build that never created it.
	if err := db.Exec("DROP TABLE IF EXISTS segments_fts;"); err != nil {
		t.Fatalf("drop segments_fts: %v", err)
	}
	if n, err := db.CountFTS(); err != nil || n != 0 {
		t.Errorf("CountFTS without table = %d (err %v), want 0/nil", n, err)
	}
	rows, err := db.KeywordSearch("anything", 5)
	if err != nil {
		t.Errorf("KeywordSearch without table errored: %v (want graceful empty)", err)
	}
	if len(rows) != 0 {
		t.Errorf("KeywordSearch without table = %d rows, want 0", len(rows))
	}
}
