// rotation.go — display-rotation detection so exported frames come out UPRIGHT.
//
// Why this exists: phone footage is almost always captured with the sensor in one
// orientation and a container-level *display rotation* flag (±90/180) telling the
// player to rotate it for viewing. The pixels on disk are sideways; only the flag
// makes them upright. If a frame is exported without honoring that flag, every
// downstream consumer (InsightFace face detection, OSINT exhibit frames, the
// reviewer's eyes) sees a sideways image. SCRFD in particular silently detects
// NOTHING on a 90°-rotated face, so a face-enrolled person is dropped corpus-wide.
//
// ffmpeg *can* auto-rotate, but whether it does depends on the build, the decode
// path (hwaccel can bypass it), and the filtergraph — too fragile to rely on for a
// forensic tool. So we read the rotation explicitly via ffprobe and apply it
// explicitly with a transpose/rotate filter (under -noautorotate), making the
// result deterministic regardless of ffmpeg's implicit behavior.
package osintexport

import (
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
)

// rotationProbe mirrors the slice of ffprobe -show_streams we need: the legacy
// per-stream "rotate" tag and the modern Display Matrix side_data "rotation".
type rotationProbe struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		Tags      struct {
			Rotate string `json:"rotate"`
		} `json:"tags"`
		SideDataList []struct {
			SideDataType string  `json:"side_data_type"`
			Rotation     float64 `json:"rotation"`
		} `json:"side_data_list"`
	} `json:"streams"`
}

// DisplayRotation returns the clockwise display rotation, in degrees, that must be
// applied to the decoded (sideways) frame to view it upright. The result is
// normalized to one of {0, 90, 180, 270}. 0 means no correction is needed.
//
// Sources, in priority order (first that yields a non-zero value wins):
//  1. Display Matrix side_data "rotation" (the modern, authoritative source).
//  2. The legacy stream "rotate" tag.
//
// Sign convention: ffmpeg's side_data "rotation" is the angle (counter-clockwise)
// to rotate the decoded frame to reach display orientation, so the clockwise
// correction we want is its negation. The legacy "rotate" tag is already the
// clockwise display angle. Both are normalized into [0,360) here. A probe failure
// degrades gracefully to 0 (no rotation) so extraction still proceeds.
func DisplayRotation(ffprobe, video string) int {
	cmd := exec.Command(ffprobe,
		"-v", "error",
		"-select_streams", "v:0",
		"-print_format", "json",
		"-show_streams",
		video,
	)
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	var p rotationProbe
	if json.Unmarshal(out, &p) != nil {
		return 0
	}
	for _, s := range p.Streams {
		if s.CodecType != "" && s.CodecType != "video" {
			continue
		}
		// Modern Display Matrix side_data: negate to convert ffmpeg's CCW
		// "rotation" into the clockwise correction we apply.
		for _, sd := range s.SideDataList {
			if sd.Rotation != 0 {
				return normalizeRotation(-int(math.Round(sd.Rotation)))
			}
		}
		// Legacy rotate tag (already clockwise display degrees).
		if s.Tags.Rotate != "" {
			if v, perr := strconv.Atoi(strings.TrimSpace(s.Tags.Rotate)); perr == nil && v != 0 {
				return normalizeRotation(v)
			}
		}
	}
	return 0
}

// normalizeRotation folds an arbitrary degree value into {0, 90, 180, 270}.
// Non-multiples of 90 snap to the nearest quadrant (phone footage is always a
// quarter-turn; anything else is metadata noise we round rather than skew on).
func normalizeRotation(deg int) int {
	deg %= 360
	if deg < 0 {
		deg += 360
	}
	// Snap to nearest 90 to be robust to off-by-small metadata values.
	q := int(math.Round(float64(deg)/90.0)) % 4
	return q * 90
}

// rotationFilter returns the ffmpeg -vf value that rotates the decoded frame
// clockwise by the given normalized degrees so it displays upright, or "" when no
// rotation is needed. transpose=1 = 90° CW, transpose=2 = 90° CCW; 180° chains two.
func rotationFilter(deg int) string {
	switch normalizeRotation(deg) {
	case 90:
		return "transpose=1"
	case 180:
		return "transpose=1,transpose=1"
	case 270:
		return "transpose=2"
	default:
		return ""
	}
}

// rotationArgs returns the ffmpeg args that disable implicit autorotation and
// apply the explicit correction filter, or nil when no rotation is needed. Kept
// separate so ExtractFrame can splice it into either the CUDA or CPU arg build.
func rotationArgs(deg int) (preInput []string, vf string) {
	vf = rotationFilter(deg)
	if vf == "" {
		return nil, ""
	}
	// -noautorotate must precede -i so our explicit filter is the only rotation
	// applied (otherwise ffmpeg may double-rotate).
	return []string{"-noautorotate"}, vf
}

// RotationLabel is a small human-readable description of a display rotation for
// verbose logging by callers (e.g. "none", "90° CW").
func RotationLabel(deg int) string {
	deg = normalizeRotation(deg)
	if deg == 0 {
		return "none"
	}
	return fmt.Sprintf("%d° CW", deg)
}
