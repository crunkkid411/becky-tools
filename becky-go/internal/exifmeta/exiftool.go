// exiftool.go — the exiftool-backed extraction path. exiftool gives the richest
// tag coverage (vendor Samsung/Apple tags, the raw QuickTime UTC dates, GPS in
// decimal degrees via -n). We run it with -n (numeric, no human formatting) and
// -G1 (family-1 group prefixes) so QuickTime dates come back as raw UTC and we
// can apply the device's own timezone offset ourselves — never the analyst PC's.
package exifmeta

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// extractExiftool runs exiftool and maps its tag set onto Metadata.
func (e Extractor) extractExiftool(path string) (Metadata, error) {
	out, err := e.runExiftool(path)
	if err != nil {
		return Metadata{}, err
	}
	tags, err := parseExiftoolJSON(out)
	if err != nil {
		return Metadata{}, err
	}
	md := Metadata{Source: "exiftool"}
	mapExiftoolTags(&md, tags)
	return md, nil
}

// runExiftool invokes exiftool in machine-readable mode. -n disables human
// formatting (raw UTC QuickTime dates, decimal-degree GPS, numeric rotation);
// -G1 prefixes tags with their family-1 group (e.g. "QuickTime:CreateDate",
// "Samsung:SamsungModel") so vendor tags are unambiguous. We deliberately use
// QuickTimeUTC=0 (the default) so QuickTime dates come back RAW (the UTC value
// stored in the container) rather than being shifted to the ANALYST PC's local
// timezone — we apply the DEVICE's own offset ourselves to get true local time.
func (e Extractor) runExiftool(path string) ([]byte, error) {
	cmd := exec.Command(e.Exiftool,
		"-j",                     // JSON output
		"-n",                     // numeric / raw values
		"-G1",                    // family-1 group names
		"-api", "QuickTimeUTC=0", // raw QuickTime dates (no shift to PC tz)
		"-charset", "filename=UTF8",
		path,
	)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("exiftool: %v: %s", err, tail(errBuf.String()))
	}
	return out, nil
}

// parseExiftoolJSON unmarshals exiftool's "-j" output (a JSON array with one
// object) into a flat string-keyed tag map. Values may be numbers, strings, or
// arrays, so they are decoded as json.RawMessage and normalized on read.
func parseExiftoolJSON(out []byte) (map[string]json.RawMessage, error) {
	var arr []map[string]json.RawMessage
	if err := json.Unmarshal(out, &arr); err != nil {
		return nil, fmt.Errorf("parse exiftool json: %w", err)
	}
	if len(arr) == 0 {
		return nil, fmt.Errorf("exiftool returned no records")
	}
	return arr[0], nil
}

// mapExiftoolTags translates the raw exiftool tag map into Metadata. The keys
// carry family-1 group prefixes (from -G1); we look tags up by suffix so the
// same logic works whether a tag is grouped under QuickTime, Samsung, Keys, etc.
func mapExiftoolTags(md *Metadata, tags map[string]json.RawMessage) {
	get := func(suffixes ...string) string { return firstTagValue(tags, suffixes...) }

	// --- Device ---
	md.DeviceModel = firstNonEmpty(get("Model"), get("SamsungModel"))
	md.DeviceMake = firstNonEmpty(get("Make"), get("Manufacturer"))
	md.DeviceName = firstNonEmpty(get("Author"), get("DeviceName"), get("CreatorTool"), get("Software"))
	// Infer make from a vendor-prefixed model when Make is absent (Samsung MP4s
	// carry SM-xxxx under a Samsung group but no Make tag).
	if md.DeviceMake == "" {
		md.DeviceMake = inferMake(md.DeviceModel, tags)
	}

	// --- Capture time ---
	resolveCaptureTime(md, tags, get)

	// --- GPS ---
	resolveGPS(md, get)

	// --- Media shape ---
	md.DurationSeconds = round3(parseFloat(get("Duration", "MediaDuration", "TrackDuration")))
	md.Resolution = firstNonEmpty(
		composeResolution(get("ImageWidth"), get("ImageHeight")),
		composeResolution(get("SourceImageWidth"), get("SourceImageHeight")),
		normalizeImageSize(get("ImageSize")))
	md.Rotation = normalizeRotation(parseInt(get("Rotation")))
	md.Container = strings.ToLower(firstNonEmpty(get("FileTypeExtension"), get("FileType")))
	md.VideoCodec = mapCompressor(firstNonEmpty(get("CompressorID"), get("VideoCodec")))
	md.AudioCodec = mapAudioFormat(firstNonEmpty(get("AudioFormat"), get("AudioCodec")))
}

