// Package beckydb is the shared SQLite + sqlite-vec layer for the becky toolset.
//
// It is deliberately driver-free: instead of a cgo SQLite driver (which would
// need a C toolchain to load the sqlite-vec extension), every statement is run
// through the sqlite3.exe CLI with the vec0 loadable extension auto-loaded. The
// vec0 extension accepts vectors as JSON-array text (e.g. '[0.1,0.2,...]') for
// both INSERT and MATCH, so no binary blob handling is needed and the Go binary
// stays dependency-free (no new go.mod deps, no cgo).
//
// becky-embed writes the forensic DB through this package; becky-search and
// becky-consolidate read it through the SAME package, so the schema and the KNN
// query live in exactly one place. See schema.sql and README.md alongside this
// file for the canonical schema and query documentation.
package beckydb

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"becky-go/internal/config"
)

// VectorDim is the embedding dimensionality for Qwen3-Embedding-0.6B. The vec0
// virtual table is declared float[VectorDim]; every inserted/queried vector must
// have exactly this many components.
const VectorDim = 1024

// DB is a handle to a forensic SQLite database accessed via the sqlite3 CLI.
// It carries the paths needed to exec the CLI with vec0 loaded. It holds no
// open file handle: each Exec/Query spawns sqlite3.exe, which is plenty fast for
// the becky batch workloads and avoids any long-lived locking.
type DB struct {
	path    string // path to the .db file
	sqlite3 string // sqlite3.exe
	vecExt  string // vec0 loadable extension (path WITHOUT shell quoting)
}

// Open returns a DB handle for dbPath using the CLI + extension paths from cfg.
// It does not create the file (sqlite3 creates it lazily on first write); call
// EnsureSchema to create the canonical tables.
func Open(cfg config.Config, dbPath string) (*DB, error) {
	if cfg.Sqlite3 == "" {
		return nil, fmt.Errorf("config.Sqlite3 is empty (need path to sqlite3.exe)")
	}
	if cfg.SqliteVecExt == "" {
		return nil, fmt.Errorf("config.SqliteVecExt is empty (need path to vec0 extension)")
	}
	return &DB{
		path:    dbPath,
		sqlite3: cfg.Sqlite3,
		vecExt:  loadArg(cfg.SqliteVecExt),
	}, nil
}

// loadArg normalizes an extension path for sqlite3's ".load" dot-command. We use
// forward slashes (sqlite accepts them on Windows) and strip a trailing .dll so
// the same value works whether the configured path includes the suffix or not.
func loadArg(ext string) string {
	ext = strings.ReplaceAll(ext, "\\", "/")
	ext = strings.TrimSuffix(ext, ".dll")
	ext = strings.TrimSuffix(ext, ".so")
	ext = strings.TrimSuffix(ext, ".dylib")
	return ext
}

