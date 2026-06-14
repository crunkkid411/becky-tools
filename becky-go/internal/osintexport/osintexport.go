// Package osintexport holds the OSINT frame-export primitives shared by
// becky-events (inline location-change exports) and becky-osint (the standalone
// frame exporter). Keeping these in one place keeps the provenance sidecar
// format identical across both tools, which is what lets detectives correlate
// frames from either source.
//
// Provenance is descriptive, never interpretive: we record where a frame came
// from (source file + SHA-256 + timestamp + perceptual hash) and nothing more.
// The fixed note makes that boundary explicit.
package osintexport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"becky-go/internal/pathx"
)

// ProvenanceNote is the exact, required note text for every sidecar. It states
// that the frame is a candidate for matching, not a geolocation conclusion.
const ProvenanceNote = "Candidate location frame for fixture matching. Not a geolocation conclusion."

// Sidecar is the provenance record written next to each exported frame. The
// field set is the union required by both 06-becky-events and 07-becky-osint, so
// the same struct serves both tools.
type Sidecar struct {
	SourceFile     string  `json:"source_file"`
	SourceSHA256   string  `json:"source_sha256"`
	EventType      string  `json:"event_type"`
	Timestamp      float64 `json:"timestamp"`
	FrameIndex     int     `json:"frame_index"`
	FPS            float64 `json:"fps"`
	Resolution     string  `json:"resolution"`
	PerceptualHash string  `json:"perceptual_hash"`
	Notes          string  `json:"notes"`
	ExtractedAt    string  `json:"extracted_at"` // RFC3339
	Tool           string  `json:"tool"`
}

// SHA256File streams a file through SHA-256 and returns the lowercase hex digest.
// Used to fingerprint the source video for provenance (the file is read, never
// modified).
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s for hashing: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ExtractFrame writes a single full-resolution frame at timestampSec to outPath.
// format is "jpg" or "png"; quality is the ffmpeg -q:v value (lower = better,
// used for JPEG). The CUDA hwaccel is attempted first as a best-effort speedup;
// if it fails (no GPU / unsupported), we transparently retry on the CPU so the
// export still succeeds. The source video is only read.
//
// Display rotation is applied automatically: phone footage is usually stored
// sideways with a ±90/180 display flag, and a sideways frame defeats face
// detection (and confuses a human reviewer). ExtractFrame probes the rotation
// (one ffprobe call) and applies the explicit correction so every exported frame
// comes out upright. For tight per-frame loops, probe once with DisplayRotation
// and call ExtractFrameRotated to avoid re-probing on each frame.
func ExtractFrame(ffmpeg, video string, timestampSec float64, outPath, format string, quality int) error {
	// ffmpeg and ffprobe ship together; swap the binary name to probe rotation.
	// If the derived ffprobe path is wrong, DisplayRotation degrades to 0 (no-op).
	return ExtractFrameRotated(ffmpeg, video, timestampSec, outPath, format, quality,
		DisplayRotation(deriveFFprobe(ffmpeg), video))
}

// ExtractFrameRotated is ExtractFrame with a caller-supplied display rotation
// (degrees clockwise to upright; 0 = none). It exists so a frame-sampling loop can
// probe the rotation ONCE (via DisplayRotation) and reuse it for every frame
// instead of re-probing per frame. rotationDeg is normalized internally, so any
// multiple of 90 is accepted; non-quadrant values snap to the nearest quarter-turn.
func ExtractFrameRotated(ffmpeg, video string, timestampSec float64, outPath, format string, quality, rotationDeg int) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("create osint dir: %w", err)
	}
	preInput, vf := rotationArgs(rotationDeg)
	// Place -ss before -i for a fast input seek; -frames:v 1 grabs one frame.
	build := func(hwaccel bool) []string {
		args := []string{"-y"}
		if hwaccel {
			args = append(args, "-hwaccel", "cuda")
		}
		// -noautorotate (from preInput) must precede -i so our explicit rotation
		// filter is the only one applied (avoids a double-rotation).
		args = append(args, preInput...)
		args = append(args,
			"-ss", fmt.Sprintf("%.3f", timestampSec),
			"-i", video,
			"-frames:v", "1")
		if vf != "" {
			args = append(args, "-vf", vf)
		}
		if strings.EqualFold(format, "jpg") || strings.EqualFold(format, "jpeg") {
			args = append(args, "-q:v", fmt.Sprintf("%d", quality))
		}
		args = append(args, "-loglevel", "error", outPath)
		return args
	}

	if err := runFFmpeg(ffmpeg, build(true)); err == nil && fileExists(outPath) {
		return nil
	}
	// Best-effort CUDA path failed; fall back to a plain CPU decode.
	if err := runFFmpeg(ffmpeg, build(false)); err != nil {
		return fmt.Errorf("ffmpeg frame extract at %.3fs: %w", timestampSec, err)
	}
	if !fileExists(outPath) {
		return fmt.Errorf("ffmpeg produced no frame at %.3fs", timestampSec)
	}
	return nil
}

// deriveFFprobe turns an ffmpeg path into the sibling ffprobe path (they ship in
// the same dir), so ExtractFrame can probe rotation without a separate config
// plumb. Falls back to a bare "ffprobe" (resolved on PATH) if the name doesn't
// contain "ffmpeg"; DisplayRotation degrades to 0 if that too is unavailable.
func deriveFFprobe(ffmpeg string) string {
	base := pathx.Base(ffmpeg)
	if strings.Contains(strings.ToLower(base), "ffmpeg") {
		probe := strings.Replace(base, "ffmpeg", "ffprobe", 1)
		probe = strings.Replace(probe, "FFMPEG", "FFPROBE", 1)
		return filepath.Join(filepath.Dir(ffmpeg), probe)
	}
	return "ffprobe"
}

// WriteProvenance serializes a Sidecar to path as indented JSON.
func WriteProvenance(path string, s Sidecar) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create osint dir: %w", err)
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal provenance: %w", err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write provenance %s: %w", path, err)
	}
	return nil
}

func runFFmpeg(ffmpeg string, args []string) error {
	cmd := exec.Command(ffmpeg, args...)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%v: %s", err, tail(errBuf.String()))
	}
	return nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 800 {
		return s[len(s)-800:]
	}
	return s
}
