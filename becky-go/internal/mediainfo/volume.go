package mediainfo

import (
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"becky-go/internal/proc"
)

// volume.go measures the loudness of a media file's audio via ffmpeg's
// volumedetect filter. becky-clip uses it to CORROBORATE that a rendered
// compilation actually contains audible sound — not a silent track — and to say so
// to the user. It is the cheap, deterministic, always-on half of "many signals,
// corroborated, before wasting a human's time"; the deep audio/visual check is
// becky-validate (Gemma-4).

// Volume holds the measured loudness in dBFS. Audible reports whether the mean is
// above the silence floor (a digitally-silent track reads about -91 dB / -inf).
type Volume struct {
	MeanDB  float64
	MaxDB   float64
	Audible bool
}

// silenceFloorDB: a mean at or below this is treated as effectively silent (real
// speech/stream audio sits well above it, typically -30..-12 dB).
const silenceFloorDB = -80.0

// MeanVolume runs `ffmpeg -i <path> -af volumedetect -f null -` and parses the
// mean/max volume from its stderr. ok=false when ffmpeg is unavailable or its
// output can't be parsed (degrade-never-crash: the caller then just omits the
// loudness note). The input is opened READ-ONLY; nothing is written.
func MeanVolume(ffmpeg, path string) (Volume, bool) {
	if ffmpeg == "" {
		return Volume{}, false
	}
	cmd := exec.Command(ffmpeg, "-hide_banner", "-nostats", "-i", path, "-af", "volumedetect", "-f", "null", "-")
	proc.NoWindow(cmd) // no console-window flash for GUI callers
	// volumedetect prints to stderr; ffmpeg exits 0 for "-f null -".
	out, _ := cmd.CombinedOutput()
	return parseVolumeDetect(string(out))
}

var (
	reMeanVol = regexp.MustCompile(`mean_volume:\s*(-?[\d.]+|-inf)\s*dB`)
	reMaxVol  = regexp.MustCompile(`max_volume:\s*(-?[\d.]+|-inf)\s*dB`)
)

// parseVolumeDetect extracts mean/max dB from ffmpeg's volumedetect stderr. PURE
// (unit-tested). ok=false when no mean line is present.
func parseVolumeDetect(stderr string) (Volume, bool) {
	m := reMeanVol.FindStringSubmatch(stderr)
	if m == nil {
		return Volume{}, false
	}
	mean := parseDB(m[1])
	v := Volume{MeanDB: mean, Audible: mean > silenceFloorDB}
	if mx := reMaxVol.FindStringSubmatch(stderr); mx != nil {
		v.MaxDB = parseDB(mx[1])
	}
	return v, true
}

// parseDB turns "-21.3" or "-inf" into a float (−inf -> a large negative number).
func parseDB(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "-inf" {
		return math.Inf(-1)
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.Inf(-1)
	}
	return f
}

// Describe renders a short human note like "mean -21.3 dB (audible)".
func (v Volume) Describe() string {
	state := "audible"
	if !v.Audible {
		state = "SILENT"
	}
	mean := fmt.Sprintf("%.1f", v.MeanDB)
	if math.IsInf(v.MeanDB, -1) {
		mean = "-inf"
	}
	return fmt.Sprintf("mean %s dB (%s)", mean, state)
}