// run execs sqlite3.exe with vec0 auto-loaded and the given SQL piped to stdin.
// modeJSON prepends ".mode json" so queries emit a JSON array of row objects.
// It returns trimmed stdout; on failure it wraps stderr for context.
func (db *DB) run(sql string, modeJSON bool) (string, error) {
	// -cmd runs the dot-command before the main SQL; passing the path as its own
	// argv element (not inside a SQL string) means no backslash escaping issues.
	args := []string{"-cmd", ".load " + db.vecExt}
	if modeJSON {
		args = append(args, "-cmd", ".mode json")
	}
	args = append(args, db.path)

	cmd := exec.Command(db.sqlite3, args...)
	cmd.Stdin = strings.NewReader(sql)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("sqlite3 failed: %v\nSQL: %s\nstderr: %s",
			err, truncate(sql, 400), strings.TrimSpace(stderr.String()))
	}
	// sqlite3 reports some errors on stderr without a non-zero exit; surface them.
	if e := strings.TrimSpace(stderr.String()); e != "" {
		return "", fmt.Errorf("sqlite3 error: %s\nSQL: %s", e, truncate(sql, 400))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Exec runs a non-query statement (or batch) and discards stdout.
func (db *DB) Exec(sql string) error {
	_, err := db.run(sql, false)
	return err
}

// queryJSON runs a SELECT and decodes the ".mode json" output (a JSON array of
// objects) into dest. Empty output decodes to an empty slice.
func (db *DB) queryJSON(sql string, dest any) error {
	out, err := db.run(sql, true)
	if err != nil {
		return err
	}
	if out == "" {
		return nil // no rows: leave dest at its zero value
	}
	if err := json.Unmarshal([]byte(out), dest); err != nil {
		return fmt.Errorf("decode sqlite json: %w\noutput: %s", err, truncate(out, 400))
	}
	return nil
}

// EnsureSchema creates the canonical becky forensic schema if it does not exist.
// It is idempotent (CREATE TABLE IF NOT EXISTS / CREATE VIRTUAL TABLE IF NOT
// EXISTS) and safe to call on every run. The schema string here is the single
// source of truth shared with schema.sql.
//
// The FTS5 keyword index (schema_fts.sql) is applied in a SEPARATE invocation
// after the core schema: a sqlite3 build compiled without FTS5 errors on its
// CREATE, and isolating it means that error can't abort the core tables. An
// FTS5-unavailable failure is swallowed (callers learn whether keyword search is
// live via FTS5Available); any OTHER FTS error is surfaced.
func (db *DB) EnsureSchema() error {
	if err := db.Exec(Schema); err != nil {
		return err
	}
	if err := db.Exec(SchemaFTS); err != nil {
		// A sqlite3 without FTS5 (or with a missing tokenizer) is a graceful
		// degrade, not a hard failure — embed/search fall back to vector-only.
		if isFTS5Unavailable(err) {
			return nil
		}
		return fmt.Errorf("ensure FTS5 schema: %w", err)
	}
	return nil
}

// FTS5Available reports whether this sqlite3 build supports FTS5, by attempting a
// throwaway in-statement FTS5 table create + drop. It is cheap (one sqlite3
// spawn) and leaves no trace on the forensic DB. becky-search calls it to decide
// whether to run the keyword half of hybrid retrieval or degrade to vector-only.
func (db *DB) FTS5Available() bool {
	err := db.Exec("CREATE VIRTUAL TABLE IF NOT EXISTS becky_fts_probe USING fts5(x);\n" +
		"DROP TABLE IF EXISTS becky_fts_probe;")
	return err == nil
}

// isFTS5Unavailable matches the sqlite3 errors that mean "this build can't do
// FTS5" (so EnsureSchema can degrade gracefully) without masking real SQL faults.
func isFTS5Unavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	// "no such module: fts5" (FTS5 not compiled in) or a tokenizer the build
	// lacks ("unknown tokenizer: porter" / "no such tokenizer").
	return strings.Contains(msg, "no such module: fts5") ||
		strings.Contains(msg, "unknown tokenizer") ||
		strings.Contains(msg, "no such tokenizer")
}

// InsertFTS stores (or refreshes) a segment's text in the FTS5 keyword index.
// FTS5 has no upsert, so — mirroring InsertVector — we DELETE any existing row
// for segmentID then INSERT, keeping re-runs idempotent. Callers that want the
// keyword index should gate writes on FTS5Available so a no-FTS5 build still
// embeds (vectors only) instead of erroring on the missing table.
func (db *DB) InsertFTS(segmentID, text string) error {
	sql := fmt.Sprintf(
		"DELETE FROM segments_fts WHERE segment_id = %s;\n"+
			"INSERT INTO segments_fts(segment_id, text) VALUES (%s, %s);",
		q(segmentID), q(segmentID), q(text),
	)
	return db.Exec(sql)
}

// CountFTS returns the number of rows in the FTS5 keyword index. Returns 0 with
// no error when the table is absent (no-FTS5 build), so embed's summary is robust.
func (db *DB) CountFTS() (int, error) {
	n, err := db.scalarInt("SELECT COUNT(*) AS n FROM segments_fts;")
	if err != nil && isMissingFTSTable(err) {
		return 0, nil
	}
	return n, err
}

// isMissingFTSTable matches the "no such table: segments_fts" error so reads
// (CountFTS / KeywordSearch) degrade to empty on a no-FTS5 DB instead of failing
// the whole tool.
func isMissingFTSTable(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "no such table: segments_fts")
}

