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

// GrabThumb writes ONE small, downscaled JPEG still from source at time t — a
// lightweight timeline thumbnail (not a forensic-quality grab). width is the
// scaled width in px (height auto, kept even); a low JPEG quality keeps the file a
// few KB so it can travel cheaply as a data: URI. Same accurate -ss-before-i seek
// as GrabFrame; the source is opened READ-ONLY. A missing ffmpeg is a clear error
// (the caller degrades to no thumbnail).
func GrabThumb(source string, t float64, outJPG string, width int) error {
	cfg := config.Load()
	if cfg.FFmpeg == "" || !available(cfg.FFmpeg) {
		return fmt.Errorf("ffmpeg not available (config FFmpeg=%q); cannot grab thumb", cfg.FFmpeg)
	}
	if _, err := os.Stat(source); err != nil {
		return fmt.Errorf("source not found: %s", source)
	}
	if width <= 0 {
		width = 160
	}
	if err := runFFmpeg(cfg.FFmpeg, false, grabThumbArgs(source, t, mustAbs(outJPG), width)); err != nil {
		return err
	}
	if _, err := os.Stat(outJPG); err != nil {
		return fmt.Errorf("thumb grab produced no file: %s", outJPG)
	}
	return nil
}

// grabThumbArgs builds the ffmpeg argv for a downscaled JPEG thumbnail. PURE
// (unit-tested). Unlike a forensic frame grab this uses a FAST keyframe seek
// (-noaccurate_seek): it outputs the nearest keyframe at/at-or-before t WITHOUT
// decoding forward to the exact frame. That is both quicker and robust on
// partial/truncated downloads, where the clip's transcript in-point can sit past
// the file's last decodable frame (an accurate seek there yields NO frame). A
// thumbnail only needs a representative still, so the nearest keyframe is ideal.
// scale=W:-2 keeps the aspect ratio with an even height; -q:v 6 stays small.
func grabThumbArgs(source string, t float64, outJPG string, width int) []string {
	return []string{
		"-y", "-hide_banner", "-loglevel", "error",
		"-noaccurate_seek",
		"-ss", formatSeconds(t),
		"-i", source,
		"-frames:v", "1",
		"-update", "1",
		"-vf", fmt.Sprintf("scale=%d:-2", width),
		"-q:v", "6",
		outJPG,
	}
}

// GrabThumbTail grabs a thumbnail from NEAR THE END of source (1s before EOF) — the
// FALLBACK for when the requested in-point sits past a truncated download's last
// decodable frame (GrabThumb there yields nothing). -sseof seeks relative to the
// end, so it lands on the last frame that actually exists, giving the clip a
// representative thumbnail instead of none. Same downscale/quality as GrabThumb.
func GrabThumbTail(source, outJPG string, width int) error {
	cfg := config.Load()
	if cfg.FFmpeg == "" || !available(cfg.FFmpeg) {
		return fmt.Errorf("ffmpeg not available (config FFmpeg=%q); cannot grab tail thumb", cfg.FFmpeg)
	}
	if _, err := os.Stat(source); err != nil {
		return fmt.Errorf("source not found: %s", source)
	}
	if width <= 0 {
		width = 160
	}
	if err := runFFmpeg(cfg.FFmpeg, false, grabThumbTailArgs(source, mustAbs(outJPG), width)); err != nil {
		return err
	}
	if _, err := os.Stat(outJPG); err != nil {
		return fmt.Errorf("tail thumb produced no file: %s", outJPG)
	}
	return nil
}

// grabThumbTailArgs builds the ffmpeg argv for an end-relative thumbnail. PURE
// (unit-tested). -sseof -1 outputs a frame ~1s before the end of the file.
func grabThumbTailArgs(source, outJPG string, width int) []string {
	return []string{
		"-y", "-hide_banner", "-loglevel", "error",
		"-sseof", "-1",
		"-i", source,
		"-frames:v", "1",
		"-update", "1",
		"-vf", fmt.Sprintf("scale=%d:-2", width),
		"-q:v", "6",
		outJPG,
	}
}
