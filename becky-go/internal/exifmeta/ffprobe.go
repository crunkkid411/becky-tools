// ffprobe.go — the ffprobe fallback extraction path, used when exiftool is not
// installed (or fails). ffprobe is always present in the becky toolchain, so this
// guarantees a metadata block is always produced. Coverage is narrower than
// exiftool (fewer vendor tags), but it still recovers the forensically critical
// fields: the container creation_time (UTC), the Android/Samsung UTC-offset tag,
// codecs, resolution, and display rotation.
package exifmeta

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ffprobeResult mirrors the subset of `ffprobe -show_format -show_streams
// -print_format json` output this path consumes.
type ffprobeResult struct {
	Streams []ffStream `json:"streams"`
	Format  ffFormat   `json:"format"`
}

type ffStream struct {
	CodecType    string            `json:"codec_type"`
	CodecName    string            `json:"codec_name"`
	Width        int               `json:"width"`
	Height       int               `json:"height"`
	Duration     string            `json:"duration"`
	Tags         map[string]string `json:"tags"`
	SideDataList []struct {
		SideDataType string      `json:"side_data_type"`
		Rotation     json.Number `json:"rotation"`
	} `json:"side_data_list"`
}

type ffFormat struct {
	FormatName     string            `json:"format_name"`
	FormatLongName string            `json:"format_long_name"`
	Duration       string            `json:"duration"`
	Tags           map[string]string `json:"tags"`
}

// extractFFprobe runs ffprobe and maps its output onto Metadata.
func (e Extractor) extractFFprobe(path string) (Metadata, error) {
	out, err := e.runFFprobe(path)
	if err != nil {
		return Metadata{}, err
	}
	var res ffprobeResult
	if err := json.Unmarshal(out, &res); err != nil {
		return Metadata{}, fmt.Errorf("parse ffprobe json: %w", err)
	}
	md := Metadata{Source: "ffprobe"}
	mapFFprobe(&md, res, path)
	return md, nil
}

// runFFprobe invokes ffprobe with the documented forensic flag set.
func (e Extractor) runFFprobe(path string) ([]byte, error) {
	cmd := exec.Command(e.FFprobe,
		"-v", "error",
		"-show_format",
		"-show_streams",
		"-print_format", "json",
		path,
	)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %v: %s", err, tail(errBuf.String()))
	}
	return out, nil
}

// mapFFprobe fills Metadata from a parsed ffprobe result.
func mapFFprobe(md *Metadata, res ffprobeResult, path string) {
	fmtTags := lowerKeys(res.Format.Tags)

	// --- Container / codecs / resolution / rotation ---
	md.Container = simplifyContainer(res.Format.FormatName, path)
	var vstream *ffStream
	for i := range res.Streams {
		s := &res.Streams[i]
		switch s.CodecType {
		case "video":
			if md.VideoCodec == "" {
				md.VideoCodec = strings.ToLower(s.CodecName)
			}
			if s.Width > 0 && s.Height > 0 && md.Resolution == "" {
				md.Resolution = fmt.Sprintf("%dx%d", s.Width, s.Height)
			}
			vstream = s
		case "audio":
			if md.AudioCodec == "" {
				md.AudioCodec = strings.ToLower(s.CodecName)
			}
		}
	}
	md.Rotation = normalizeRotation(ffprobeRotation(vstream))

	// --- Duration ---
	md.DurationSeconds = round3(ffFloat(res.Format.Duration))

	// --- Device (from container tags; ffprobe surfaces fewer vendor tags) ---
	md.DeviceName = firstNonEmpty(fmtTags["author"], fmtTags["artist"], fmtTags["encoder"])
	md.DeviceModel = firstNonEmpty(fmtTags["com.android.model"], fmtTags["model"])
	md.DeviceMake = firstNonEmpty(fmtTags["com.android.manufacturer"], fmtTags["make"])
	if md.DeviceMake == "" {
		if _, ok := fmtTags["com.samsung.android.utc_offset"]; ok {
			md.DeviceMake = "Samsung"
		} else if _, ok := fmtTags["com.android.version"]; ok {
			md.DeviceMake = "Android"
		}
	}

	// --- Capture time: UTC creation_time + the device's own offset tag ---
	resolveFFprobeCaptureTime(md, res, fmtTags)

	// --- GPS (Apple stores it as the ISO6709 com.apple.quicktime.location tag) ---
	resolveFFprobeGPS(md, fmtTags)
}

