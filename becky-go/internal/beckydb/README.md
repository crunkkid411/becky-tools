# internal/beckydb — shared SQLite + sqlite-vec layer

The single forensic-database layer for the becky toolset. **becky-embed** writes
it; **becky-search** and **becky-consolidate** read it through this same package
so the schema and the KNN query live in exactly one place.

## How DB access works (no cgo, no go.mod deps)

A cgo SQLite driver plus C-extension loading is fragile here (no guaranteed C
toolchain), so this package drives SQLite through the **`sqlite3.exe` CLI** with
the **sqlite-vec `vec0`** loadable extension auto-loaded on every statement.

- Each `Exec`/query spawns `sqlite3.exe -cmd ".load <vec0>" [-cmd ".mode json"] <db>`
  with the SQL piped to **stdin**. Passing the extension path as its own argv
  element avoids backslash-escaping problems on Windows.
- Vectors are passed as **JSON-array text** (`'[0.1,0.2,...]'`) for both INSERT
  and `MATCH` — no binary blob handling.
- Queries use `.mode json`, so `sqlite3` returns a JSON array of row objects that
  Go unmarshals straight into structs.

Paths come from `internal/config` (never hardcode them):

| Config field        | Meaning                          | Default |
|---------------------|----------------------------------|---------|
| `Sqlite3`           | `sqlite3.exe` CLI                | `C:\ProgramData\anaconda3\Library\bin\sqlite3.exe` |
| `SqliteVecExt`      | `vec0` extension (vec0.dll)      | `...kevs...\models\sqlite-vec\vec0.dll` |
| `EmbedModelCache`   | Qwen3 sentence-transformers cache | `...kevs...\models\embeddings` |

Verified against **sqlite-vec v0.1.9** (`vec_version()`).

## Schema

The runnable DDL is `schema.sql` (embedded via `//go:embed` and applied by
`EnsureSchema()`, idempotently, on every run). Tables:

### `segments` — one transcript segment

| Column | Type | Notes |
|--------|------|-------|
| `segment_id` | TEXT PK | deterministic: `sha12(source_sha256) + ":" + index` (re-runs overwrite, never duplicate) |
| `source_file` | TEXT | source media path/name (provenance) |
| `source_sha256` | TEXT | SHA-256 of the source media file (provenance) |
| `start_time` | REAL | segment start, seconds (clip-relative) |
| `end_time` | REAL | segment end, seconds (clip-relative) |
| `text` | TEXT | transcript text for the segment |
| `speaker_id` | TEXT | diarization label e.g. `SPEAKER_00` (may be `""`) |
| `speaker_name` | TEXT | resolved name e.g. `Defendant`; **NULL until identified** — search shows "unidentified speaker" when empty |
| `speaker_confidence` | REAL | 0..1 confidence in `speaker_name` (0 = unknown) |
| `needs_review` | INTEGER | `1` = needs human review (default); cleared when verified |
| `verified_by` | TEXT | who/what verified the row; **NULL until verified** |
| `created_at` | TEXT | RFC3339, e.g. `2026-06-06T20:00:00Z` |

Indexes: `idx_segments_source(source_file)`, `idx_segments_speaker(speaker_name)`,
`idx_segments_review(needs_review)`.

### `segments_vec` — sqlite-vec virtual table

```sql
CREATE VIRTUAL TABLE IF NOT EXISTS segments_vec USING vec0(
    segment_id TEXT PRIMARY KEY,
    embedding  float[1024] distance_metric=cosine
);
```

- One 1024-dim embedding per segment, keyed by the same `segment_id`.
- `distance_metric=cosine`. Qwen3 vectors are L2-normalized.
- **vec0 does NOT support `INSERT OR REPLACE`** on a TEXT primary key — it raises
  a UNIQUE constraint error. For idempotency, `InsertVector` does `DELETE` then
  `INSERT`.

### `segments_fts` — FTS5 keyword index (hybrid retrieval, keyword half)

```sql
CREATE VIRTUAL TABLE IF NOT EXISTS segments_fts USING fts5(
    segment_id UNINDEXED,
    text,
    tokenize='porter unicode61'
);
```

- The **keyword half of hybrid retrieval**. Dense KNN over `segments_vec` blurs
  exact tokens (names, dates, plates, addresses); this BM25 index finds them by
  literal match. `becky-search` fuses the two rankings with Reciprocal Rank Fusion.
- Built into `sqlite3` — **no extra extension** (unlike vec0). The runnable DDL
  lives in **`schema_fts.sql`** (embedded as `beckydb.SchemaFTS`) and is applied by
  `EnsureSchema()` in its **own** `sqlite3` invocation, *after* the core schema:
  a sqlite3 built without FTS5 errors on the CREATE, and isolating it means that
  error cannot abort the core tables. `EnsureSchema` **swallows** an FTS5-
  unavailable error so embed/search/consolidate still work; `FTS5Available()`
  reports whether keyword search is live, and `becky-search` degrades to
  vector-only (with a note) when it is not.
- `segment_id` is `UNINDEXED` (stored, not tokenized) so FTS rows join back to
  `segments` by the same key. Tokenizer `porter unicode61` = unicode folding +
  English stemming ("buying" matches "buy").
- **FTS5 has no upsert** — like `segments_vec`, `InsertFTS` does `DELETE` then
  `INSERT` by `segment_id` for idempotent re-runs. `becky-embed` populates it
  alongside the segment/vector inserts (gated on `FTS5Available`).

### `media` — optional per-file provenance/probe facts

`source_file` (PK), `source_sha256`, `duration`, `fps`, `ingested_at` (RFC3339).
Populated when becky-embed is given `--source <video>`. consolidate uses it for
per-file coverage math.

