package faceembed

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"becky-go/internal/config"
)

const testVideo = `X:\AI-2\becky-tools\test.mp4`

// TestParseJSONTolerant verifies the bottom-up JSON scan ignores banner noise and
// reads the last well-formed JSON line — the core robustness of the runner. This
// runs everywhere (no models/deps required).
func TestParseJSONTolerant(t *testing.T) {
	noisy := "Applied providers: ['CPUExecutionProvider']\n" +
		"find model: det_10g.onnx detection\n" +
		`{"dim": 512, "faces": [{"path": "a.jpg", "found": true, "n_faces": 2, "vector": [0.1,0.2], "bbox": [1,2,3,4], "det_score": 0.77}]}` + "\n"
	res, ok := parseJSON(noisy)
	if !ok {
		t.Fatalf("parseJSON failed to find the JSON line")
	}
	if res.Skipped {
		t.Fatalf("unexpected skipped result")
	}
	if len(res.Faces) != 1 {
		t.Fatalf("want 1 face record, got %d", len(res.Faces))
	}
	f := res.Faces[0]
	if !f.Found || f.NFaces != 2 || f.DetScore != 0.77 || len(f.BBox) != 4 {
		t.Fatalf("bad parsed record: %+v", f)
	}
}

// TestParseJSONSkipped verifies a helper "skipped" line is surfaced.
func TestParseJSONSkipped(t *testing.T) {
	res, ok := parseJSON(`{"skipped": true, "reason": "ImportError: insightface"}`)
	if !ok || !res.Skipped || res.Reason == "" {
		t.Fatalf("want skipped result with reason, got ok=%v res=%+v", ok, res)
	}
}

// TestEmbedTwoFaces is the positive path: it composites two copies of the
// t=7.5s face side-by-side (hstack) and asserts Embed reports NFaces>=2 for that
// image. Skips cleanly (not fails) when the test video, ffmpeg, or the face
// models/deps are unavailable, so the suite stays green in barebones environments.
func TestEmbedTwoFaces(t *testing.T) {
	if _, err := os.Stat(testVideo); err != nil {
		t.Skipf("test video not present: %v", err)
	}
	cfg := config.Load()
	if cfg.FFmpeg == "" {
		t.Skip("ffmpeg not configured")
	}
	if _, err := os.Stat(cfg.FaceModelRoot); err != nil {
		t.Skipf("face model root not present (%s): %v", cfg.FaceModelRoot, err)
	}

	dir := t.TempDir()
	face := filepath.Join(dir, "face.jpg")
	two := filepath.Join(dir, "twoface.jpg")

	// Extract the t=7.5s face frame.
	if out, err := exec.Command(cfg.FFmpeg, "-y", "-ss", "7.5", "-i", testVideo,
		"-frames:v", "1", "-q:v", "2", "-loglevel", "error", face).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg extract failed: %v\n%s", err, out)
	}
	// hstack two copies into a 2-face composite.
	if out, err := exec.Command(cfg.FFmpeg, "-y", "-i", face, "-i", face,
		"-filter_complex", "hstack", "-q:v", "2", "-loglevel", "error", two).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg hstack failed: %v\n%s", err, out)
	}

	faces, err := Embed(cfg, []string{two}, "cpu", false)
	if err != nil {
		t.Skipf("face embed unavailable (deps/models): %v", err)
	}
	if len(faces) != 1 {
		t.Fatalf("want 1 result for 1 image, got %d", len(faces))
	}
	if faces[0].NFaces < 2 {
		t.Fatalf("expected NFaces>=2 on the 2-face composite, got %d", faces[0].NFaces)
	}
	t.Logf("2-face composite: NFaces=%d det_score=%.3f", faces[0].NFaces, faces[0].DetScore)
}
