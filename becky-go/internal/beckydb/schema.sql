-- becky forensic database schema (canonical).
--
-- Shared by becky-embed (writer) and becky-search / becky-consolidate (readers).
-- This file is the single source of truth: beckydb embeds it via //go:embed and
-- EnsureSchema() runs it idempotently on every tool invocation.
--
-- Access: all statements run through sqlite3.exe with the sqlite-vec "vec0"
-- loadable extension auto-loaded (.load <vec0>). Vectors are passed as JSON-array
-- text, e.g. '[0.1,0.2,...]', for both INSERT and MATCH.

-- segments: one transcript segment (caption-sized chunk of speech) with its
-- provenance, timing, text, speaker enrichment, and review state. The vec0 table
-- below holds the matching embedding keyed by the same segment_id.
CREATE TABLE IF NOT EXISTS segments (
    segment_id         TEXT PRIMARY KEY,  -- deterministic: sha12(source_sha256)+":"+index
    source_file        TEXT,              -- path/name of the source media (provenance)
    source_sha256      TEXT,              -- SHA-256 of the source media file (provenance)
    start_time         REAL,              -- segment start, seconds (clip-relative)
    end_time           REAL,              -- segment end, seconds (clip-relative)
    text               TEXT,              -- transcript text for this segment
    speaker_id         TEXT,              -- diarization label, e.g. "SPEAKER_00" (may be "")
    speaker_name       TEXT,              -- resolved name, e.g. "Defendant"; NULL until identified
    speaker_confidence REAL DEFAULT 0,    -- 0..1 confidence in speaker_name (0 = unknown)
    needs_review       INTEGER DEFAULT 1, -- 1 = needs human review; cleared when verified
    verified_by        TEXT,              -- who/what verified this row; NULL until verified
    created_at         TEXT               -- RFC3339 timestamp this row was written
);

-- Helpful covering indexes for the search/consolidate read paths.
CREATE INDEX IF NOT EXISTS idx_segments_source ON segments(source_file);
CREATE INDEX IF NOT EXISTS idx_segments_speaker ON segments(speaker_name);
CREATE INDEX IF NOT EXISTS idx_segments_review ON segments(needs_review);

-- segments_vec: sqlite-vec virtual table holding the 1024-dim Qwen3 embedding
-- for each segment, keyed by segment_id. distance_metric=cosine + L2-normalized
-- vectors means: similarity = 1.0 - distance (in [0,1]).
CREATE VIRTUAL TABLE IF NOT EXISTS segments_vec USING vec0(
    segment_id TEXT PRIMARY KEY,
    embedding  float[1024] distance_metric=cosine
);

-- segments_fts: FTS5 full-text index over segment text, for BM25 keyword search.
-- This is the keyword half of hybrid retrieval: dense KNN over segments_vec blurs
-- exact tokens (names, dates, plates, addresses), so we also keep a literal index
-- and fuse the two rankings with Reciprocal Rank Fusion in becky-search. FTS5 is
-- built into sqlite3 (no extra extension); the tokenizer is 'porter unicode61'
-- (unicode-aware folding + English stemming so "buying" matches "buy"). segment_id
-- is UNINDEXED (stored but not tokenized) so it can be joined back to `segments`.
-- becky-embed populates this alongside the segment/vector inserts; like segments_vec
-- it has no upsert, so re-runs DELETE then INSERT by segment_id to stay idempotent.
--
-- The runnable DDL lives in schema_fts.sql (embedded separately) and is applied by
-- EnsureSchema() in its OWN sqlite3 invocation, NOT in this core batch: a sqlite3
-- build compiled WITHOUT FTS5 errors on the CREATE, and running it apart from the
-- core tables means that failure can't abort the rest of the schema. EnsureSchema
-- swallows an FTS5-unavailable error so embed/search/consolidate still work, and
-- becky-search degrades to vector-only with a note. The statement is:
--
--   CREATE VIRTUAL TABLE IF NOT EXISTS segments_fts USING fts5(
--       segment_id UNINDEXED,
--       text,
--       tokenize='porter unicode61'
--   );

-- embed_meta: single-row-per-key metadata about how this DB was indexed. The
-- critical key is 'embed_model' — the embedding model whose vector space the
-- segments_vec table lives in (e.g. "qwen3-4b" served by the resident
-- llama-server, or "qwen3-0.6b" in-process). becky-search reads it and REFUSES
-- to run when the query model differs, because cosine across two different
-- embedding spaces is meaningless (silent garbage). becky-embed writes it and
-- FAILS if a re-index would mix a new model into a DB already indexed with
-- another. Plain key/value so it never needs a migration.
CREATE TABLE IF NOT EXISTS embed_meta (
    key   TEXT PRIMARY KEY, -- e.g. 'embed_model'
    value TEXT              -- e.g. 'qwen3-4b'
);

-- media: optional per-source-file provenance + probe facts. becky-embed fills
-- this when a --source video is supplied; consolidate uses it for per-file
-- coverage math (e.g. "recognized in 47/92 videos").
CREATE TABLE IF NOT EXISTS media (
    source_file   TEXT PRIMARY KEY, -- path/name of the source media
    source_sha256 TEXT,             -- SHA-256 of the source media file
    duration      REAL,             -- media duration, seconds
    fps           REAL,             -- video frames per second (0 for audio-only)
    ingested_at   TEXT              -- RFC3339 timestamp first ingested
);

