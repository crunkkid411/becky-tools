package facecrop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"becky-go/internal/beckydb"
	"becky-go/internal/faceembed"
)

// faceModality is the appearance_embeddings.modality value for a face sighting.
const faceModality = "face"

// Sha12 returns the first 12 hex chars of the SHA-256 of s — the same short-id
// scheme used across beckydb for deterministic provenance-tied keys (segment_id,
// chat_id, ocr_id, etc.; see cmd/consolidate/ingest.go). Exported so the producer
// (cmd/identify) can build crop filenames with the SAME id the DB row uses.
func Sha12(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

// AppearanceID is the deterministic primary key for a face appearance row:
// sha12(srcFile)+":face:"+frameIndex. Matching the scheme documented in
// cluster.go's appearance_embeddings DDL, a re-run of the same clip overwrites the
// same row (INSERT OR REPLACE) rather than duplicating.
func AppearanceID(srcFile string, frameIndex int) string {
	return Sha12(srcFile) + ":" + faceModality + ":" + strconv.Itoa(frameIndex)
}

// CropFileName is the deterministic crop artifact base name for a detection:
// <sha12(srcFile)>_<frameIndex>_face<bboxOrdinal>.<format>. bboxOrdinal is 0 for the
// single prominent face becky-identify embeds today (1,2,… reserved for the
// multi-face extension). Deterministic, so a re-run overwrites the same file.
func CropFileName(srcFile string, frameIndex, bboxOrdinal int, format string) string {
	ext := strings.ToLower(strings.TrimSpace(format))
	if ext == "" || ext == "jpeg" {
		ext = "jpg"
	}
	return fmt.Sprintf("%s_%d_face%d.%s", Sha12(srcFile), frameIndex, bboxOrdinal, ext)
}

// CropPath joins the crop directory with the deterministic crop file name.
func CropPath(cropDir, srcFile string, frameIndex, bboxOrdinal int, format string) string {
	return filepath.Join(cropDir, CropFileName(srcFile, frameIndex, bboxOrdinal, format))
}

// AppearanceFromFace builds the beckydb.AppearanceRow that persists one detected,
// embedded face into the appearance_embeddings table. It is pure (no I/O): the
// caller writes the row via db.UpsertAppearance after db.EnsureClusterSchema.
//
// Mapping (per SPEC-FACE-CROP-DB.md §2c):
//   - AppearanceID: deterministic sha12(srcFile)+":face:"+frameIndex
//   - SourceFile/SourceSHA256: provenance from the identify run
//   - Modality: "face" (constant)
//   - VectorJSON: f.Vector marshaled as a JSON float array
//   - Dim: len(f.Vector) (recorded, not assumed to be 512)
//   - Timestamp: frame time in seconds; FrameIndex: int(ts*fps+0.5) (passed in)
//   - SpeakerID: "" (face); DetScore: f.DetScore
//   - CropPath: path to the tight crop artifact ("" when no crop was written)
//
// CreatedAt is left empty so UpsertAppearance stamps it (keeps callers simple).
func AppearanceFromFace(srcFile, srcSHA256 string, ts float64, frameIndex int, f faceembed.Face, cropPath string) beckydb.AppearanceRow {
	vecJSON := "[]"
	if len(f.Vector) > 0 {
		if b, err := json.Marshal(f.Vector); err == nil {
			vecJSON = string(b)
		}
	}
	return beckydb.AppearanceRow{
		AppearanceID: AppearanceID(srcFile, frameIndex),
		SourceFile:   srcFile,
		SourceSHA256: srcSHA256,
		Modality:     faceModality,
		VectorJSON:   vecJSON,
		Dim:          len(f.Vector),
		Timestamp:    ts,
		FrameIndex:   frameIndex,
		SpeakerID:    "",
		DetScore:     f.DetScore,
		CropPath:     cropPath,
	}
}
