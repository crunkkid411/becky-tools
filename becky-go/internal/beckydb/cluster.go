// cluster.go — self-contained storage for becky-cluster (SPEC-PERSON-CLUSTERING §7).
//
// This file is DELIBERATELY independent of the canonical schema (schema.sql /
// EnsureSchema): it ships its own CREATE TABLE IF NOT EXISTS and its own
// EnsureClusterSchema(), so adding cross-corpus person clustering touches neither
// the shared schema init nor the embed/search read/write paths. It is purely
// additive — a DB that never clusters is unaffected, and clustering can run against
// the same forensic.db without migration risk.
//
// Two tables:
//   - appearance_embeddings: one embedded sighting of a (still-unknown) person, with
//     full provenance. This is the durable raw material the SPEC calls for: today
//     becky-identify throws embeddings away after matching, so persisting them once
//     lets clustering run across the whole corpus. Vectors are stored as JSON-array
//     TEXT (e.g. '[0.1,0.2,...]') — the same wire format beckydb already uses for
//     sqlite-vec — so no blob handling and no fixed-dim vec0 table (voice is 192-d,
//     face is 512-d, and clustering reads ALL rows at once rather than KNN-querying).
//   - clusters: one persisted cluster result (a recurring person: "Person A appears
//     in N clips"), with its members serialized as JSON and an optional human name.
//     Naming a cluster once (suggested_name -> name) is the hook that back-fills
//     every member clip and can seed a new KB identity.
//
// Access is via the same driver-free sqlite3.exe CLI used by the rest of beckydb.
package beckydb

import (
	"fmt"
	"strings"
	"time"
)

// clusterSchema is the additive DDL for the clustering tables. Kept as a Go string
// (not a //go:embed of a new .sql) so this file is fully self-contained and cannot
// be mistaken for an edit to the canonical schema.sql.
const clusterSchema = `
-- appearance_embeddings: one embedded sighting of a person (voice or face) with
-- provenance. The durable input to cross-corpus clustering. Additive; independent
-- of segments/segments_vec so embed/search are unaffected.
CREATE TABLE IF NOT EXISTS appearance_embeddings (
    appearance_id TEXT PRIMARY KEY, -- deterministic: sha12(source_file)+":"+modality+":"+frame_index
    source_file   TEXT,             -- path/name of the source clip (provenance)
    source_sha256 TEXT,             -- provenance hash of the source (may be a sha12 fallback)
    modality      TEXT,             -- voice | face
    vector_json   TEXT,             -- L2-normalized embedding as a JSON float array
    dim           INTEGER,          -- vector dimensionality (recorded from the helper; CAM++ voice + ArcFace face are both 512-d on this deployment)
    timestamp     REAL DEFAULT 0,   -- seconds into the clip (voice span start | face frame time)
    frame_index   INTEGER DEFAULT 0,-- frame index (face) or speaker ordinal (voice)
    speaker_id    TEXT,             -- diarization label for voice (e.g. SPEAKER_00; "" for face)
    det_score     REAL DEFAULT 0,   -- detector confidence (face det_score; 1.0 for voice)
    created_at    TEXT              -- RFC3339 timestamp this row was written
);

CREATE INDEX IF NOT EXISTS idx_appearance_modality ON appearance_embeddings(modality);
CREATE INDEX IF NOT EXISTS idx_appearance_source ON appearance_embeddings(source_file);

-- clusters: one persisted clustering result (a recurring person). members_json is
-- the list of appearance_ids in the cluster; suggested_name stays NULL until a
-- human names it once, which then back-fills every member. Additive.
CREATE TABLE IF NOT EXISTS clusters (
    cluster_id     TEXT PRIMARY KEY, -- e.g. "voice-A" / "face-B" (run-stable label)
    modality       TEXT,             -- voice | face
    suggested_name TEXT,             -- human-assigned name; NULL until named once
    member_count   INTEGER DEFAULT 0,-- number of appearances in the cluster
    distinct_files INTEGER DEFAULT 0,-- distinct source files (the "appears in N clips" count)
    cohesion       REAL DEFAULT 0,   -- mean intra-cluster cosine (quality signal)
    edge_threshold REAL DEFAULT 0,   -- the cosine edge used to form this cluster
    members_json   TEXT,             -- JSON array of appearance_ids
    created_at     TEXT              -- RFC3339 timestamp this cluster was written
);

CREATE INDEX IF NOT EXISTS idx_clusters_modality ON clusters(modality);
`

// EnsureClusterSchema creates the clustering tables if absent. Idempotent and safe
// to call on every run. It is SEPARATE from EnsureSchema so becky-cluster can add
// its tables to a forensic DB without invoking (or depending on) the canonical
// schema. A caller that also wants the canonical tables calls EnsureSchema too.
func (db *DB) EnsureClusterSchema() error {
	if err := db.Exec(clusterSchema); err != nil {
		return fmt.Errorf("ensure cluster schema: %w", err)
	}
	return nil
}

