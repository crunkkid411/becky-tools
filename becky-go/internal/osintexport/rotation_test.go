package osintexport

import (
	"image"
	_ "image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"becky-go/internal/config"
)

// rotatedTestClip is a real portrait phone clip stored 1280x720 landscape with a
// 90° display rotation (legacy rotate=90 + Display Matrix rotation=-90). Used by
// the ffprobe-backed test; skipped cleanly when absent.
const rotatedTestClip = `E:\TakingBack2007\July 2025\20250704_181431.mp4`

// uprightTestClip is the standard becky test asset: already-portrait 720x1280 with
// NO rotation flag. Used to assert the no-rotation path stays a true no-op.
const uprightTestClip = `X:\AI-2\becky-tools\test.mp4`

// TestNormalizeRotation verifies arbitrary degree values fold to {0,90,180,270},
// including negatives, wrap-around, and near-quadrant snapping. Pure logic — runs
// everywhere.
func TestNormalizeRotation(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{0, 0}, {90, 90}, {180, 180}, {270, 270},
		{-90, 270}, {-180, 180}, {-270, 90},
		{360, 0}, {450, 90}, {-360, 0},
		{89, 90}, {91, 90}, {271, 270}, {44, 0}, {46, 90},
	}
	for _, c := range cases {
		if got := normalizeRotation(c.in); got != c.want {
			t.Errorf("normalizeRotation(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestRotationFilter verifies each quadrant maps to the correct ffmpeg transpose
// expression, and 0/no-rotation yields an empty filter (no-op).
func TestRotationFilter(t *testing.T) {
	cases := []struct {
		deg  int
		want string
	}{
		{0, ""},
		{90, "transpose=1"},              // 90° CW
		{180, "transpose=1,transpose=1"}, // 180°
		{270, "transpose=2"},             // 90° CCW
		{-90, "transpose=2"},             // normalizes to 270
		{360, ""},                        // normalizes to 0
	}
	for _, c := range cases {
		if got := rotationFilter(c.deg); got != c.want {
			t.Errorf("rotationFilter(%d) = %q, want %q", c.deg, got, c.want)
		}
	}
}

// TestRotationArgs verifies the no-rotation case produces no extra args (a true
// no-op for already-upright footage) and the rotated case disables autorotate and
// supplies the explicit filter.
func TestRotationArgs(t *testing.T) {
	if pre, vf := rotationArgs(0); pre != nil || vf != "" {
		t.Errorf("rotationArgs(0) should be a no-op, got pre=%v vf=%q", pre, vf)
	}
	pre, vf := rotationArgs(90)
	if len(pre) != 1 || pre[0] != "-noautorotate" {
		t.Errorf("rotationArgs(90) preInput = %v, want [-noautorotate]", pre)
	}
	if vf != "transpose=1" {
		t.Errorf("rotationArgs(90) vf = %q, want transpose=1", vf)
	}
}

// TestRotationLabel covers the human-readable verbose-log labels.
func TestRotationLabel(t *testing.T) {
	if got := RotationLabel(0); got != "none" {
		t.Errorf("RotationLabel(0) = %q, want none", got)
	}
	if got := RotationLabel(90); got != "90° CW" {
		t.Errorf("RotationLabel(90) = %q, want 90° CW", got)
	}
	if got := RotationLabel(-90); got != "270° CW" {
		t.Errorf("RotationLabel(-90) = %q, want 270° CW", got)
	}
}

// TestDeriveFFprobe verifies the ffmpeg->ffprobe sibling-path derivation used so
// ExtractFrame can probe rotation without separate config.
func TestDeriveFFprobe(t *testing.T) {
	// The dir separator in the result is whatever filepath.Join emits for the host
	// OS, so we assert on the basename + that "ffprobe" replaced "ffmpeg" rather
	// than on the literal separator, keeping the test OS-agnostic.
	cases := []struct {
		ffmpeg, wantBase string
	}{
		{`C:\ProgramData\anaconda3\Library\bin\ffmpeg.exe`, "ffprobe.exe"},
		{"/usr/bin/ffmpeg", "ffprobe"},
		{"ffmpeg", "ffprobe"},
		{"mplayer", "ffprobe"}, // unknown name -> bare ffprobe fallback
	}
	for _, c := range cases {
		got := deriveFFprobe(c.ffmpeg)
		if filepath.Base(got) != c.wantBase {
			t.Errorf("deriveFFprobe(%q) base = %q, want %q (full=%q)", c.ffmpeg, filepath.Base(got), c.wantBase, got)
		}
		if strings.Contains(strings.ToLower(filepath.Base(got)), "ffmpeg") {
			t.Errorf("deriveFFprobe(%q) still contains ffmpeg: %q", c.ffmpeg, got)
		}
	}
}

// TestDisplayRotationReal exercises DisplayRotation against real files via ffprobe.
// The rotated clip must report 90; the upright control must report 0. Skips
// cleanly when ffprobe or the assets are unavailable.
func TestDisplayRotationReal(t *testing.T) {
	cfg := config.Load()
	if _, err := os.Stat(cfg.FFprobe); err != nil {
		t.Skipf("ffprobe not present (%s): %v", cfg.FFprobe, err)
	}

	if _, err := os.Stat(uprightTestClip); err == nil {
		if got := DisplayRotation(cfg.FFprobe, uprightTestClip); got != 0 {
			t.Errorf("DisplayRotation(upright control) = %d, want 0", got)
		}
	} else {
		t.Logf("upright control clip absent, skipping that assertion: %v", err)
	}

	if _, err := os.Stat(rotatedTestClip); err == nil {
		if got := DisplayRotation(cfg.FFprobe, rotatedTestClip); got != 90 {
			t.Errorf("DisplayRotation(rotated clip) = %d, want 90", got)
		}
	} else {
		t.Skipf("rotated test clip absent: %v", err)
	}
}

// TestExtractFrameUpright is the end-to-end proof of the fix: the rotated clip is
// stored 1280x720 (landscape pixels) but displays as 720x1280 portrait. A correct
// ExtractFrame must hand back an UPRIGHT (portrait) frame — height > width — not the
// raw sideways pixels. Skips cleanly when ffmpeg or the rotated clip is unavailable.
func TestExtractFrameUpright(t *testing.T) {
	cfg := config.Load()
	if _, err := os.Stat(cfg.FFmpeg); err != nil {
		t.Skipf("ffmpeg not present (%s): %v", cfg.FFmpeg, err)
	}
	if _, err := os.Stat(rotatedTestClip); err != nil {
		t.Skipf("rotated test clip absent: %v", err)
	}

	out := filepath.Join(t.TempDir(), "upright.jpg")
	if err := ExtractFrame(cfg.FFmpeg, rotatedTestClip, 7.0, out, "jpg", 2); err != nil {
		t.Fatalf("ExtractFrame failed: %v", err)
	}
	f, err := os.Open(out)
	if err != nil {
		t.Fatalf("open extracted frame: %v", err)
	}
	defer f.Close()
	cfgImg, _, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatalf("decode extracted frame: %v", err)
	}
	if cfgImg.Height <= cfgImg.Width {
		t.Errorf("frame is sideways: %dx%d (want portrait, height > width)", cfgImg.Width, cfgImg.Height)
	}
	t.Logf("extracted upright frame: %dx%d", cfgImg.Width, cfgImg.Height)
}
