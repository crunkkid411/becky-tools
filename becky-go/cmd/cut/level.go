package main

// level.go — the DETECTION THRESHOLD, made relative to the recording's own level.
//
// WHY THIS EXISTS (2026-08-16, found on Jordan's IMG_9624.MP4).
//
// auto-editor's audio threshold is an ABSOLUTE level (its default is 4% of full
// scale, about -28 dBFS), so how much it cuts depends entirely on how loud the
// recording happens to be. Jordan's own working flow never handed it a raw
// camera file: "4 polish-and-auto-edit_with_captions.bat" re-encodes first with
// `compand ... , volume=4, asoftclip` (about +14 dB on a quiet phone clip) and
// only THEN runs `auto-editor --edit audio:-27dB,stream=all`. becky-cut skipped
// that polish step and ran detection on the raw file, which is why the same clip
// came out shredded here and clean there. Measured on IMG_9624.MP4 (mean volume
// -42.2 dBFS):
//
//	raw + auto-editor default : 38 keep segments, 16.9s kept of 89.2s, fragments
//	                            averaging 0.55s - whole words chopped in half
//	his polish chain, then -27dB: 40 segments, 43.9s kept, phrases intact
//	raw + threshold -41dB       : 35 segments, 49.7s kept, phrases intact
//
// The last line is this file: instead of rewriting his audio, lower the
// threshold by the same amount his polish step would have raised the level.
//
// The rule is "keep what is at or above the file's own average level", clamped
// so it can never be STRICTER than auto-editor's own default — footage that is
// already at a normal level (his cli-cut test_VAD.mp4 is -17.8 dBFS mean) gets
// exactly the behaviour it got before this change.

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"becky-go/internal/proc"
)

const (
	// defaultThresholdDB is auto-editor's own default (4% amplitude = -27.96 dB).
	// The adaptive threshold never goes above it, so this change can only ever
	// keep MORE audio than the previous behaviour, never less.
	defaultThresholdDB = -28.0
	// minThresholdDB is the floor: below this we would be keeping room tone on a
	// near-silent recording, and the VAD post-pass would have to clean it all up.
	minThresholdDB = -50.0
	// defaultHeadroomDB is how far above the file's mean volume the threshold
	// sits. Calibrated against Jordan's own numbers: his polished audio measures
	// -27.8 dBFS mean and he cuts it at -27 dB. Footage whose mic level sits
	// close to the room tone needs a NEGATIVE headroom (threshold BELOW the mean)
	// or the pauses inside sentences read as silence — dial it with --headroom.
	defaultHeadroomDB = 1.0
)

// detectThresholdDB converts a file's mean volume (dBFS) into the auto-editor
// audio threshold to use for it. headroomDB is how far above the mean the
// threshold sits (defaultHeadroomDB unless the caller dials it). PURE — unit-
// tested in cut_test.go.
func detectThresholdDB(meanDB, headroomDB float64) float64 {
	t := meanDB + headroomDB
	if t > defaultThresholdDB {
		t = defaultThresholdDB
	}
	if t < minThresholdDB {
		t = minThresholdDB
	}
	return t
}

// parseMeanVolumeDB pulls "mean_volume: -42.2 dB" out of an ffmpeg volumedetect
// stderr dump. PURE — unit-tested. Reports ok=false when the line is absent
// (no audio stream, an ffmpeg that failed), and the caller then falls back to
// auto-editor's default threshold rather than guessing.
func parseMeanVolumeDB(stderr string) (float64, bool) {
	const marker = "mean_volume:"
	i := strings.Index(stderr, marker)
	if i < 0 {
		return 0, false
	}
	rest := strings.TrimSpace(stderr[i+len(marker):])
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// measureMeanVolumeDB runs ONE audio-only ffmpeg volumedetect pass over the
// input and returns its mean volume in dBFS. Decode-only (`-f null`), so it
// writes nothing and costs a few seconds even on a long recording.
func measureMeanVolumeDB(ffmpeg, input string) (float64, error) {
	cmd := exec.Command(ffmpeg, "-hide_banner", "-nostdin",
		"-i", input, "-vn", "-af", "volumedetect", "-f", "null", "-")
	proc.NoWindow(cmd)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("volumedetect failed: %w", err)
	}
	v, ok := parseMeanVolumeDB(errBuf.String())
	if !ok {
		return 0, fmt.Errorf("volumedetect printed no mean_volume (no audio stream?)")
	}
	return v, nil
}

// editExpr builds auto-editor's --edit argument for a threshold in dB.
// stream=all matches Jordan's own command line (auto-edit.bat), so a clip with
// a second audio track is judged on all of its audio, not just stream 0.
func editExpr(thresholdDB float64) string {
	return fmt.Sprintf("audio:%.1fdB,stream=all", thresholdDB)
}