// AppearanceRow is one stored appearance embedding (the appearance_embeddings row).
// vector_json is the embedding as a JSON float array; the becky-cluster command
// parses it back into []float64 for the in-Go cosine/clustering math.
type AppearanceRow struct {
	AppearanceID string  `json:"appearance_id"`
	SourceFile   string  `json:"source_file"`
	SourceSHA256 string  `json:"source_sha256"`
	Modality     string  `json:"modality"`
	VectorJSON   string  `json:"vector_json"`
	Dim          int     `json:"dim"`
	Timestamp    float64 `json:"timestamp"`
	FrameIndex   int     `json:"frame_index"`
	SpeakerID    string  `json:"speaker_id"`
	DetScore     float64 `json:"det_score"`
	CreatedAt    string  `json:"created_at"`
}

// UpsertAppearance inserts or replaces one appearance embedding keyed by
// AppearanceID. CreatedAt defaults to now (RFC3339) when empty. INSERT OR REPLACE
// keeps re-embedding the same clip idempotent (deterministic appearance_id).
func (db *DB) UpsertAppearance(a AppearanceRow) error {
	if a.CreatedAt == "" {
		a.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	sql := fmt.Sprintf(
		"INSERT OR REPLACE INTO appearance_embeddings "+
			"(appearance_id, source_file, source_sha256, modality, vector_json, dim, "+
			"timestamp, frame_index, speaker_id, det_score, created_at) "+
			"VALUES (%s, %s, %s, %s, %s, %d, %s, %d, %s, %s, %s);",
		q(a.AppearanceID), q(a.SourceFile), nullableStr(a.SourceSHA256), q(a.Modality),
		q(a.VectorJSON), a.Dim, num(a.Timestamp), a.FrameIndex,
		nullableStr(a.SpeakerID), num(a.DetScore), q(a.CreatedAt),
	)
	return db.Exec(sql)
}

// ListAppearances returns stored appearance embeddings for a modality ("voice" |
// "face"), or all of them when modality is "" or "both". Ordered deterministically
// (modality, source_file, frame_index, appearance_id) for stable clustering input.
// Returns an empty slice (not an error) when the table is absent so a never-stored
// DB degrades cleanly.
func (db *DB) ListAppearances(modality string) ([]AppearanceRow, error) {
	where := ""
	if modality != "" && modality != "both" {
		where = "WHERE modality = " + q(modality) + " "
	}
	sql := "SELECT appearance_id, source_file, COALESCE(source_sha256,'') AS source_sha256, " +
		"modality, vector_json, dim, timestamp, frame_index, " +
		"COALESCE(speaker_id,'') AS speaker_id, det_score, COALESCE(created_at,'') AS created_at " +
		"FROM appearance_embeddings " + where +
		"ORDER BY modality, source_file, frame_index, appearance_id;"
	var rows []AppearanceRow
	if err := db.queryJSON(sql, &rows); err != nil {
		if isMissingAppearanceTable(err) {
			return nil, nil
		}
		return nil, err
	}
	return rows, nil
}

// CountAppearances returns the number of stored appearance embeddings (0 with no
// error when the table is absent).
func (db *DB) CountAppearances() (int, error) {
	n, err := db.scalarInt("SELECT COUNT(*) AS n FROM appearance_embeddings;")
	if err != nil && isMissingAppearanceTable(err) {
		return 0, nil
	}
	return n, err
}

// ClusterRow is one persisted cluster result (a recurring person).
type ClusterRow struct {
	ClusterID     string  `json:"cluster_id"`
	Modality      string  `json:"modality"`
	SuggestedName string  `json:"suggested_name"`
	MemberCount   int     `json:"member_count"`
	DistinctFiles int     `json:"distinct_files"`
	Cohesion      float64 `json:"cohesion"`
	EdgeThreshold float64 `json:"edge_threshold"`
	MembersJSON   string  `json:"members_json"`
	CreatedAt     string  `json:"created_at"`
}

// UpsertCluster inserts or replaces one cluster result keyed by ClusterID.
// CreatedAt defaults to now (RFC3339) when empty.
func (db *DB) UpsertCluster(c ClusterRow) error {
	if c.CreatedAt == "" {
		c.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	sql := fmt.Sprintf(
		"INSERT OR REPLACE INTO clusters "+
			"(cluster_id, modality, suggested_name, member_count, distinct_files, "+
			"cohesion, edge_threshold, members_json, created_at) "+
			"VALUES (%s, %s, %s, %d, %d, %s, %s, %s, %s);",
		q(c.ClusterID), q(c.Modality), nullableStr(c.SuggestedName),
		c.MemberCount, c.DistinctFiles, num(c.Cohesion), num(c.EdgeThreshold),
		q(c.MembersJSON), q(c.CreatedAt),
	)
	return db.Exec(sql)
}

// NameCluster assigns a human name to a cluster (the "name-once" hook): it sets
// suggested_name by cluster_id. From here a caller can seed a KB identity and/or
// back-fill identifications for every member clip (SPEC §7.3/§7.4).
func (db *DB) NameCluster(clusterID, name string) error {
	sql := fmt.Sprintf(
		"UPDATE clusters SET suggested_name = %s WHERE cluster_id = %s;",
		q(name), q(clusterID),
	)
	return db.Exec(sql)
}

// isMissingAppearanceTable matches the "no such table: appearance_embeddings"
// error so reads degrade to empty on a DB that never stored appearances instead of
// failing the whole tool (mirrors isMissingFTSTable / isMissingEmbedMetaTable).
func isMissingAppearanceTable(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "no such table: appearance_embeddings")
}
