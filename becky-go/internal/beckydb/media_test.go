package beckydb

import (
	"math"
	"path/filepath"
	"testing"

	"becky-go/internal/config"
)

// unit returns a MediaVectorDim vector pointing along axis i (cosine 1 with itself, 0 with others).
func unit(i int) []float32 {
	v := make([]float32, MediaVectorDim)
	v[i] = 1
	return v
}

// TestMediaRoundTrip: picture/sound rows and shot rows are stored, scored and replaced
// independently (re-reading a file never wipes its shot log), through the real sqlite3 CLI.
func TestMediaRoundTrip(t *testing.T) {
	cfg := config.Load()
	if cfg.Sqlite3 == "" || cfg.SqliteVecExt == "" {
		t.Skip("sqlite3 CLI or vec0 extension not configured")
	}
	db, err := Open(cfg, filepath.Join(t.TempDir(), "video.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.EnsureMediaSchema(); err != nil {
		t.Skipf("media schema unavailable: %v", err)
	}
	f := MediaFile{SourceFile: `C:\clips\it's a.mp4`, Size: 10, Mtime: 20, Step: 1, Model: "m"}
	if err := db.ReplaceMedia(f, []MediaItem{
		{Kind: "picture", Start: 0, End: 1, Vec: unit(0)},
		{Kind: "sound", Start: 0, End: 5, Vec: unit(1)},
	}); err != nil {
		t.Fatalf("ReplaceMedia: %v", err)
	}
	if err := db.ReplaceKind(f.SourceFile, "shot", []MediaItem{{Kind: "shot", Start: 0, End: 4, Label: "A dog's bark, wide shot.", Vec: unit(2)}}); err != nil {
		t.Fatalf("ReplaceKind: %v", err)
	}
	got, ok, err := db.GetMediaFile(f.SourceFile)
	if err != nil || !ok || got.Items != 2 || got.Step != 1 {
		t.Fatalf("GetMediaFile = %+v ok=%v err=%v", got, ok, err)
	}

	q := make([]float64, MediaVectorDim)
	q[2] = 1
	hits, err := db.SearchMedia(q, nil, 10)
	if err != nil || len(hits) != 3 {
		t.Fatalf("SearchMedia = %v, %v", hits, err)
	}
	if hits[0].Kind != "shot" || hits[0].Label != "A dog's bark, wide shot." || math.Abs(hits[0].Similarity-1) > 1e-5 {
		t.Fatalf("best hit = %+v, want the shot at similarity 1", hits[0])
	}

	// Re-reading the file replaces pictures/sound but keeps the shot.
	if err := db.ReplaceMedia(f, []MediaItem{{Kind: "picture", Start: 0, End: 1, Vec: unit(0)}}); err != nil {
		t.Fatalf("ReplaceMedia again: %v", err)
	}
	hits, _ = db.SearchMedia(q, []string{f.SourceFile}, 10)
	if len(hits) != 2 {
		t.Fatalf("after re-read got %d rows, want 2 (1 picture + the kept shot)", len(hits))
	}
	if hits, _ = db.SearchMedia(q, []string{"other.mp4"}, 10); len(hits) != 0 {
		t.Fatalf("file filter leaked %d rows", len(hits))
	}
}