// resolveFFprobeCaptureTime reads creation_time (UTC, ISO8601) from the format
// tags (or the first stream as a fallback) and applies the Android/Samsung
// UTC-offset tag to derive the device-local capture time. Leaves the source
// empty if no creation_time is present (caller then falls back to mtime).
func resolveFFprobeCaptureTime(md *Metadata, res ffprobeResult, fmtTags map[string]string) {
	offset := parseDeviceOffset(firstNonEmpty(
		fmtTags["com.samsung.android.utc_offset"],
		fmtTags["com.android.utc_offset"],
	))

	ct := fmtTags["creation_time"]
	if ct == "" {
		// Fall back to the first stream's creation_time tag.
		for _, s := range res.Streams {
			if v := lowerKeys(s.Tags)["creation_time"]; v != "" {
				ct = v
				break
			}
		}
	}
	if ct == "" {
		return
	}

	utc, ok := parseISO8601UTC(ct)
	if !ok {
		md.Notes = append(md.Notes, "unparseable creation_time tag: "+ct)
		return
	}
	md.CaptureTimeUTC = utc.UTC().Format(time.RFC3339)
	if offset != "" {
		loc := time.FixedZone("dev", offsetSeconds(offset))
		md.CaptureTimeLocal = utc.In(loc).Format(time.RFC3339)
		md.UTCOffset = offset
	} else {
		md.CaptureTimeLocal = utc.UTC().Format(time.RFC3339)
		md.Notes = append(md.Notes,
			"no device UTC-offset tag; capture_time_local shown in UTC (true local timezone unknown)")
	}
	md.CaptureTimeSource = SourceFFprobe
}

// resolveFFprobeGPS parses the Apple ISO6709 location tag, e.g.
// "+37.7749-122.4194+010.000/", when present.
func resolveFFprobeGPS(md *Metadata, fmtTags map[string]string) {
	loc := firstNonEmpty(
		fmtTags["com.apple.quicktime.location.iso6709"],
		fmtTags["location"],
		fmtTags["location-eng"],
	)
	if loc == "" {
		return
	}
	lat, lon, alt, ok := parseISO6709(loc)
	if !ok || (lat == 0 && lon == 0) {
		return
	}
	g := &GPS{Present: true, Latitude: round6(lat), Longitude: round6(lon)}
	if alt != nil {
		a := round3(*alt)
		g.Altitude = &a
	}
	md.GPS = g
}

// ffprobeRotation reads display rotation from a video stream: the legacy
// tags.rotate value, or the Display Matrix side_data rotation (negated, since
// ffprobe reports the matrix rotation which is the inverse of display rotation).
func ffprobeRotation(s *ffStream) int {
	if s == nil {
		return 0
	}
	if r := lowerKeys(s.Tags)["rotate"]; r != "" {
		if v, err := strconv.Atoi(strings.TrimSpace(r)); err == nil {
			return v
		}
	}
	for _, sd := range s.SideDataList {
		if strings.EqualFold(sd.SideDataType, "Display Matrix") {
			if f, err := sd.Rotation.Float64(); err == nil {
				// Matrix rotation is the inverse of the applied display rotation.
				return int(-f)
			}
		}
	}
	return 0
}

// parseISO8601UTC parses ffprobe's "2025-07-04T23:14:40.000000Z" creation_time.
func parseISO8601UTC(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{
		"2006-01-02T15:04:05.000000Z",
		"2006-01-02T15:04:05.000000Z07:00",
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	// Last resort: treat a bare datetime as UTC.
	if t, err := time.ParseInLocation("2006-01-02T15:04:05", strings.TrimSuffix(s, "Z"), time.UTC); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// parseISO6709 decodes "+DD.DDDD+DDD.DDDD[+AAA.AAA]/" coordinate strings used by
// Apple's location tag. Returns lat, lon, optional altitude, ok.
func parseISO6709(s string) (float64, float64, *float64, bool) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "/"))
	// Split on sign boundaries while keeping the signs.
	var nums []string
	var cur strings.Builder
	for i, r := range s {
		if (r == '+' || r == '-') && i > 0 {
			nums = append(nums, cur.String())
			cur.Reset()
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		nums = append(nums, cur.String())
	}
	if len(nums) < 2 {
		return 0, 0, nil, false
	}
	lat, err1 := strconv.ParseFloat(nums[0], 64)
	lon, err2 := strconv.ParseFloat(nums[1], 64)
	if err1 != nil || err2 != nil {
		return 0, 0, nil, false
	}
	var alt *float64
	if len(nums) >= 3 {
		if a, err := strconv.ParseFloat(nums[2], 64); err == nil {
			alt = &a
		}
	}
	return lat, lon, alt, true
}

// simplifyContainer prefers the file extension (mp4/mov) over ffprobe's broad
// format_name list ("mov,mp4,m4a,3gp,3g2,mj2").
func simplifyContainer(formatName, path string) string {
	if i := strings.LastIndex(path, "."); i >= 0 && i < len(path)-1 {
		ext := strings.ToLower(path[i+1:])
		if len(ext) <= 5 {
			return ext
		}
	}
	return formatName
}

// lowerKeys returns a copy of m with lowercased keys (ffprobe tag casing varies).
func lowerKeys(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[strings.ToLower(k)] = v
	}
	return out
}

func ffFloat(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}