// Segment is one transcript segment row. The field set is the union required by
// becky-embed (writer) and becky-search / becky-consolidate (readers):
//   - source provenance: SourceFile, SourceSHA256
//   - timing + content: StartTime, EndTime, Text
//   - speaker enrichment (filled later by identify/consolidate): SpeakerID,
//     SpeakerName, SpeakerConfidence
//   - review state: NeedsReview, VerifiedBy
type Segment struct {
	SegmentID         string  `json:"segment_id"`
	SourceFile        string  `json:"source_file"`
	SourceSHA256      string  `json:"source_sha256"`
	StartTime         float64 `json:"start_time"`
	EndTime           float64 `json:"end_time"`
	Text              string  `json:"text"`
	SpeakerID         string  `json:"speaker_id"`
	SpeakerName       string  `json:"speaker_name"`
	SpeakerConfidence float64 `json:"speaker_confidence"`
	NeedsReview       int     `json:"needs_review"`
	VerifiedBy        string  `json:"verified_by"`
	CreatedAt         string  `json:"created_at"`
}

// UpsertSegment inserts or replaces a segment row keyed by SegmentID. CreatedAt
// defaults to now (RFC3339) when empty. INSERT OR REPLACE makes re-runs of
// becky-embed idempotent for the metadata table.
func (db *DB) UpsertSegment(s Segment) error {
	if s.CreatedAt == "" {
		s.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	sql := fmt.Sprintf(
		"INSERT OR REPLACE INTO segments "+
			"(segment_id, source_file, source_sha256, start_time, end_time, text, "+
			"speaker_id, speaker_name, speaker_confidence, needs_review, verified_by, created_at) "+
			"VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %d, %s, %s);",
		q(s.SegmentID), q(s.SourceFile), q(s.SourceSHA256),
		num(s.StartTime), num(s.EndTime), q(s.Text),
		q(s.SpeakerID), nullableStr(s.SpeakerName), num(s.SpeakerConfidence),
		s.NeedsReview, nullableStr(s.VerifiedBy), q(s.CreatedAt),
	)
	return db.Exec(sql)
}

// InsertVector stores a segment's embedding in the vec0 table. vec0 does not
// support INSERT OR REPLACE on a TEXT primary key, so we DELETE then INSERT to
// stay idempotent across re-runs. vecJSON must be a JSON array of VectorDim
// floats, e.g. "[0.1,0.2,...]".
func (db *DB) InsertVector(segmentID, vecJSON string) error {
	sql := fmt.Sprintf(
		"DELETE FROM segments_vec WHERE segment_id = %s;\n"+
			"INSERT INTO segments_vec(segment_id, embedding) VALUES (%s, %s);",
		q(segmentID), q(segmentID), q(vecJSON),
	)
	return db.Exec(sql)
}

// UpsertMedia records one source media file (provenance + probe facts). Optional
// table; becky-embed populates it when a --source video is supplied.
func (db *DB) UpsertMedia(sourceFile, sha256 string, duration, fps float64) error {
	sql := fmt.Sprintf(
		"INSERT OR REPLACE INTO media (source_file, source_sha256, duration, fps, ingested_at) "+
			"VALUES (%s, %s, %s, %s, %s);",
		q(sourceFile), q(sha256), num(duration), num(fps),
		q(time.Now().UTC().Format(time.RFC3339)),
	)
	return db.Exec(sql)
}

// MediaMeta is the yt-dlp .info.json provenance for one source file: the upload
// timeline anchor + identity/title/description, stored by the becky-pipeline
// metadata step so downloaded video carries when/who/what without re-deriving it.
type MediaMeta struct {
	SourceFile   string  `json:"source_file"`
	VideoID      string  `json:"video_id"`
	Title        string  `json:"title"`
	Description  string  `json:"description"`
	Uploader     string  `json:"uploader"`
	UploaderID   string  `json:"uploader_id"`
	Channel      string  `json:"channel"`
	ChannelID    string  `json:"channel_id"`
	ChannelURL   string  `json:"channel_url"`
	UploadISO    string  `json:"upload_iso"` // RFC3339 UTC
	UploadUnix   int64   `json:"upload_unix"`
	Duration     float64 `json:"duration"`
	WebpageURL   string  `json:"webpage_url"`
	ChaptersJSON string  `json:"chapters_json"` // chapters[] serialized
	TagsJSON     string  `json:"tags_json"`     // tags[] serialized
}

// UpsertMediaMeta inserts or replaces the metadata row for a source file. Keyed
// by SourceFile so re-ingesting the same video's .info.json is idempotent.
func (db *DB) UpsertMediaMeta(m MediaMeta) error {
	sql := fmt.Sprintf(
		"INSERT OR REPLACE INTO media_meta "+
			"(source_file, video_id, title, description, uploader, uploader_id, "+
			"channel, channel_id, channel_url, upload_iso, upload_unix, duration, "+
			"webpage_url, chapters_json, tags_json, ingested_at) "+
			"VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %d, %s, %s, %s, %s, %s);",
		q(m.SourceFile), q(m.VideoID), q(m.Title), q(m.Description),
		q(m.Uploader), q(m.UploaderID), q(m.Channel), q(m.ChannelID), q(m.ChannelURL),
		nullableStr(m.UploadISO), m.UploadUnix, num(m.Duration), q(m.WebpageURL),
		q(m.ChaptersJSON), q(m.TagsJSON), q(time.Now().UTC().Format(time.RFC3339)),
	)
	return db.Exec(sql)
}

// CountMediaMeta returns the number of rows in the media_meta table.
func (db *DB) CountMediaMeta() (int, error) {
	return db.scalarInt("SELECT COUNT(*) AS n FROM media_meta;")
}

// ChatLine is one timestamped live-chat message stored for a source video.
type ChatLine struct {
	ChatID     string  `json:"chat_id"`
	SourceFile string  `json:"source_file"`
	Author     string  `json:"author"`
	Text       string  `json:"text"`
	OffsetSec  float64 `json:"offset_sec"`
}

// InsertLiveChat inserts or replaces one live-chat row. ChatID is deterministic
// (caller passes sha12(source)+":"+ordinal) so re-ingestion is idempotent.
func (db *DB) InsertLiveChat(c ChatLine) error {
	sql := fmt.Sprintf(
		"INSERT OR REPLACE INTO live_chat "+
			"(chat_id, source_file, author, text, offset_sec, created_at) "+
			"VALUES (%s, %s, %s, %s, %s, %s);",
		q(c.ChatID), q(c.SourceFile), q(c.Author), q(c.Text), num(c.OffsetSec),
		q(time.Now().UTC().Format(time.RFC3339)),
	)
	return db.Exec(sql)
}

// CountLiveChat returns the number of rows in the live_chat table.
func (db *DB) CountLiveChat() (int, error) {
	return db.scalarInt("SELECT COUNT(*) AS n FROM live_chat;")
}

// Identification is one named recognition of an entity in one source video,
// ingested from becky-identify output. It is the unit becky-consolidate reports
// coverage over and propagates confirmed names across.
//
// VerifiedBy distinguishes a confirmed identification (an analyst/human signed
// off, VerifiedBy set) from an unconfirmed model guess (VerifiedBy empty/NULL).
// Propagation only starts from confirmed rows and only writes onto unconfirmed
// rows whose Confidence clears the threshold.
type Identification struct {
	ID           string  `json:"id"`
	SourceFile   string  `json:"source_file"`
	SourceSHA256 string  `json:"source_sha256"`
	EntityName   string  `json:"entity_name"`
	Modality     string  `json:"modality"` // voice | face | location
	Confidence   float64 `json:"confidence"`
	SpeakerID    string  `json:"speaker_id"`
	VerifiedBy   string  `json:"verified_by"`
	CreatedAt    string  `json:"created_at"`
}

// Confirmed reports whether this identification has been verified by a human or
// analyst (VerifiedBy set). Only confirmed rows seed name propagation.
func (i Identification) Confirmed() bool {
	return strings.TrimSpace(i.VerifiedBy) != ""
}

// UpsertIdentification inserts or replaces an identification row keyed by ID.
// CreatedAt defaults to now (RFC3339) when empty. INSERT OR REPLACE makes
// re-ingesting the same becky-identify run idempotent (deterministic IDs).
func (db *DB) UpsertIdentification(i Identification) error {
	if i.CreatedAt == "" {
		i.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	sql := fmt.Sprintf(
		"INSERT OR REPLACE INTO identifications "+
			"(id, source_file, source_sha256, entity_name, modality, confidence, "+
			"speaker_id, verified_by, created_at) "+
			"VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s);",
		q(i.ID), q(i.SourceFile), q(i.SourceSHA256), nullableStr(i.EntityName),
		q(i.Modality), num(i.Confidence), q(i.SpeakerID),
		nullableStr(i.VerifiedBy), q(i.CreatedAt),
	)
	return db.Exec(sql)
}

// ListIdentifications returns every identification row, ordered for stable,
// deterministic output (entity, then source, then modality, then id). NULL
// entity_name / verified_by decode to empty strings via COALESCE.
func (db *DB) ListIdentifications() ([]Identification, error) {
	const sql = "SELECT id, source_file, source_sha256, " +
		"COALESCE(entity_name,'') AS entity_name, modality, confidence, " +
		"COALESCE(speaker_id,'') AS speaker_id, COALESCE(verified_by,'') AS verified_by, " +
		"COALESCE(created_at,'') AS created_at " +
		"FROM identifications " +
		"ORDER BY entity_name, source_file, modality, id;"
	var rows []Identification
	if err := db.queryJSON(sql, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// SetIdentificationVerified propagates a confirmed name onto one (previously
// unconfirmed) identification row: it sets entity_name and verified_by by ID.
// This is the single write the propagation pass performs.
func (db *DB) SetIdentificationVerified(id, entityName, verifiedBy string) error {
	sql := fmt.Sprintf(
		"UPDATE identifications SET entity_name = %s, verified_by = %s WHERE id = %s;",
		q(entityName), q(verifiedBy), q(id),
	)
	return db.Exec(sql)
}

// CountIdentifications returns the number of rows in the identifications table.
func (db *DB) CountIdentifications() (int, error) {
	return db.scalarInt("SELECT COUNT(*) AS n FROM identifications;")
}

// DistinctSourceFiles returns the unique source_file values that appear in
// either the media table or the identifications table — the denominator for
// coverage math ("recognized in N/total videos"). The union means coverage
// works whether the corpus was registered via becky-embed (--source) or only
// implied by ingested identifications.
func (db *DB) DistinctSourceFiles() ([]string, error) {
	const sql = "SELECT source_file FROM (" +
		"SELECT source_file FROM media WHERE source_file IS NOT NULL AND source_file <> '' " +
		"UNION " +
		"SELECT source_file FROM identifications WHERE source_file IS NOT NULL AND source_file <> '') " +
		"GROUP BY source_file ORDER BY source_file;"
	var rows []struct {
		SourceFile string `json:"source_file"`
	}
	if err := db.queryJSON(sql, &rows); err != nil {
		return nil, err
	}
	files := make([]string, 0, len(rows))
	for _, r := range rows {
		files = append(files, r.SourceFile)
	}
	return files, nil
}

// EmbedModelKey is the embed_meta key under which the indexed embedding model
// name is stored. becky-embed writes it; becky-search reads it to refuse a
// cross-vector-space query (different model => incomparable cosine).
const EmbedModelKey = "embed_model"

// GetEmbedModel returns the embedding model name this DB was indexed with (the
// embed_meta 'embed_model' value), or "" when the DB has never been indexed
// (fresh DB / no vectors yet). A missing embed_meta table degrades to "" with no
// error so a pre-existing DB created before this column still reads cleanly.
func (db *DB) GetEmbedModel() (string, error) {
	var rows []struct {
		Value string `json:"value"`
	}
	sql := fmt.Sprintf("SELECT value FROM embed_meta WHERE key = %s;", q(EmbedModelKey))
	if err := db.queryJSON(sql, &rows); err != nil {
		if isMissingEmbedMetaTable(err) {
			return "", nil
		}
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	return rows[0].Value, nil
}

// SetEmbedModel records the embedding model name this DB is indexed with. It is
// idempotent (INSERT OR REPLACE on the key). becky-embed calls it once per run
// so the model tag always reflects the vectors actually stored.
func (db *DB) SetEmbedModel(model string) error {
	sql := fmt.Sprintf(
		"INSERT OR REPLACE INTO embed_meta(key, value) VALUES (%s, %s);",
		q(EmbedModelKey), q(model),
	)
	return db.Exec(sql)
}

// isMissingEmbedMetaTable matches the "no such table: embed_meta" error so a DB
// that predates the embed_meta table reads as "no recorded model" instead of
// failing the whole tool.
func isMissingEmbedMetaTable(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "no such table: embed_meta")
}

// CountSegments returns the number of rows in the segments table.
func (db *DB) CountSegments() (int, error) {
	return db.scalarInt("SELECT COUNT(*) AS n FROM segments;")
}

// CountVectors returns the number of rows in the segments_vec table.
func (db *DB) CountVectors() (int, error) {
	return db.scalarInt("SELECT COUNT(*) AS n FROM segments_vec;")
}

func (db *DB) scalarInt(sql string) (int, error) {
	var rows []struct {
		N int `json:"n"`
	}
	if err := db.queryJSON(sql, &rows); err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return rows[0].N, nil
}

// Neighbor is one retrieval result: a segment plus its score against the query.
// For vector results, Similarity = 1 - cosine_distance (vec0's segments_vec uses
// distance_metric=cosine and Qwen3 vectors are L2-normalized, so similarity is in
// [0,1]). For keyword (BM25) results, Distance/Similarity are 0 and BM25 carries
// the raw bm25() score (more negative = more relevant). Rank is the 1-based
// position within the list this neighbor came from (the vector ranking or the
// keyword ranking) — the input Reciprocal Rank Fusion in becky-search needs.
type Neighbor struct {
	Segment
	Distance   float64 `json:"distance"`
	Similarity float64 `json:"similarity"`
	BM25       float64 `json:"bm25,omitempty"` // keyword results only (raw bm25() score)
	Rank       int     `json:"rank"`           // 1-based rank within its own list
}

// KNN runs a k-nearest-neighbor search for queryVecJSON (a JSON array of
// VectorDim floats), joining the vec0 results back to the segments table so the
// caller gets full rows. Results with similarity < minSim are dropped. Rows are
// ordered by ascending distance (closest / most similar first).
//
// This is the canonical search query that becky-search uses verbatim.
func (db *DB) KNN(queryVecJSON string, k int, minSim float64) ([]Neighbor, error) {
	if k <= 0 {
		k = 10
	}
	sql := fmt.Sprintf(
		"SELECT s.segment_id, s.source_file, s.source_sha256, s.start_time, s.end_time, "+
			"s.text, s.speaker_id, s.speaker_name, s.speaker_confidence, s.needs_review, "+
			"s.verified_by, s.created_at, v.distance, (1.0 - v.distance) AS similarity "+
			"FROM segments_vec v JOIN segments s ON s.segment_id = v.segment_id "+
			"WHERE v.embedding MATCH %s AND k = %d "+
			"AND (1.0 - v.distance) >= %s "+
			"ORDER BY v.distance;",
		q(queryVecJSON), k, num(minSim),
	)
	var rows []Neighbor
	if err := db.queryJSON(sql, &rows); err != nil {
		return nil, err
	}
	// Stamp the 1-based rank within this (vector) list, so RRF fusion in
	// becky-search can fuse it against the keyword list by position.
	for i := range rows {
		rows[i].Rank = i + 1
	}
	return rows, nil
}

// KeywordSearch runs a BM25 full-text search over segments_fts for the keyword
// half of hybrid retrieval, joining back to segments so it returns the SAME
// Neighbor shape as KNN. Results are ordered by bm25(segments_fts) ascending
// (SQLite's bm25 returns more-negative for more-relevant), capped at k, and each
// row carries its 1-based Rank for RRF fusion. The raw bm25 score is exposed in
// BM25; Distance/Similarity stay 0 (cosine is meaningless for a keyword hit).
//
// query is arbitrary user text: it is sanitized into a safe FTS5 MATCH expression
// (each alphanumeric token quoted as a phrase, OR-joined) so special characters
// ("a-b OR c", quotes, etc.) can't error or be interpreted as FTS5 operators. A
// query with no usable tokens, or a DB without FTS5, yields an empty slice (not
// an error) so becky-search degrades to vector-only.
func (db *DB) KeywordSearch(query string, k int) ([]Neighbor, error) {
	if k <= 0 {
		k = 10
	}
	match := sanitizeFTSQuery(query)
	if match == "" {
		return nil, nil // nothing searchable (e.g. punctuation-only query)
	}
	sql := fmt.Sprintf(
		"SELECT s.segment_id, s.source_file, s.source_sha256, s.start_time, s.end_time, "+
			"s.text, s.speaker_id, s.speaker_name, s.speaker_confidence, s.needs_review, "+
			"s.verified_by, s.created_at, bm25(segments_fts) AS bm25 "+
			"FROM segments_fts f JOIN segments s ON s.segment_id = f.segment_id "+
			"WHERE segments_fts MATCH %s "+
			"ORDER BY bm25(segments_fts) LIMIT %d;",
		q(match), k,
	)
	var rows []Neighbor
	if err := db.queryJSON(sql, &rows); err != nil {
		// No-FTS5 DB (table never created) is a graceful degrade, not an error.
		if isMissingFTSTable(err) {
			return nil, nil
		}
		return nil, err
	}
	for i := range rows {
		rows[i].Rank = i + 1
	}
	return rows, nil
}

// sanitizeFTSQuery turns arbitrary user text into a safe FTS5 MATCH expression.
// FTS5's query syntax treats characters like " - * : ^ ( ) and the bare words
// AND/OR/NOT/NEAR as operators, so raw user text can error (e.g. an unbalanced
// quote) or behave surprisingly. We extract alphanumeric/underscore tokens,
// double-quote each as a literal phrase (which also neutralizes operator
// keywords), and OR-join them — so the result matches any term, exactly what the
// keyword half of hybrid retrieval wants. Returns "" when no token remains.
func sanitizeFTSQuery(query string) string {
	tokens := strings.FieldsFunc(query, func(r rune) bool {
		return !(r == '_' ||
			(r >= '0' && r <= '9') ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			r > 127) // keep non-ASCII letters; unicode61 tokenizer folds them
	})
	quoted := make([]string, 0, len(tokens))
	for _, t := range tokens {
		// A double-quoted FTS5 phrase escapes embedded quotes by doubling them;
		// our tokens never contain quotes (split above strips them), but double
		// defensively in case the token-class set ever widens.
		quoted = append(quoted, `"`+strings.ReplaceAll(t, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, " OR ")
}

// NeighborSegments returns the segments immediately surrounding a hit (same
// source), for best-effort context (becky-search --expand). segmentID is the
// matched segment's key, formatted "<sha12>:<index>"; radius is how many
// segments to include on each side (e.g. radius 1 -> the previous and next
// segment, excluding the hit itself). Results are ordered by start_time.
//
// It is best-effort: a segmentID that does not parse, or a DB with no neighbors,
// yields an empty slice (not an error). This keeps the read path here, so
// becky-search never builds its own SQL.
func (db *DB) NeighborSegments(sourceSHA256, segmentID string, radius int) ([]Segment, error) {
	if radius <= 0 {
		radius = 1
	}
	idx, ok := segmentIndex(segmentID)
	if !ok {
		return nil, nil // unparseable id: no context, but not an error
	}
	lo, hi := idx-radius, idx+radius
	// Reconstruct the candidate neighbor ids from the shared "<sha12>:<i>" scheme
	// and exclude the hit itself, so context is the surrounding turns only.
	prefix := segmentID[:strings.LastIndex(segmentID, ":")]
	ids := make([]string, 0, 2*radius)
	for i := lo; i <= hi; i++ {
		if i < 0 || i == idx {
			continue
		}
		ids = append(ids, q(fmt.Sprintf("%s:%d", prefix, i)))
	}
	if len(ids) == 0 {
		return nil, nil
	}
	sql := fmt.Sprintf(
		"SELECT segment_id, source_file, source_sha256, start_time, end_time, text, "+
			"speaker_id, speaker_name, speaker_confidence, needs_review, verified_by, created_at "+
			"FROM segments WHERE source_sha256 = %s AND segment_id IN (%s) "+
			"ORDER BY start_time;",
		q(sourceSHA256), strings.Join(ids, ","),
	)
	var rows []Segment
	if err := db.queryJSON(sql, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// segmentIndex parses the trailing integer index from a "<sha12>:<index>"
// segment id. Returns ok=false when the id has no parseable ":<int>" suffix.
func segmentIndex(segmentID string) (int, bool) {
	pos := strings.LastIndex(segmentID, ":")
	if pos < 0 || pos == len(segmentID)-1 {
		return 0, false
	}
	n, err := strconv.Atoi(segmentID[pos+1:])
	if err != nil {
		return 0, false
	}
	return n, true
}

// --- small SQL literal helpers (no driver = manual escaping) ----------------

// q renders a SQL single-quoted string literal, doubling embedded quotes. This
// is the only place untrusted text reaches SQL, so all string values funnel
// through here to prevent injection / breakage on apostrophes.
func q(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// nullableStr emits SQL NULL for an empty string, else a quoted literal. Used
// for columns that default NULL (speaker_name, verified_by) so downstream tools
// can distinguish "unset" from "empty".
func nullableStr(s string) string {
	if s == "" {
		return "NULL"
	}
	return q(s)
}

// num formats a float without scientific notation for SQL.
func num(f float64) string {
	return fmt.Sprintf("%g", f)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
