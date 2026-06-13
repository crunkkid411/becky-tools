// db.go — the optional SQLite path for becky-cluster, via the shared beckydb
// package (driver-free sqlite3.exe CLI). Two uses:
//
//   - READ: load previously stored appearance embeddings (--db) so clustering can
//     run over the durable corpus without re-embedding every clip.
//   - WRITE: persist freshly embedded appearances and the cluster results back, so
//     the embeddings are computed once and naming a cluster has somewhere to live.
//
// All schema work goes through beckydb.EnsureClusterSchema (the self-contained,
// additive cluster tables) — never the canonical EnsureSchema, so embed/search are
// untouched.
package main

import (
	"encoding/json"
	"fmt"

	"becky-go/internal/beckydb"
	"becky-go/internal/config"
)

// loadAppearancesFromDB reads stored appearance embeddings of the given modality
// and parses their JSON vectors back into appearance records. modality may be
// "voice", "face", or "both".
func loadAppearancesFromDB(cfg config.Config, dbPath, modality string) ([]appearance, error) {
	db, err := beckydb.Open(cfg, dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := db.EnsureClusterSchema(); err != nil {
		return nil, err
	}
	rows, err := db.ListAppearances(modality)
	if err != nil {
		return nil, fmt.Errorf("list appearances: %w", err)
	}
	apps := make([]appearance, 0, len(rows))
	for _, r := range rows {
		var vec []float64
		if err := json.Unmarshal([]byte(r.VectorJSON), &vec); err != nil || len(vec) == 0 {
			continue // skip a malformed/empty stored vector rather than abort
		}
		apps = append(apps, appearance{
			ID:           r.AppearanceID,
			Modality:     r.Modality,
			SourceFile:   r.SourceFile,
			SourceSHA256: r.SourceSHA256,
			Timestamp:    r.Timestamp,
			FrameIndex:   r.FrameIndex,
			SpeakerID:    r.SpeakerID,
			DetScore:     r.DetScore,
			Vector:       normalize(vec),
		})
	}
	return apps, nil
}

// storeAppearances persists freshly embedded appearances so the embeddings are
// computed once (the SPEC §7 "store-embeddings" durable path). Idempotent via the
// deterministic appearance_id.
func storeAppearances(cfg config.Config, dbPath string, apps []appearance) error {
	db, err := beckydb.Open(cfg, dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	if err := db.EnsureClusterSchema(); err != nil {
		return err
	}
	for _, a := range apps {
		row := beckydb.AppearanceRow{
			AppearanceID: a.ID,
			SourceFile:   a.SourceFile,
			SourceSHA256: a.SourceSHA256,
			Modality:     a.Modality,
			VectorJSON:   vectorJSON(a.Vector),
			Dim:          len(a.Vector),
			Timestamp:    a.Timestamp,
			FrameIndex:   a.FrameIndex,
			SpeakerID:    a.SpeakerID,
			DetScore:     a.DetScore,
		}
		if err := db.UpsertAppearance(row); err != nil {
			return fmt.Errorf("store appearance %s: %w", a.ID, err)
		}
	}
	return nil
}

// storeClusters persists the cluster results (a recurring person + its members)
// so a later "name-once" step has a row to update and back-fill from.
func storeClusters(cfg config.Config, dbPath string, clusters []Cluster) error {
	db, err := beckydb.Open(cfg, dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	if err := db.EnsureClusterSchema(); err != nil {
		return err
	}
	for _, c := range clusters {
		ids := make([]string, 0, len(c.Members))
		for _, m := range c.Members {
			ids = append(ids, m.AppearanceID)
		}
		membersJSON, _ := json.Marshal(ids)
		row := beckydb.ClusterRow{
			ClusterID:     c.ClusterID,
			Modality:      c.Modality,
			SuggestedName: "", // null until a human names it once
			MemberCount:   c.MemberCount,
			DistinctFiles: c.DistinctSourceFiles,
			Cohesion:      c.Cohesion,
			EdgeThreshold: c.EdgeThreshold,
			MembersJSON:   string(membersJSON),
		}
		if err := db.UpsertCluster(row); err != nil {
			return fmt.Errorf("store cluster %s: %w", c.ClusterID, err)
		}
	}
	return nil
}

// vectorJSON renders a vector as a compact JSON float array for storage (the same
// wire format beckydb uses for sqlite-vec). Marshaling a []float64 is exact and
// avoids manual float formatting.
func vectorJSON(v []float64) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}
