package beckydb

import (
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"becky-go/internal/config"
)

// TestLoadArg checks extension-path normalization for the .load dot-command.
func TestLoadArg(t *testing.T) {
	cases := map[string]string{
		`X:\a\b\vec0.dll`:  "X:/a/b/vec0",
		"X:/a/b/vec0":      "X:/a/b/vec0",
		"/usr/lib/vec0.so": "/usr/lib/vec0",
	}
	for in, want := range cases {
		if got := loadArg(in); got != want {
			t.Errorf("loadArg(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestQEscaping confirms SQL string literals double embedded apostrophes.
func TestQEscaping(t *testing.T) {
	if got := q("I'm not buying it"); got != "'I''m not buying it'" {
		t.Errorf("q = %q", got)
	}
	if got := nullableStr(""); got != "NULL" {
		t.Errorf("nullableStr(empty) = %q, want NULL", got)
	}
	if got := nullableStr("Defendant"); got != "'Defendant'" {
		t.Errorf("nullableStr = %q", got)
	}
}

// vecJSONTest builds a vec0-ready JSON array of dim floats with one component set.
func vecJSONTest(dim, hot int) string {
	parts := make([]string, dim)
	for i := range parts {
		if i == hot {
			parts[i] = "1"
		} else {
			parts[i] = "0"
		}
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// TestRoundTripKNN exercises the real sqlite3 CLI + vec0 extension end to end:
// EnsureSchema, UpsertSegment (incl. idempotency + apostrophe text), InsertVector,
// CountSegments/CountVectors, and KNN self-match (distance 0 / similarity 1).
//
// It skips gracefully if the CLI or extension are not present on this machine.
func TestRoundTripKNN(t *testing.T) {
	cfg := config.Load()
	if cfg.Sqlite3 == "" || cfg.SqliteVecExt == "" {
		t.Skip("sqlite3 CLI or vec0 extension not configured")
	}

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(cfg, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.EnsureSchema(); err != nil {
		t.Skipf("schema/vec0 unavailable: %v", err)
	}
	// EnsureSchema must be idempotent.
	if err := db.EnsureSchema(); err != nil {
		t.Fatalf("EnsureSchema not idempotent: %v", err)
	}

	const dim = VectorDim
	rows := []Segment{
		{SegmentID: "s:0", SourceFile: "v.mp4", SourceSHA256: "abc", StartTime: 0, EndTime: 1, Text: "I'm not buying it", NeedsReview: 1},
		{SegmentID: "s:1", SourceFile: "v.mp4", SourceSHA256: "abc", StartTime: 1, EndTime: 2, Text: "the cash was in the drawer", NeedsReview: 1},
	}
	for i, r := range rows {
		if err := db.UpsertSegment(r); err != nil {
			t.Fatalf("UpsertSegment: %v", err)
		}
		if err := db.InsertVector(r.SegmentID, vecJSONTest(dim, i)); err != nil {
			t.Fatalf("InsertVector: %v", err)
		}
	}

	// Re-run the same writes: counts must stay at 2 (idempotency).
	for i, r := range rows {
		if err := db.UpsertSegment(r); err != nil {
			t.Fatalf("UpsertSegment(2): %v", err)
		}
		if err := db.InsertVector(r.SegmentID, vecJSONTest(dim, i)); err != nil {
			t.Fatalf("InsertVector(2): %v", err)
		}
	}

	if n, err := db.CountSegments(); err != nil || n != 2 {
		t.Fatalf("CountSegments = %d (err %v), want 2", n, err)
	}
	if n, err := db.CountVectors(); err != nil || n != 2 {
		t.Fatalf("CountVectors = %d (err %v), want 2", n, err)
	}

	// KNN: query with s:0's own vector -> it must come back first at similarity 1.
	got, err := db.KNN(vecJSONTest(dim, 0), 2, 0.0)
	if err != nil {
		t.Fatalf("KNN: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("KNN returned no rows")
	}
	if got[0].SegmentID != "s:0" {
		t.Errorf("KNN top = %q, want s:0", got[0].SegmentID)
	}
	if math.Abs(got[0].Similarity-1.0) > 1e-4 {
		t.Errorf("self-match similarity = %v, want 1.0", got[0].Similarity)
	}
	// Joined row must carry the segment text (apostrophe survived escaping).
	if got[0].Text != "I'm not buying it" {
		t.Errorf("joined text = %q", got[0].Text)
	}

	// minSim filter: a high threshold drops the orthogonal neighbor.
	filtered, err := db.KNN(vecJSONTest(dim, 0), 2, 0.99)
	if err != nil {
		t.Fatalf("KNN(filtered): %v", err)
	}
	if len(filtered) != 1 {
		t.Errorf("KNN(minSim=0.99) returned %d rows, want 1", len(filtered))
	}
}

// TestNumFormat ensures floats are SQL-safe (no scientific notation for typical
// embedding magnitudes).
func TestNumFormat(t *testing.T) {
	if got := num(0.123456); got != "0.123456" {
		t.Errorf("num = %q", got)
	}
	// Sanity: round-trips back to a float.
	if _, err := strconv.ParseFloat(num(-0.084961), 64); err != nil {
		t.Errorf("num produced unparseable float: %v", err)
	}
}

// TestSegmentIndex parses the trailing index from the shared "<sha12>:<i>" key.
func TestSegmentIndex(t *testing.T) {
	cases := []struct {
		id   string
		want int
		ok   bool
	}{
		{"bdd5a9bc9823:6", 6, true},
		{"abc:0", 0, true},
		{"abc:12", 12, true},
		{"noindex", 0, false},
		{"trailing:", 0, false},
		{"bad:xx", 0, false},
	}
	for _, c := range cases {
		got, ok := segmentIndex(c.id)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("segmentIndex(%q) = (%d,%v), want (%d,%v)", c.id, got, ok, c.want, c.ok)
		}
	}
}

// TestEmbedModelTag exercises the embed_meta model-tag helpers against the real
// sqlite3 CLI — the guard that stops becky-search/embed from mixing two vector
// spaces. A fresh DB reads "" (no model); SetEmbedModel records it; GetEmbedModel
// reads it back; SetEmbedModel is idempotent (INSERT OR REPLACE on the key).
// Skips gracefully without the CLI/extension.
func TestEmbedModelTag(t *testing.T) {
	cfg := config.Load()
	if cfg.Sqlite3 == "" || cfg.SqliteVecExt == "" {
		t.Skip("sqlite3 CLI or vec0 extension not configured")
	}
	dbPath := filepath.Join(t.TempDir(), "tag.db")
	db, err := Open(cfg, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.EnsureSchema(); err != nil {
		t.Skipf("schema/vec0 unavailable: %v", err)
	}

	// Fresh DB: no recorded model.
	if got, err := db.GetEmbedModel(); err != nil || got != "" {
		t.Fatalf("GetEmbedModel(fresh) = %q (err %v), want \"\"", got, err)
	}
	// Record, read back.
	if err := db.SetEmbedModel("qwen3-4b"); err != nil {
		t.Fatalf("SetEmbedModel: %v", err)
	}
	if got, err := db.GetEmbedModel(); err != nil || got != "qwen3-4b" {
		t.Fatalf("GetEmbedModel = %q (err %v), want qwen3-4b", got, err)
	}
	// Idempotent overwrite (same key replaces, not appends).
	if err := db.SetEmbedModel("qwen3-4b"); err != nil {
		t.Fatalf("SetEmbedModel(2): %v", err)
	}
	n, err := db.scalarInt("SELECT COUNT(*) AS n FROM embed_meta;")
	if err != nil || n != 1 {
		t.Fatalf("embed_meta row count = %d (err %v), want 1", n, err)
	}
	// Re-tag a different model (the caller is responsible for refusing a mix; the
	// store itself simply overwrites the single key).
	if err := db.SetEmbedModel("qwen3-0.6b"); err != nil {
		t.Fatalf("SetEmbedModel(re-tag): %v", err)
	}
	if got, _ := db.GetEmbedModel(); got != "qwen3-0.6b" {
		t.Fatalf("GetEmbedModel(re-tag) = %q, want qwen3-0.6b", got)
	}
}

// TestEmbedModelMissingTableDegrades proves GetEmbedModel reads "" (not an error)
// when embed_meta is absent — the shape a DB created before this table would have.
func TestEmbedModelMissingTableDegrades(t *testing.T) {
	cfg := config.Load()
	if cfg.Sqlite3 == "" || cfg.SqliteVecExt == "" {
		t.Skip("sqlite3 CLI or vec0 extension not configured")
	}
	dbPath := filepath.Join(t.TempDir(), "notag.db")
	db, err := Open(cfg, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.EnsureSchema(); err != nil {
		t.Skipf("schema/vec0 unavailable: %v", err)
	}
	if err := db.Exec("DROP TABLE IF EXISTS embed_meta;"); err != nil {
		t.Fatalf("drop embed_meta: %v", err)
	}
	if got, err := db.GetEmbedModel(); err != nil || got != "" {
		t.Errorf("GetEmbedModel(no table) = %q (err %v), want \"\"/nil", got, err)
	}
}

// TestIdentificationsRoundTrip exercises the identifications helpers against the
// real sqlite3 CLI: EnsureSchema (additive new table), UpsertIdentification
// (incl. idempotency on deterministic id), ListIdentifications, CountIdentifications,
// DistinctSourceFiles (union with media), and SetIdentificationVerified
// (the propagation write). Skips gracefully without the CLI/extension.
func TestIdentificationsRoundTrip(t *testing.T) {
	cfg := config.Load()
	if cfg.Sqlite3 == "" || cfg.SqliteVecExt == "" {
		t.Skip("sqlite3 CLI or vec0 extension not configured")
	}
	dbPath := filepath.Join(t.TempDir(), "ident.db")
	db, err := Open(cfg, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.EnsureSchema(); err != nil {
		t.Skipf("schema/vec0 unavailable: %v", err)
	}

	rows := []Identification{
		{ID: "v1:voice:S0", SourceFile: "a.mp4", EntityName: "Defendant", Modality: "voice", Confidence: 0.92, SpeakerID: "SPEAKER_00", VerifiedBy: "analyst"},
		{ID: "v1:voice:S1", SourceFile: "a.mp4", EntityName: "Defendant", Modality: "voice", Confidence: 0.60, SpeakerID: "SPEAKER_01"}, // unconfirmed
		{ID: "v2:voice:S0", SourceFile: "b.mp4", EntityName: "The Wife", Modality: "voice", Confidence: 0.70, SpeakerID: "SPEAKER_00"},
	}
	for _, r := range rows {
		if err := db.UpsertIdentification(r); err != nil {
			t.Fatalf("UpsertIdentification: %v", err)
		}
	}
	// Re-insert identical rows: deterministic id means count stays at 3.
	for _, r := range rows {
		if err := db.UpsertIdentification(r); err != nil {
			t.Fatalf("UpsertIdentification(2): %v", err)
		}
	}
	if n, err := db.CountIdentifications(); err != nil || n != 3 {
		t.Fatalf("CountIdentifications = %d (err %v), want 3", n, err)
	}

	got, err := db.ListIdentifications()
	if err != nil {
		t.Fatalf("ListIdentifications: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListIdentifications returned %d, want 3", len(got))
	}
	// Confirmed/unconfirmed must round-trip (NULL verified_by -> empty -> !Confirmed).
	var confirmed, unconfirmed int
	for _, r := range got {
		if r.Confirmed() {
			confirmed++
		} else {
			unconfirmed++
		}
	}
	if confirmed != 1 || unconfirmed != 2 {
		t.Errorf("confirmed/unconfirmed = %d/%d, want 1/2", confirmed, unconfirmed)
	}

	// DistinctSourceFiles unions identifications with media; register a 3rd file
	// via media to prove the union denominator.
	if err := db.UpsertMedia("c.mp4", "", 0, 0); err != nil {
		t.Fatalf("UpsertMedia: %v", err)
	}
	files, err := db.DistinctSourceFiles()
	if err != nil {
		t.Fatalf("DistinctSourceFiles: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("DistinctSourceFiles = %v, want 3 (a,b,c)", files)
	}

	// Propagation write: confirm the unconfirmed row, then verify it round-trips.
	if err := db.SetIdentificationVerified("v1:voice:S1", "Defendant", "becky-consolidate"); err != nil {
		t.Fatalf("SetIdentificationVerified: %v", err)
	}
	after, err := db.ListIdentifications()
	if err != nil {
		t.Fatalf("ListIdentifications(after): %v", err)
	}
	var found bool
	for _, r := range after {
		if r.ID == "v1:voice:S1" {
			found = true
			if r.VerifiedBy != "becky-consolidate" || r.EntityName != "Defendant" {
				t.Errorf("propagated row = %+v, want verified_by becky-consolidate / Defendant", r)
			}
		}
	}
	if !found {
		t.Fatal("propagated row v1:voice:S1 not found after update")
	}
}

// TestNeighborSegments exercises the --expand context query against the real CLI:
// the previous and next segment (same source) are returned, ordered by time, and
// the hit itself is excluded. Skips gracefully without the CLI/extension.
func TestNeighborSegments(t *testing.T) {
	cfg := config.Load()
	if cfg.Sqlite3 == "" || cfg.SqliteVecExt == "" {
		t.Skip("sqlite3 CLI or vec0 extension not configured")
	}
	dbPath := filepath.Join(t.TempDir(), "ctx.db")
	db, err := Open(cfg, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.EnsureSchema(); err != nil {
		t.Skipf("schema/vec0 unavailable: %v", err)
	}
	for i := 0; i < 3; i++ {
		row := Segment{
			SegmentID:    "abc:" + strconv.Itoa(i),
			SourceFile:   "v.mp4",
			SourceSHA256: "abc",
			StartTime:    float64(i),
			EndTime:      float64(i) + 0.5,
			Text:         "seg" + strconv.Itoa(i),
			NeedsReview:  1,
		}
		if err := db.UpsertSegment(row); err != nil {
			t.Fatalf("UpsertSegment: %v", err)
		}
	}

	// Context around the middle segment (index 1) => indices 0 and 2, in order,
	// excluding the hit itself.
	got, err := db.NeighborSegments("abc", "abc:1", 1)
	if err != nil {
		t.Fatalf("NeighborSegments: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d neighbors, want 2", len(got))
	}
	if got[0].Text != "seg0" || got[1].Text != "seg2" {
		t.Errorf("neighbor texts = %q,%q want seg0,seg2", got[0].Text, got[1].Text)
	}

	// An unparseable id yields no context but no error.
	none, err := db.NeighborSegments("abc", "noindex", 1)
	if err != nil || len(none) != 0 {
		t.Errorf("NeighborSegments(unparseable) = %v,%v want nil,nil", none, err)
	}
}

// TestMediaMetaAndLiveChatRoundTrip exercises the additive yt-dlp metadata
// tables through the real sqlite3 CLI: UpsertMediaMeta (incl. idempotency) and
// InsertLiveChat, plus the count helpers. Skips if the CLI/extension are absent.
func TestMediaMetaAndLiveChatRoundTrip(t *testing.T) {
	cfg := config.Load()
	if cfg.Sqlite3 == "" || cfg.SqliteVecExt == "" {
		t.Skip("sqlite3 CLI or vec0 extension not configured")
	}
	dbPath := filepath.Join(t.TempDir(), "meta.db")
	db, err := Open(cfg, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.EnsureSchema(); err != nil {
		t.Skipf("schema unavailable: %v", err)
	}

	m := MediaMeta{
		SourceFile: "v.mp4", VideoID: "abc123", Title: "It's a Test",
		Uploader: "Chan", UploaderID: "@chan", Channel: "Chan",
		ChannelID: "UC0", ChannelURL: "http://c", UploadISO: "2026-01-06T23:26:50Z",
		UploadUnix: 1767742010, Duration: 148, WebpageURL: "http://w",
		ChaptersJSON: "[]", TagsJSON: "[]",
	}
	if err := db.UpsertMediaMeta(m); err != nil {
		t.Fatalf("UpsertMediaMeta: %v", err)
	}
	if err := db.UpsertMediaMeta(m); err != nil { // idempotent
		t.Fatalf("UpsertMediaMeta(2): %v", err)
	}
	n, err := db.CountMediaMeta()
	if err != nil || n != 1 {
		t.Fatalf("CountMediaMeta = %d,%v want 1,nil", n, err)
	}

	chats := []ChatLine{
		{ChatID: "v:0", SourceFile: "v.mp4", Author: "@a", Text: "yay", OffsetSec: 3.6},
		{ChatID: "v:1", SourceFile: "v.mp4", Author: "@b", Text: "it's lit", OffsetSec: 14.7},
	}
	for _, c := range chats {
		if err := db.InsertLiveChat(c); err != nil {
			t.Fatalf("InsertLiveChat: %v", err)
		}
	}
	for _, c := range chats { // idempotent re-insert
		if err := db.InsertLiveChat(c); err != nil {
			t.Fatalf("InsertLiveChat(2): %v", err)
		}
	}
	cn, err := db.CountLiveChat()
	if err != nil || cn != 2 {
		t.Fatalf("CountLiveChat = %d,%v want 2,nil", cn, err)
	}
}
