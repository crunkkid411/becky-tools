// Package mediainfo wraps ffprobe to report the handful of media properties the
// becky tools need: duration, frame rate, and which stream types are present.
package mediainfo

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Info is the subset of ffprobe output the tools care about.
type Info struct {
	Duration float64 // seconds
	FPS      float64 // frames per second (from r_frame_rate)
	Width    int     // video width in pixels (0 if no video)
	Height   int     // video height in pixels (0 if no video)
	HasVideo bool
	HasAudio bool
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
	out, err := cmd.Output()
	if err != nil {
		return Info{}, fmt.Errorf("ffprobe failed: %w", err)
	}
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
			}
		case "audio":
			info.HasAudio = true
		}
	}
	return info, nil
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
