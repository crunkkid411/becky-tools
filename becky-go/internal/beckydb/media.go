// media.go — the picture + sound layer of the becky video database (becky-mediasearch, becky-shots).
//
// segments/segments_vec hold what was SAID (Qwen3 text vectors). These two tables hold what was
// SEEN and HEARD: EmbeddingGemma 2 vectors for single frames ("picture"), short runs of frames
// ("action") and stretches of sound ("sound"), all in one 768-dim space with text, so a sentence
// can be compared with a frame directly. becky-shots adds "shot" rows: Gemma 4's written
// description of each shot (in label) plus the vector of that text, so the same search finds
// shots by what was written about them. Same additive, self-contained pattern as cluster.go:
// its own DDL and EnsureMediaSchema, no change to schema.sql or the embed/search paths.
//
// Vectors live in a plain BLOB column (vec_f32) and are scored with vec_distance_cosine in a
// brute-force scan, so one query can be limited to chosen files with an ordinary WHERE (a vec0
// KNN table cannot). ~7,000 rows per hour of video at one frame a second; a scan of a few hundred
// thousand rows takes seconds.
package beckydb

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// MediaVectorDim is EmbeddingGemma 2's output size.
const MediaVectorDim = 768

const mediaSchema = `
-- media_files: one row per file read by becky-mediasearch, written LAST, so a file only counts as
-- read once all its media_items are in. size+mtime+step+model decide whether it is still current.
CREATE TABLE IF NOT EXISTS media_files (
    source_file TEXT PRIMARY KEY, -- absolute path
    size        INTEGER,
    mtime       INTEGER,          -- unix seconds
    step        REAL,             -- seconds between frames (0 = every frame)
    model       TEXT,
    items       INTEGER,
    created_at  TEXT
);
-- media_items: one embedded piece of a file.
CREATE TABLE IF NOT EXISTS media_items (
    source_file TEXT,
    kind        TEXT,             -- picture | action | sound | said (becky-mediasearch) | shot (becky-shots)
    start_time  REAL,
    end_time    REAL,
    label       TEXT,             -- shot: the written description; said: the spoken line; empty otherwise
    embedding   BLOB              -- vec_f32 of MediaVectorDim floats, L2-normalised
);
CREATE INDEX IF NOT EXISTS idx_media_items_source ON media_items(source_file);
`

// mediaKinds are the rows becky-mediasearch owns (ReplaceMedia). "shot" rows belong to
// becky-shots / --transcript (ReplaceKind), so re-reading one never wipes the other.
const mediaKinds = "('picture','action','sound')"

// EnsureMediaSchema creates the media tables if absent. Idempotent.
func (db *DB) EnsureMediaSchema() error {
	if err := db.Exec(mediaSchema); err != nil {
		return fmt.Errorf("ensure media schema: %w", err)
	}
	return nil
}

// MediaFile is a media_files row.
type MediaFile struct {
	SourceFile string  `json:"source_file"`
	Size       int64   `json:"size"`
	Mtime      int64   `json:"mtime"`
	Step       float64 `json:"step"`
	Model      string  `json:"model"`
	Items      int     `json:"items"`
}

// MediaItem is one vector to store.
type MediaItem struct {
	Kind       string
	Start, End float64
	Label      string
	Vec        []float32
}

// MediaHit is one scored media_items row.
type MediaHit struct {
	SourceFile string  `json:"source_file"`
	Kind       string  `json:"kind"`
	Start      float64 `json:"start_time"`
	End        float64 `json:"end_time"`
	Label      string  `json:"label"`
	Similarity float64 `json:"similarity"`
}

// GetMediaFile returns the read record for a file (ok=false if it was never read).
func (db *DB) GetMediaFile(path string) (MediaFile, bool, error) {
	var rows []MediaFile
	err := db.queryJSON("SELECT source_file, size, mtime, step, model, items FROM media_files WHERE source_file = "+q(path)+";", &rows) // nosec: every value is quoted by q() (sqlite3 CLI, TestMediaRoundTrip)
	if err != nil || len(rows) == 0 {
		return MediaFile{}, false, err
	}
	return rows[0], true, nil
}

