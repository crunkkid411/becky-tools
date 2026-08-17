// Package mediainfo wraps ffprobe to report the handful of media properties the
// becky tools need: duration, frame rate, and which stream types are present.
package mediainfo

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"becky-go/internal/proc"
)

// Info is the subset of ffprobe output the tools care about.
type Info struct {
	Duration float64 // seconds
	FPS      float64 // frames per second (from r_frame_rate)
	Width    int     // video width in pixels, AS CODED (0 if no video)
	Height   int     // video height in pixels, AS CODED (0 if no video)
	Rotation int     // display-matrix rotation in degrees, normalised to 0/90/180/270
	HasVideo bool
	HasAudio bool
}

// DisplayWidth and DisplayHeight are the dimensions the picture is actually SEEN
// at, which is what a render or a canvas has to be sized to.
//
// A phone shoots portrait but stores the frame landscape (1920x1080) plus a
// display matrix saying "rotate me 90 degrees". Width/Height above are the coded
// numbers and stay landscape; these two swap when the rotation is a quarter turn.
// Use these for anything the viewer sees; use Width/Height only when you truly
// mean the stored frame.
func (i Info) DisplayWidth() int {
	if i.Rotation == 90 || i.Rotation == 270 {
		return i.Height
	}
	return i.Width
}

func (i Info) DisplayHeight() int {
	if i.Rotation == 90 || i.Rotation == 270 {
		return i.Width
	}
	return i.Height
}

// Resolution returns the "WxH" string for the video stream, or "" if none.
func (i Info) Resolution() string {
	if i.Width <= 0 || i.Height <= 0 {
		return ""
	}
	return fmt.Sprintf("%dx%d", i.Width, i.Height)
}

type ffprobeOut struct {
	Streams []struct {
		CodecType  string `json:"codec_type"`
		RFrameRate string `json:"r_frame_rate"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
		// The display matrix arrives as a side-data entry. ffprobe prints its
		// rotation as a NUMBER, but older builds printed a string, so accept
		// both rather than silently reading zero.
		SideData []struct {
			Rotation json.Number `json:"rotation"`
		} `json:"side_data_list"`
		// Some builds also expose it as a stream tag.
		Tags struct {
			Rotate string `json:"rotate"`
		} `json:"tags"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// Probe runs ffprobe and returns duration, frame rate, and stream presence.
func Probe(ffprobe, path string) (Info, error) {
	cmd := exec.Command(ffprobe,
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	)
	proc.NoWindow(cmd) // suppress the console-window flash for GUI callers
	out, err := cmd.Output()
	if err != nil {
		return Info{}, fmt.Errorf("ffprobe failed: %w", err)
	}
	return infoFromProbeJSON(out)
}

// infoFromProbeJSON is the pure half of Probe: ffprobe's JSON in, Info out. Split
// out so the rotation handling can be tested without ffprobe or a media file.
func infoFromProbeJSON(out []byte) (Info, error) {
	var parsed ffprobeOut
	if err := json.Unmarshal(out, &parsed); err != nil {
		return Info{}, fmt.Errorf("parse ffprobe output: %w", err)
	}
	info := Info{}
	info.Duration, _ = strconv.ParseFloat(strings.TrimSpace(parsed.Format.Duration), 64)
	for _, s := range parsed.Streams {
		switch s.CodecType {
		case "video":
			info.HasVideo = true
			if info.FPS == 0 {
				info.FPS = parseRate(s.RFrameRate)
			}
			if info.Width == 0 && s.Width > 0 {
				info.Width = s.Width
				info.Height = s.Height
				rot := 0.0
				for _, sd := range s.SideData {
					if sd.Rotation != "" {
						if f, err := sd.Rotation.Float64(); err == nil {
							rot = f
							break
						}
					}
				}
				if rot == 0 && strings.TrimSpace(s.Tags.Rotate) != "" {
					if f, err := strconv.ParseFloat(strings.TrimSpace(s.Tags.Rotate), 64); err == nil {
						rot = f
					}
				}
				info.Rotation = normalizeRotation(rot)
			}
		case "audio":
			info.HasAudio = true
		}
	}
	return info, nil
}

// normalizeRotation folds any display-matrix angle onto 0/90/180/270.
//
// The sign convention is the confusing part: an iPhone portrait clip reports
// rotation -90, meaning "the stored frame must be turned 90 degrees clockwise to
// be seen correctly". Only whether it is a QUARTER turn matters for sizing, so
// -90 and 270 both land on 270 and swap the dimensions either way.
func normalizeRotation(deg float64) int {
	r := int(deg)
	if float64(r) != deg { // e.g. -90.000001 from a float matrix
		r = int(deg + 0.5*sign(deg))
	}
	r = ((r % 360) + 360) % 360
	switch {
	case r >= 45 && r < 135:
		return 90
	case r >= 135 && r < 225:
		return 180
	case r >= 225 && r < 315:
		return 270
	}
	return 0
}

func sign(f float64) float64 {
	if f < 0 {
		return -1
	}
	return 1
}

// parseRate turns ffprobe's "30000/1001" or "25" into a float.
func parseRate(r string) float64 {
	if strings.Contains(r, "/") {
		parts := strings.SplitN(r, "/", 2)
		num, _ := strconv.ParseFloat(parts[0], 64)
		den, _ := strconv.ParseFloat(parts[1], 64)
		if den != 0 {
			return num / den
		}
		return 0
	}
	f, _ := strconv.ParseFloat(r, 64)
	return f
}
