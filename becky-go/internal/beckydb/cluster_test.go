package beckydb

import (
	"path/filepath"
	"testing"

	"becky-go/internal/config"
)

// TestClusterRoundTrip exercises the additive cluster tables through the real
// sqlite3 CLI: EnsureClusterSchema (idempotent), UpsertAppearance + ListAppearances
// + CountAppearances (incl. modality filter), UpsertCluster + NameCluster. It skips
// gracefully if the sqlite3 CLI / vec0 extension are not present on this machine.
func TestClusterRoundTrip(t *testing.T) {
	cfg := config.Load()
	if cfg.Sqlite3 == "" || cfg.SqliteVecExt == "" {
		t.Skip("sqlite3 CLI or vec0 extension not configured")
	}
	dbPath := filepath.Join(t.TempDir(), "cluster.db")
	db, err := Open(cfg, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.EnsureClusterSchema(); err != nil {
		t.Skipf("cluster schema unavailable: %v", err)
	}
	// EnsureClusterSchema must be idempotent.
	if err := db.EnsureClusterSchema(); err != nil {
		t.Fatalf("EnsureClusterSchema not idempotent: %v", err)
	}

	voice := AppearanceRow{
		AppearanceID: "src1:voice:0", SourceFile: "clip-one.mp4", SourceSHA256: "ab12cd34",
		Modality: "voice", VectorJSON: "[0.1,0.2,0.3]", Dim: 3, Timestamp: 1.5,
		FrameIndex: 0, SpeakerID: "SPEAKER_00", DetScore: 1.0,
	}
	face := AppearanceRow{
		AppearanceID: "src2:face:42", SourceFile: "clip-two.mp4",
		Modality: "face", VectorJSON: "[0.4,0.5,0.6]", Dim: 3, FrameIndex: 42, DetScore: 0.87,
		CropPath: "face-crops/src2_42_face0.jpg",
	}
	if err := db.UpsertAppearance(voice); err != nil {
		t.Fatalf("UpsertAppearance voice: %v", err)
	}
	if err := db.UpsertAppearance(face); err != nil {
		t.Fatalf("UpsertAppearance face: %v", err)
	}
	// Idempotent re-upsert must not duplicate.
	if err := db.UpsertAppearance(voice); err != nil {
		t.Fatalf("re-UpsertAppearance: %v", err)
	}

	if n, _ := db.CountAppearances(); n != 2 {
		t.Errorf("CountAppearances = %d, want 2", n)
	}

	voices, err := db.ListAppearances("voice")
	if err != nil {
		t.Fatalf("ListAppearances voice: %v", err)
	}
	if len(voices) != 1 || voices[0].AppearanceID != "src1:voice:0" {
		t.Fatalf("ListAppearances(voice) = %+v", voices)
	}
	if voices[0].VectorJSON != "[0.1,0.2,0.3]" || voices[0].Dim != 3 {
		t.Errorf("voice row vector/dim mismatch: %+v", voices[0])
	}
	if voices[0].SpeakerID != "SPEAKER_00" {
		t.Errorf("voice speaker_id = %q, want SPEAKER_00", voices[0].SpeakerID)
	}

	all, err := db.ListAppearances("both")
	if err != nil || len(all) != 2 {
		t.Fatalf("ListAppearances(both) = %d rows (err %v), want 2", len(all), err)
	}

	// The face row must round-trip its crop_path (the schema-change regression):
	// vector_json, det_score, frame_index, AND crop_path come back byte-for-byte.
	faces, err := db.ListAppearances("face")
	if err != nil {
		t.Fatalf("ListAppearances face: %v", err)
	}
	if len(faces) != 1 {
		t.Fatalf("ListAppearances(face) = %d rows, want 1", len(faces))
	}
	got := faces[0]
	if got.CropPath != "face-crops/src2_42_face0.jpg" {
		t.Errorf("face crop_path = %q, want face-crops/src2_42_face0.jpg", got.CropPath)
	}
	if got.VectorJSON != "[0.4,0.5,0.6]" {
		t.Errorf("face vector_json = %q, want [0.4,0.5,0.6]", got.VectorJSON)
	}
	if got.DetScore != 0.87 {
		t.Errorf("face det_score = %v, want 0.87", got.DetScore)
	}
	if got.FrameIndex != 42 {
		t.Errorf("face frame_index = %d, want 42", got.FrameIndex)
	}

	// Persist a cluster, then name it (the "name-once" hook).
	c := ClusterRow{
		ClusterID: "voice-A", Modality: "voice", MemberCount: 2, DistinctFiles: 2,
		Cohesion: 0.81, EdgeThreshold: 0.65, MembersJSON: `["src1:voice:0","src3:voice:0"]`,
	}
	if err := db.UpsertCluster(c); err != nil {
		t.Fatalf("UpsertCluster: %v", err)
	}
	if err := db.NameCluster("voice-A", "Braxton"); err != nil {
		t.Fatalf("NameCluster: %v", err)
	}
}

// TestAppearanceCropPathReplace confirms a second UpsertAppearance with the same
// appearance_id REPLACES the row (count stays 1) and updates crop_path — the
// idempotent-re-run guarantee for the schema change.
func TestAppearanceCropPathReplace(t *testing.T) {
	cfg := config.Load()
	if cfg.Sqlite3 == "" || cfg.SqliteVecExt == "" {
		t.Skip("sqlite3 CLI or vec0 extension not configured")
	}
	db, err := Open(cfg, filepath.Join(t.TempDir(), "replace.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.EnsureClusterSchema(); err != nil {
		t.Skipf("cluster schema unavailable: %v", err)
	}

	row := AppearanceRow{
		AppearanceID: "srcX:face:7", SourceFile: "x.mp4", Modality: "face",
		VectorJSON: "[0.1,0.2]", Dim: 2, FrameIndex: 7, DetScore: 0.5,
		CropPath: "crops/old.jpg",
	}
	if err := db.UpsertAppearance(row); err != nil {
		t.Fatalf("UpsertAppearance 1: %v", err)
	}
	row.CropPath = "crops/new.jpg"
	row.DetScore = 0.9
	if err := db.UpsertAppearance(row); err != nil {
		t.Fatalf("UpsertAppearance 2: %v", err)
	}

	if n, _ := db.CountAppearances(); n != 1 {
		t.Errorf("CountAppearances after replace = %d, want 1", n)
	}
	faces, err := db.ListAppearances("face")
	if err != nil || len(faces) != 1 {
		t.Fatalf("ListAppearances(face) = %d rows (err %v), want 1", len(faces), err)
	}
	if faces[0].CropPath != "crops/new.jpg" {
		t.Errorf("crop_path after replace = %q, want crops/new.jpg", faces[0].CropPath)
	}
	if faces[0].DetScore != 0.9 {
		t.Errorf("det_score after replace = %v, want 0.9", faces[0].DetScore)
	}
}

// TestClusterReadsDegradeOnMissingTable confirms reads return empty (not error) on
// a DB that never created the cluster tables (graceful degrade).
func TestClusterReadsDegradeOnMissingTable(t *testing.T) {
	cfg := config.Load()
	if cfg.Sqlite3 == "" || cfg.SqliteVecExt == "" {
		t.Skip("sqlite3 CLI or vec0 extension not configured")
	}
	dbPath := filepath.Join(t.TempDir(), "empty.db")
	db, err := Open(cfg, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Create the canonical schema only (NO cluster tables) so the file exists.
	if err := db.EnsureSchema(); err != nil {
		t.Skipf("schema/vec0 unavailable: %v", err)
	}
	rows, err := db.ListAppearances("voice")
	if err != nil {
		t.Errorf("ListAppearances on missing table should not error, got %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("ListAppearances on missing table = %d rows, want 0", len(rows))
	}
	if n, err := db.CountAppearances(); err != nil || n != 0 {
		t.Errorf("CountAppearances on missing table = %d (err %v), want 0", n, err)
	}
}