// resolveCaptureTime picks the TRUE capture time and records its source. EXIF
// DateTimeOriginal wins (still-image semantics); otherwise the QuickTime
// CreateDate (raw UTC) combined with the device's own timezone offset. Only when
// neither tag exists do we leave the source empty so the caller falls back to
// the untrusted mtime.
func resolveCaptureTime(md *Metadata, tags map[string]json.RawMessage, get func(...string) string) {
	offset := parseDeviceOffset(firstNonEmpty(
		get("SamsungAndroidUtcOffset"), // Samsung phones
		get("AndroidUtcOffset"),
		get("UtcOffset"),
		get("OffsetTimeOriginal"), // EXIF offset tags
		get("OffsetTime"),
	))

	// EXIF original timestamp (already local on most still cameras).
	if dto := get("DateTimeOriginal"); dto != "" {
		if applyEXIFLocal(md, dto, offset) {
			md.CaptureTimeSource = SourceEXIF
			return
		}
	}

	// QuickTime/MP4 mvhd creation time. With -n + QuickTimeUTC=1 this is raw UTC.
	if cd := firstNonEmpty(get("CreateDate"), get("MediaCreateDate"), get("TrackCreateDate")); cd != "" {
		if applyQuickTimeUTC(md, cd, offset) {
			md.CaptureTimeSource = SourceQuickTime
			return
		}
	}
}

// applyEXIFLocal interprets an EXIF "YYYY:MM:DD HH:MM:SS[±HH:MM]" timestamp,
// which is wall-clock local time. If the string lacks an offset we apply the
// device offset when known. Returns false if it cannot be parsed.
func applyEXIFLocal(md *Metadata, raw, offset string) bool {
	t, off, ok := parseEXIFDateTime(raw, offset)
	if !ok {
		return false
	}
	setCaptureTimes(md, t, off)
	return true
}

// applyQuickTimeUTC interprets a raw-UTC QuickTime date and applies the device
// offset to produce the device-local capture time. Returns false on parse fail.
func applyQuickTimeUTC(md *Metadata, raw, offset string) bool {
	utc, ok := parseUTCDateTime(raw)
	if !ok {
		return false
	}
	loc := time.FixedZone("dev", offsetSeconds(offset))
	local := utc.In(loc)
	md.CaptureTimeUTC = utc.UTC().Format(time.RFC3339)
	if offset != "" {
		md.CaptureTimeLocal = local.Format(time.RFC3339)
		md.UTCOffset = offset
	} else {
		// No device offset: report UTC as the local value but flag the gap.
		md.CaptureTimeLocal = utc.UTC().Format(time.RFC3339)
		md.Notes = append(md.Notes,
			"no device UTC-offset tag; capture_time_local shown in UTC (true local timezone unknown)")
	}
	return true
}

// setCaptureTimes fills both local and UTC capture fields from a parsed instant.
func setCaptureTimes(md *Metadata, t time.Time, offset string) {
	md.CaptureTimeLocal = t.Format(time.RFC3339)
	md.CaptureTimeUTC = t.UTC().Format(time.RFC3339)
	if offset != "" {
		md.UTCOffset = offset
	} else if name := t.Format("-07:00"); name != "" {
		md.UTCOffset = name
	}
}

// resolveGPS reads decimal-degree GPS (exiftool -n) and attaches a GPS block
// only when coordinates are present and non-zero.
func resolveGPS(md *Metadata, get func(...string) string) {
	lat, latOK := parseFloatOK(get("GPSLatitude"))
	lon, lonOK := parseFloatOK(get("GPSLongitude"))
	if !latOK || !lonOK {
		// Some containers expose a combined "GPSPosition" / "GPSCoordinates".
		if combined := firstNonEmpty(get("GPSPosition"), get("GPSCoordinates")); combined != "" {
			if a, b, ok := parseCombinedGPS(combined); ok {
				lat, lon, latOK, lonOK = a, b, true, true
			}
		}
	}
	if !latOK || !lonOK {
		return
	}
	if lat == 0 && lon == 0 {
		return // (0,0) is the null island; treat as no fix
	}
	gps := &GPS{Present: true, Latitude: round6(lat), Longitude: round6(lon)}
	if alt, ok := parseFloatOK(get("GPSAltitude")); ok {
		a := round3(alt)
		gps.Altitude = &a
	}
	md.GPS = gps
}

