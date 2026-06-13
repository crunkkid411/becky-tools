-- becky forensic database — FTS5 keyword-index DDL (applied separately).
--
-- This is the keyword half of hybrid retrieval. It is kept OUT of schema.sql's
-- core batch on purpose: a sqlite3 built without FTS5 errors on this CREATE, and
-- running it in its own sqlite3 invocation means that failure cannot abort the
-- core tables. EnsureSchema() applies it after the core schema and treats an
-- FTS5-unavailable error as non-fatal (becky-search then degrades to vector-only).
--
-- segment_id is UNINDEXED (stored, not tokenized) so the FTS rows join back to the
-- `segments` table by the same deterministic "<sha12>:<index>" key. tokenizer is
-- 'porter unicode61': unicode-aware case/diacritic folding + English (Porter)
-- stemming, so "buying" matches "buy". becky-embed populates this alongside the
-- segment/vector inserts; FTS5 has no upsert, so re-runs DELETE then INSERT by
-- segment_id to stay idempotent.
CREATE VIRTUAL TABLE IF NOT EXISTS segments_fts USING fts5(
    segment_id UNINDEXED,
    text,
    tokenize='porter unicode61'
);

-- ============================================================================
-- Canonical BM25 keyword query (becky-search uses this verbatim for --mode
-- keyword and the keyword half of --mode hybrid). Bind:
--   :match = a sanitized FTS5 MATCH expression, e.g. '"pound" OR "sand"'
--   :k     = max rows to return
--
--   SELECT s.segment_id, s.source_file, s.source_sha256, s.start_time,
--          s.end_time, s.text, s.speaker_id, s.speaker_name,
--          s.speaker_confidence, s.needs_review, s.verified_by, s.created_at,
--          bm25(segments_fts) AS bm25
--   FROM segments_fts f
--   JOIN segments s ON s.segment_id = f.segment_id
--   WHERE segments_fts MATCH :match
--   ORDER BY bm25(segments_fts)   -- ascending: most relevant (most negative) first
--   LIMIT :k;
-- ============================================================================
