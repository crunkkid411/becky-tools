package facecrop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"

	"becky-go/internal/faceembed"
)

func TestSha12(t *testing.T) {
	in := "clip-one.mp4"
	sum := sha256.Sum256([]byte(in))
	want := hex.EncodeToString(sum[:])[:12]
	if got := Sha12(in); got != want {
		t.Errorf("Sha12(%q) = %q, want %q", in, got, want)
	}
	if len(Sha12(in)) != 12 {
		t.Errorf("Sha12 length = %d, want 12", len(Sha12(in)))
	}
}

func TestAppearanceID(t *testing.T) {
	want := Sha12("clip.mp4") + ":face:42"
	if got := AppearanceID("clip.mp4", 42); got != want {
		t.Errorf("AppearanceID = %q, want %q", got, want)
	}
}

func TestCropFileName(t *testing.T) {
	sha := Sha12("clip.mp4")
	tests := []struct {
		format string
		want   string
	}{
		{"jpg", sha + "_42_face0.jpg"},
		{"jpeg", sha + "_42_face0.jpg"}, // jpeg normalizes to jpg
		{"png", sha + "_42_face0.png"},
		{"", sha + "_42_face0.jpg"}, // empty defaults to jpg
	}
	for _, tc := range tests {
		if got := CropFileName("clip.mp4", 42, 0, tc.format); got != tc.want {
			t.Errorf("CropFileName(format=%q) = %q, want %q", tc.format, got, tc.want)
		}
	}
}

func TestAppearanceFromFace(t *testing.T) {
	vec := []float64{0.1, -0.2, 0.3, 0.4}
	f := faceembed.Face{
		Path:     "/tmp/frame.jpg",
		Found:    true,
		NFaces:   1,
		Vector:   vec,
		DetScore: 0.873,
		BBox:     []float64{10, 20, 110, 220},
	}
	row := AppearanceFromFace("clip.mp4", "abc123sha", 12.5, 375, f, "/crops/abc_375_face0.jpg")

	if got, want := row.AppearanceID, Sha12("clip.mp4")+":face:375"; got != want {
		t.Errorf("AppearanceID = %q, want %q", got, want)
	}
	if row.SourceFile != "clip.mp4" {
		t.Errorf("SourceFile = %q", row.SourceFile)
	}
	if row.SourceSHA256 != "abc123sha" {
		t.Errorf("SourceSHA256 = %q", row.SourceSHA256)
	}
	if row.Modality != "face" {
		t.Errorf("Modality = %q, want face", row.Modality)
	}
	if row.Dim != 4 {
		t.Errorf("Dim = %d, want 4", row.Dim)
	}
	if row.Timestamp != 12.5 {
		t.Errorf("Timestamp = %v, want 12.5", row.Timestamp)
	}
	if row.FrameIndex != 375 {
		t.Errorf("FrameIndex = %d, want 375", row.FrameIndex)
	}
	if row.SpeakerID != "" {
		t.Errorf("SpeakerID = %q, want empty (face)", row.SpeakerID)
	}
	if row.DetScore != 0.873 {
		t.Errorf("DetScore = %v, want 0.873", row.DetScore)
	}
	if row.CropPath != "/crops/abc_375_face0.jpg" {
		t.Errorf("CropPath = %q", row.CropPath)
	}

	// vector_json must JSON-parse back to the exact input vector.
	var back []float64
	if err := json.Unmarshal([]byte(row.VectorJSON), &back); err != nil {
		t.Fatalf("vector_json does not parse: %v (%q)", err, row.VectorJSON)
	}
	if !reflect.DeepEqual(back, vec) {
		t.Errorf("vector round-trip = %v, want %v", back, vec)
	}
}

func TestAppearanceFromFaceEmptyVector(t *testing.T) {
	f := faceembed.Face{Found: false}
	row := AppearanceFromFace("clip.mp4", "", 0, 0, f, "")
	if row.Dim != 0 {
		t.Errorf("Dim = %d, want 0 for empty vector", row.Dim)
	}
	if row.VectorJSON != "[]" {
		t.Errorf("VectorJSON = %q, want []", row.VectorJSON)
	}
}