// --- small parse/format helpers ---

// firstTagValue looks up a tag by any of the given suffixes, matching the part
// after the family-1 group prefix (e.g. suffix "Model" matches "QuickTime:Model"
// or an ungrouped "Model"). It also matches ungrouped keys for robustness.
func firstTagValue(tags map[string]json.RawMessage, suffixes ...string) string {
	for _, suf := range suffixes {
		for k, v := range tags {
			name := k
			if i := strings.LastIndex(k, ":"); i >= 0 {
				name = k[i+1:]
			}
			if name == suf {
				if s := rawToString(v); s != "" {
					return s
				}
			}
		}
	}
	return ""
}

// rawToString normalizes a json.RawMessage tag value to a string: JSON strings
// unquote; numbers/bools render as-is; arrays take the first scalar element.
func rawToString(v json.RawMessage) string {
	s := strings.TrimSpace(string(v))
	if s == "" || s == "null" {
		return ""
	}
	if s[0] == '"' {
		var str string
		if json.Unmarshal(v, &str) == nil {
			return strings.TrimSpace(str)
		}
	}
	if s[0] == '[' {
		var arr []json.RawMessage
		if json.Unmarshal(v, &arr) == nil && len(arr) > 0 {
			return rawToString(arr[0])
		}
		return ""
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// inferMake derives a manufacturer from a model/tag set when no Make tag exists.
func inferMake(model string, tags map[string]json.RawMessage) string {
	for k := range tags {
		grp := k
		if i := strings.Index(k, ":"); i >= 0 {
			grp = k[:i]
		}
		switch grp {
		case "Samsung":
			return "Samsung"
		case "Apple":
			return "Apple"
		case "GoPro":
			return "GoPro"
		}
	}
	m := strings.ToUpper(model)
	switch {
	case strings.HasPrefix(m, "SM-"):
		return "Samsung"
	case strings.HasPrefix(m, "IPHONE"), strings.HasPrefix(m, "IPAD"):
		return "Apple"
	}
	return ""
}

// parseDeviceOffset normalizes a vendor UTC-offset tag ("-0500", "-05:00",
// "-5") into the canonical "±HH:MM" form. Returns "" if it cannot be parsed.
func parseDeviceOffset(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	sign := "+"
	if strings.HasPrefix(raw, "-") {
		sign = "-"
		raw = raw[1:]
	} else if strings.HasPrefix(raw, "+") {
		raw = raw[1:]
	}
	raw = strings.ReplaceAll(raw, ":", "")
	if raw == "" || !isAllDigits(raw) {
		return ""
	}
	var hh, mm int
	var err error
	switch {
	case len(raw) >= 4:
		hh, err = strconv.Atoi(raw[:2])
		if err != nil {
			return ""
		}
		mm, err = strconv.Atoi(raw[2:4])
		if err != nil {
			return ""
		}
	default:
		hh, err = strconv.Atoi(raw)
		if err != nil {
			return ""
		}
	}
	if hh > 14 || mm > 59 {
		return ""
	}
	return fmt.Sprintf("%s%02d:%02d", sign, hh, mm)
}

// isAllDigits reports whether s is non-empty and contains only ASCII digits.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// offsetSeconds converts a "±HH:MM" offset to seconds east of UTC.
func offsetSeconds(offset string) int {
	if offset == "" {
		return 0
	}
	sign := 1
	if offset[0] == '-' {
		sign = -1
	}
	body := strings.TrimLeft(offset, "+-")
	parts := strings.SplitN(body, ":", 2)
	hh, _ := strconv.Atoi(parts[0])
	mm := 0
	if len(parts) == 2 {
		mm, _ = strconv.Atoi(parts[1])
	}
	return sign * (hh*3600 + mm*60)
}

// parseUTCDateTime parses exiftool's raw "YYYY:MM:DD HH:MM:SS" (assumed UTC).
func parseUTCDateTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	for _, layout := range []string{"2006:01:02 15:04:05", "2006:01:02 15:04:05Z"} {
		if t, err := time.ParseInLocation(layout, strings.TrimSuffix(raw, "Z"), time.UTC); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// parseEXIFDateTime parses "YYYY:MM:DD HH:MM:SS[±HH:MM]" wall-clock time. When
// the string carries no offset, the supplied device offset (if any) is applied;
// otherwise it is treated as UTC and flagged by the caller. Returns the instant,
// the effective offset string, and ok.
func parseEXIFDateTime(raw, deviceOffset string) (time.Time, string, bool) {
	raw = strings.TrimSpace(raw)
	// Embedded offset, e.g. "2025:07:04 18:14:40-05:00".
	for _, layout := range []string{"2006:01:02 15:04:05-07:00", "2006:01:02 15:04:05Z07:00"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, t.Format("-07:00"), true
		}
	}
	// No embedded offset.
	if t, err := time.ParseInLocation("2006:01:02 15:04:05", raw, time.UTC); err == nil {
		if deviceOffset != "" {
			loc := time.FixedZone("dev", offsetSeconds(deviceOffset))
			lt := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, loc)
			return lt, deviceOffset, true
		}
		return t, "", true
	}
	return time.Time{}, "", false
}

// normalizeImageSize canonicalizes exiftool's ImageSize ("1280 720" with -n, or
// "1280x720" otherwise) into the "WxH" form. Returns "" if it cannot be parsed.
func normalizeImageSize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "x", " ")
	s = strings.ReplaceAll(s, "X", " ")
	fields := strings.Fields(s)
	if len(fields) != 2 {
		return ""
	}
	return composeResolution(fields[0], fields[1])
}