// ListMediaFiles returns every file becky-mediasearch has read.
func (db *DB) ListMediaFiles() ([]MediaFile, error) {
	var rows []MediaFile
	err := db.queryJSON("SELECT source_file, size, mtime, step, model, items FROM media_files ORDER BY source_file;", &rows)
	return rows, err
}

// ReplaceMedia stores a file's picture/action/sound items (dropping its older ones, never its
// shots) and then its media_files row.
func (db *DB) ReplaceMedia(f MediaFile, items []MediaItem) error {
	if err := db.Exec("DELETE FROM media_files WHERE source_file = " + q(f.SourceFile) + ";\n" + // nosec: every value is quoted by q() (sqlite3 CLI, TestMediaRoundTrip)
		"DELETE FROM media_items WHERE source_file = " + q(f.SourceFile) + " AND kind IN " + mediaKinds + ";"); err != nil { // nosec: every value is quoted by q() (sqlite3 CLI, TestMediaRoundTrip)
		return err
	}
	if err := db.insertItems(f.SourceFile, items); err != nil {
		return err
	}
	f.Items = len(items)
	return db.Exec(fmt.Sprintf("INSERT INTO media_files VALUES (%s, %d, %d, %s, %s, %d, %s);", // nosec: every value is quoted by q() (sqlite3 CLI, TestMediaRoundTrip)
		q(f.SourceFile), f.Size, f.Mtime, num(f.Step), q(f.Model), f.Items, q(time.Now().UTC().Format(time.RFC3339))))
}

// ReplaceKind stores a file's labelled text rows of one kind — "shot" (becky-shots' descriptions) or
// "said" (transcript lines, becky-mediasearch --transcript) — dropping that kind's older rows.
func (db *DB) ReplaceKind(file, kind string, items []MediaItem) error {
	if err := db.Exec("DELETE FROM media_items WHERE source_file = " + q(file) + " AND kind = " + q(kind) + ";"); err != nil { // nosec: every value is quoted by q() (sqlite3 CLI, TestMediaRoundTrip)
		return err
	}
	return db.insertItems(file, items)
}

// insertItems writes rows in chunks so one sqlite3 call never gets a huge script.
func (db *DB) insertItems(file string, items []MediaItem) error {
	const chunk = 400
	for i := 0; i < len(items); i += chunk {
		var b strings.Builder
		b.WriteString("BEGIN;\n")
		for _, it := range items[i:min(i+chunk, len(items))] {
			if len(it.Vec) != MediaVectorDim {
				return fmt.Errorf("media vector has %d dims, want %d", len(it.Vec), MediaVectorDim)
			}
			fmt.Fprintf(&b, "INSERT INTO media_items VALUES (%s, %s, %s, %s, %s, vec_f32('%s'));\n", // nosec: every value is quoted by q() (sqlite3 CLI, TestMediaRoundTrip)
				q(file), q(it.Kind), num(it.Start), num(it.End), q(it.Label), vecJSON(it.Vec))
		}
		b.WriteString("COMMIT;\n")
		if err := db.Exec(b.String()); err != nil {
			return err
		}
	}
	return nil
}

// SearchMedia scores stored items against a query vector and returns the `limit` best, limited
// to `files` when given (else the whole database).
func (db *DB) SearchMedia(query []float64, files []string, limit int) ([]MediaHit, error) {
	qv := make([]float32, len(query))
	for i, x := range query {
		qv[i] = float32(x)
	}
	where := ""
	if len(files) > 0 {
		quoted := make([]string, len(files))
		for i, f := range files {
			quoted[i] = q(f)
		}
		where = "WHERE source_file IN (" + strings.Join(quoted, ",") + ") " // nosec: every value is quoted by q() (sqlite3 CLI, TestMediaRoundTrip)
	}
	sql := fmt.Sprintf("SELECT source_file, kind, start_time, end_time, coalesce(label,'') AS label, "+
		"1.0 - vec_distance_cosine(embedding, vec_f32('%s')) AS similarity "+
		"FROM media_items %sORDER BY similarity DESC LIMIT %d;", vecJSON(qv), where, limit)
	var rows []MediaHit
	err := db.queryJSON(sql, &rows)
	return rows, err
}

func vecJSON(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(x), 'g', 6, 32))
	}
	b.WriteByte(']')
	return b.String()
}
