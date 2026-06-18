package reel

import (
	"fmt"
	"os"

	"becky-go/internal/config"
)

// GrabFrame writes a single frame-accurate PNG from source at time t (seconds)
// to outPNG. It uses accurate input-seek + a single output frame (R-CUT §4c).
// The source is opened READ-ONLY. Returns an error (never a panic) if ffmpeg is
// absent or the grab fails.
func GrabFrame(source string, t float64, outPNG string) error {
	cfg := config.Load()
	if cfg.FFmpeg == "" || !available(cfg.FFmpeg) {
		return fmt.Errorf("ffmpeg not available (config FFmpeg=%q); cannot grab frame", cfg.FFmpeg)
	}
	if _, err := os.Stat(source); err != nil {
		return fmt.Errorf("source not found: %s", source)
	}
	args := grabFrameArgs(source, t, mustAbs(outPNG))
	if err := runFFmpeg(cfg.FFmpeg, false, args); err != nil {
		return err
	}
	if _, err := os.Stat(outPNG); err != nil {
		return fmt.Errorf("frame grab produced no file: %s", outPNG)
	}
	return nil
}

// grabFrameArgs builds the ffmpeg argv for a single still by timestamp. PURE
// (unit-tested). Accurate seek: -ss before -i decodes to the nearest displayed
// frame, then -frames:v 1 writes exactly one PNG, -update 1 overwrites cleanly.
func grabFrameArgs(source string, t float64, outPNG string) []string {
	return []string{
		"-y", "-hide_banner", "-loglevel", "error",
		"-ss", formatSeconds(t),
		"-i", source,
		"-frames:v", "1",
		"-update", "1",
		outPNG,
	}
}