### `identifications` — one named recognition (voice/face/location)

Independent of `segments`/`segments_vec` (embed/search are unaffected). Written
and read by **becky-consolidate**: ingested from becky-identify output, reported
on for coverage, and updated by name propagation.

```sql
CREATE TABLE IF NOT EXISTS identifications (
    id            TEXT PRIMARY KEY, -- deterministic: sha12(source)+":"+modality+":"+key
    source_file   TEXT,             -- source video this came from
    source_sha256 TEXT,             -- SHA-256 of the source media (may be "")
    entity_name   TEXT,             -- resolved name e.g. "Defendant" (NULL/"" = unknown)
    modality      TEXT,             -- voice | face | location
    confidence    REAL DEFAULT 0,   -- 0..1 match confidence
    speaker_id    TEXT,             -- diarization label e.g. SPEAKER_00 (may be "")
    verified_by   TEXT,             -- who confirmed it; NULL = unconfirmed (model guess)
    created_at    TEXT              -- RFC3339 timestamp
);
```

Indexes: `idx_ident_source(source_file)`, `idx_ident_entity(entity_name)`,
`idx_ident_modality(modality)`.

- `verified_by` NULL means an unconfirmed model guess; set means a human/analyst
  (or becky-consolidate propagation) confirmed it. Propagation only ever STARTS
  from a confirmed row and only writes onto UNCONFIRMED rows above the threshold —
  it never invents a name for an entity that has no confirmation.
- `id` is deterministic so re-ingesting the same becky-identify run replaces
  rows (idempotent), keyed by `sha12(source)+":"+modality+":"+(speaker_id|name)`.

## Distance → similarity

With `distance_metric=cosine` and L2-normalized vectors:

```
similarity = 1.0 - distance        # range [0, 1], 1.0 = identical
```

(Verified: querying a stored vector returns itself at `distance = 0.0`,
i.e. `similarity = 1.0`.)

## Canonical KNN query (becky-search uses this verbatim)

```sql
SELECT s.segment_id, s.source_file, s.source_sha256, s.start_time, s.end_time,
       s.text, s.speaker_id, s.speaker_name, s.speaker_confidence,
       s.needs_review, s.verified_by, s.created_at,
       v.distance, (1.0 - v.distance) AS similarity
FROM segments_vec v
JOIN segments s ON s.segment_id = v.segment_id
WHERE v.embedding MATCH :qvec AND k = :k       -- :qvec = '[float,...]' (1024)
  AND (1.0 - v.distance) >= :minsim
ORDER BY v.distance;                            -- closest (most similar) first
```

Notes for vec0:
- The `MATCH` operand and `k = :k` constraint are **both required** on the vec0
  table for a KNN scan.
- The query vector goes in as JSON-array **text**.

## Canonical BM25 keyword query (the keyword half of hybrid retrieval)

```sql
SELECT s.segment_id, s.source_file, s.source_sha256, s.start_time, s.end_time,
       s.text, s.speaker_id, s.speaker_name, s.speaker_confidence,
       s.needs_review, s.verified_by, s.created_at,
       bm25(segments_fts) AS bm25
FROM segments_fts f
JOIN segments s ON s.segment_id = f.segment_id
WHERE segments_fts MATCH :match          -- sanitized, e.g. '"pound" OR "sand"'
ORDER BY bm25(segments_fts)              -- ascending: most relevant (most negative) first
LIMIT :k;
```

- `KeywordSearch` returns the **same `Neighbor` shape** as `KNN` so `becky-search`
  fuses the two by rank. For keyword hits `Distance`/`Similarity` are 0 and `BM25`
  carries the raw `bm25()` score.
- `:match` is **sanitized** from arbitrary user text (`sanitizeFTSQuery`): each
  alphanumeric token is double-quoted as a literal phrase and OR-joined, so FTS5
  operators (`-`, `"`, `*`, `:`, `AND`/`OR`/`NEAR`, etc.) in user input can't error
  or change the query semantics. A query with no usable tokens → empty result.

## Go API (import `becky-go/internal/beckydb`)

```go
db, _ := beckydb.Open(cfg, "forensic.db")
db.EnsureSchema()
db.UpsertSegment(beckydb.Segment{...})          // INSERT OR REPLACE (idempotent)
db.InsertVector(segmentID, "[0.1,0.2,...]")     // DELETE + INSERT (idempotent)
db.InsertFTS(segmentID, text)                    // DELETE + INSERT (idempotent; gate on FTS5Available)
db.UpsertMedia(file, sha, duration, fps)
n, _ := db.CountSegments()                       // row counts for summaries
m, _ := db.CountFTS()                            // FTS row count (0 on no-FTS5 build)
neighbors, _ := db.KNN(queryVecJSON, 10, 0.5)    // []Neighbor (vector; Similarity + Rank)
kw, _ := db.KeywordSearch("pound of sand", 10)   // []Neighbor (BM25; BM25 + Rank)
if db.FTS5Available() { /* run hybrid */ }       // probe: is keyword search live?

// identifications (becky-consolidate; additive, embed/search unaffected):
db.UpsertIdentification(beckydb.Identification{...}) // INSERT OR REPLACE (idempotent)
ids, _ := db.ListIdentifications()                   // all rows, deterministic order
db.SetIdentificationVerified(id, name, "analyst")    // the propagation write
files, _ := db.DistinctSourceFiles()                 // coverage denominator (media ∪ ident)
m, _ := db.CountIdentifications()
```

- `beckydb.VectorDim` = `1024`.
- `Segment` / `Neighbor` are the row structs (JSON-tagged) shared by all readers.
- All string values are single-quote-escaped before reaching SQL (`q()`); this is
  the only place untrusted text touches SQL.