-- identifications: one named recognition of an entity (voice/face/location) in
-- one source video, ingested from becky-identify output. becky-consolidate is
-- the writer (via --ingest) and the reader (coverage report + propagation). It
-- is independent of segments/segments_vec so embed/search are unaffected.
--   - id is deterministic so re-ingesting the same identify run is idempotent:
--     sha12(source_file) + ":" + modality + ":" + speaker_id-or-entity.
--   - verified_by NULL => unconfirmed (a model guess); set => human/analyst
--     confirmed. Propagation only ever STARTS from a confirmed row and only
--     writes entity_name/verified_by onto UNCONFIRMED rows above --threshold.
CREATE TABLE IF NOT EXISTS identifications (
    id            TEXT PRIMARY KEY, -- deterministic: sha12(source)+":"+modality+":"+key
    source_file   TEXT,             -- path/name of the source video this came from
    source_sha256 TEXT,             -- SHA-256 of the source media (provenance; may be "")
    entity_name   TEXT,             -- resolved name, e.g. "Defendant" (may be "" if unknown)
    modality      TEXT,             -- voice | face | location
    confidence    REAL DEFAULT 0,   -- 0..1 match confidence
    speaker_id    TEXT,             -- diarization label e.g. SPEAKER_00 (voice/face; may be "")
    verified_by   TEXT,             -- who confirmed it; NULL = unconfirmed (model guess)
    created_at    TEXT              -- RFC3339 timestamp this row was written
);

-- Read-path indexes for consolidate: per-source coverage and per-entity rollups.
CREATE INDEX IF NOT EXISTS idx_ident_source ON identifications(source_file);
CREATE INDEX IF NOT EXISTS idx_ident_entity ON identifications(entity_name);
CREATE INDEX IF NOT EXISTS idx_ident_modality ON identifications(modality);

-- media_meta: yt-dlp .info.json provenance for a source file, ingested by the
-- becky-pipeline "metadata" step. This is the timeline anchor + identity layer
-- for downloaded video: when it was uploaded (upload_iso = RFC3339 UTC), who
-- uploaded it (uploader/channel + ids), title/description, source URL/id, and
-- duration. One row per source_file. Additive (CREATE TABLE IF NOT EXISTS), so
-- embed/search are unaffected on DBs that never ingest metadata.
CREATE TABLE IF NOT EXISTS media_meta (
    source_file   TEXT PRIMARY KEY, -- path/name of the source media
    video_id      TEXT,             -- yt-dlp "id" (e.g. dQw4w9WgXcQ)
    title         TEXT,             -- video title
    description   TEXT,             -- video description (full text; searchable)
    uploader      TEXT,             -- uploader display name
    uploader_id   TEXT,             -- uploader id / @handle
    channel       TEXT,             -- channel name
    channel_id    TEXT,             -- channel id (UC...)
    channel_url   TEXT,             -- channel URL
    upload_iso    TEXT,             -- RFC3339 UTC upload time (the timeline anchor)
    upload_unix   INTEGER,          -- unix seconds (0 if unknown)
    duration      REAL,             -- seconds
    webpage_url   TEXT,             -- canonical source URL
    chapters_json TEXT,             -- chapters[] as JSON (may be '[]')
    tags_json     TEXT,             -- tags[] as JSON (may be '[]')
    ingested_at   TEXT              -- RFC3339 timestamp this row was written
);

CREATE INDEX IF NOT EXISTS idx_media_meta_upload ON media_meta(upload_iso);
CREATE INDEX IF NOT EXISTS idx_media_meta_channel ON media_meta(channel_id);

-- live_chat: timestamped chat lines from a yt-dlp .live_chat.json, ingested by
-- the becky-pipeline "metadata" step. Each row is one chat message: who said it,
-- what, and when (offset_sec into the video) — searchable who/when/what for
-- cross-referencing against the transcript timeline. Deterministic chat_id keeps
-- re-ingestion idempotent: sha12(source_file)+":"+row-ordinal.
CREATE TABLE IF NOT EXISTS live_chat (
    chat_id     TEXT PRIMARY KEY, -- deterministic: sha12(source_file)+":"+ordinal
    source_file TEXT,             -- the video this chat belongs to
    author      TEXT,             -- chat author handle
    text        TEXT,             -- message text
    offset_sec  REAL,             -- seconds into the video
    created_at  TEXT              -- RFC3339 timestamp this row was written
);

CREATE INDEX IF NOT EXISTS idx_live_chat_source ON live_chat(source_file);

-- ============================================================================
-- Canonical KNN query (becky-search uses this verbatim). Bind:
--   :qvec  = JSON array of 1024 floats, e.g. '[0.1,...]'
--   :k     = number of neighbors to fetch
--   :minsim= minimum similarity (1 - distance) to keep
--
--   SELECT s.segment_id, s.source_file, s.source_sha256, s.start_time,
--          s.end_time, s.text, s.speaker_id, s.speaker_name,
--          s.speaker_confidence, s.needs_review, s.verified_by, s.created_at,
--          v.distance, (1.0 - v.distance) AS similarity
--   FROM segments_vec v
--   JOIN segments s ON s.segment_id = v.segment_id
--   WHERE v.embedding MATCH :qvec AND k = :k
--     AND (1.0 - v.distance) >= :minsim
--   ORDER BY v.distance;        -- closest (most similar) first
-- ============================================================================