// composeResolution joins width/height tag values into "WxH" when both parse.
func composeResolution(w, h string) string {
	wi, wOK := parseIntOK(w)
	hi, hOK := parseIntOK(h)
	if !wOK || !hOK || wi <= 0 || hi <= 0 {
		return ""
	}
	return fmt.Sprintf("%dx%d", wi, hi)
}

// parseCombinedGPS splits "lat lon" / "lat, lon" forms into decimals.
func parseCombinedGPS(s string) (float64, float64, bool) {
	s = strings.ReplaceAll(s, ",", " ")
	fields := strings.Fields(s)
	if len(fields) < 2 {
		return 0, 0, false
	}
	lat, ok1 := parseFloatOK(fields[0])
	lon, ok2 := parseFloatOK(fields[1])
	return lat, lon, ok1 && ok2
}

// mapCompressor maps QuickTime CompressorID values to friendly codec names.
func mapCompressor(id string) string {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "hvc1", "hev1":
		return "hevc"
	case "avc1":
		return "h264"
	case "av01":
		return "av1"
	case "":
		return ""
	default:
		return strings.ToLower(id)
	}
}

// mapAudioFormat maps QuickTime AudioFormat fourccs to friendly codec names.
func mapAudioFormat(f string) string {
	switch strings.ToLower(strings.TrimSpace(f)) {
	case "mp4a":
		return "aac"
	case "":
		return ""
	default:
		return strings.ToLower(f)
	}
}

func parseFloat(vals ...string) float64 {
	for _, v := range vals {
		if f, ok := parseFloatOK(v); ok {
			return f
		}
	}
	return 0
}

func parseFloatOK(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	// exiftool sometimes appends units ("8.15 s"); take the leading number.
	fields := strings.Fields(s)
	if len(fields) > 0 {
		s = fields[0]
	}
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
}

func parseInt(s string) int {
	i, _ := parseIntOK(s)
	return i
}

func parseIntOK(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int(f), true
	}
	return 0, false
}

// normalizeRotation clamps a rotation to the 0/90/180/270 set.
func normalizeRotation(r int) int {
	r %= 360
	if r < 0 {
		r += 360
	}
	return r
}

func round3(f float64) float64 { return float64(int(f*1000+0.5)) / 1000 }
func round6(f float64) float64 {
	if f < 0 {
		return -round6(-f)
	}
	return float64(int(f*1e6+0.5)) / 1e6
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 600 {
		return s[len(s)-600:]
	}
	return s
}
