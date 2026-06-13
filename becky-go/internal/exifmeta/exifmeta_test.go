package exifmeta

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// --- exiftool tag mapping: the real Samsung Galaxy S25 Ultra clip shape ---

func TestMapExiftoolTags_SamsungQuickTimeUTC(t *testing.T) {
	// Synthetic copy of the family-1 (-G1) numeric (-n) exiftool output for a
	// Samsung MP4: raw-UTC QuickTime CreateDate + the device's own UTC offset.
	raw := `[{
		"QuickTime:CreateDate": "2025:07:04 23:14:40",
		"QuickTime:Duration": 8.15,
		"Samsung:SamsungModel": "SM-S938U1",
		"UserData:Author": "Galaxy S25 Ultra",
		"Keys:SamsungAndroidUtcOffset": "-0500",
		"Composite:Rotation": 90,
		"Composite:ImageSize": "1280x720",
		"Track1:CompressorID": "hvc1",
		"Track2:AudioFormat": "mp4a",
		"File:FileTypeExtension": "mp4"
	}]`
	tags, err := parseExiftoolJSON([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var md Metadata
	mapExiftoolTags(&md, tags)

	if md.DeviceModel != "SM-S938U1" {
		t.Errorf("device_model = %q, want SM-S938U1", md.DeviceModel)
	}
	if md.DeviceMake != "Samsung" {
		t.Errorf("device_make = %q, want Samsung (inferred)", md.DeviceMake)
	}
	if md.DeviceName != "Galaxy S25 Ultra" {
		t.Errorf("device_name = %q, want Galaxy S25 Ultra", md.DeviceName)
	}
	// 23:14:40 UTC at -05:00 => 18:14:40 local.
	if md.CaptureTimeLocal != "2025-07-04T18:14:40-05:00" {
		t.Errorf("capture_time_local = %q, want 2025-07-04T18:14:40-05:00", md.CaptureTimeLocal)
	}
	if md.CaptureTimeUTC != "2025-07-04T23:14:40Z" {
		t.Errorf("capture_time_utc = %q, want 2025-07-04T23:14:40Z", md.CaptureTimeUTC)
	}
	if md.UTCOffset != "-05:00" {
		t.Errorf("utc_offset = %q, want -05:00", md.UTCOffset)
	}
	if md.CaptureTimeSource != SourceQuickTime {
		t.Errorf("capture_time_source = %q, want %q", md.CaptureTimeSource, SourceQuickTime)
	}
	if md.Rotation != 90 {
		t.Errorf("rotation = %d, want 90", md.Rotation)
	}
	if md.Resolution != "1280x720" {
		t.Errorf("resolution = %q, want 1280x720", md.Resolution)
	}
	if md.VideoCodec != "hevc" {
		t.Errorf("video_codec = %q, want hevc", md.VideoCodec)
	}
	if md.AudioCodec != "aac" {
		t.Errorf("audio_codec = %q, want aac", md.AudioCodec)
	}
	if md.Container != "mp4" {
		t.Errorf("container = %q, want mp4", md.Container)
	}
	if md.GPS != nil {
		t.Errorf("gps should be nil for a clip with no GPS tags, got %+v", md.GPS)
	}
	if md.DurationSeconds != 8.15 {
		t.Errorf("duration = %v, want 8.15", md.DurationSeconds)
	}
}

func TestMapExiftoolTags_EXIFWithEmbeddedOffsetAndGPS(t *testing.T) {
	raw := `[{
		"ExifIFD:DateTimeOriginal": "2024:01:02 09:30:00-08:00",
		"IFD0:Make": "Apple",
		"IFD0:Model": "iPhone 15 Pro",
		"GPS:GPSLatitude": 37.774929,
		"GPS:GPSLongitude": -122.419418,
		"GPS:GPSAltitude": 12.5
	}]`
	tags, err := parseExiftoolJSON([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var md Metadata
	mapExiftoolTags(&md, tags)

	if md.CaptureTimeSource != SourceEXIF {
		t.Errorf("source = %q, want exif", md.CaptureTimeSource)
	}
	if md.CaptureTimeLocal != "2024-01-02T09:30:00-08:00" {
		t.Errorf("local = %q, want 2024-01-02T09:30:00-08:00", md.CaptureTimeLocal)
	}
	if md.CaptureTimeUTC != "2024-01-02T17:30:00Z" {
		t.Errorf("utc = %q, want 2024-01-02T17:30:00Z", md.CaptureTimeUTC)
	}
	if md.GPS == nil {
		t.Fatalf("gps should be present")
	}
	if md.GPS.Latitude != 37.774929 || md.GPS.Longitude != -122.419418 {
		t.Errorf("gps = %v,%v want 37.774929,-122.419418", md.GPS.Latitude, md.GPS.Longitude)
	}
	if md.GPS.Altitude == nil || *md.GPS.Altitude != 12.5 {
		t.Errorf("gps altitude = %v, want 12.5", md.GPS.Altitude)
	}
}

// --- ffprobe fallback: Samsung container tags ---

func TestMapFFprobe_SamsungCreationTimeAndOffset(t *testing.T) {
	raw := `{
		"streams": [
			{"codec_type":"video","codec_name":"hevc","width":1280,"height":720,
			 "tags":{"rotate":"90","creation_time":"2025-07-04T23:14:40.000000Z"},
			 "side_data_list":[{"side_data_type":"Display Matrix","rotation":-90}]},
			{"codec_type":"audio","codec_name":"aac"}
		],
		"format": {
			"format_name":"mov,mp4,m4a,3gp,3g2,mj2",
			"duration":"8.149200",
			"tags":{
				"creation_time":"2025-07-04T23:14:40.000000Z",
				"com.samsung.android.utc_offset":"-0500",
				"com.android.version":"15"
			}
		}
	}`
	var res ffprobeResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var md Metadata
	mapFFprobe(&md, res, "20250704_181431.mp4")

	if md.CaptureTimeSource != SourceFFprobe {
		t.Errorf("source = %q, want ffprobe", md.CaptureTimeSource)
	}
	if md.CaptureTimeLocal != "2025-07-04T18:14:40-05:00" {
		t.Errorf("local = %q, want 2025-07-04T18:14:40-05:00", md.CaptureTimeLocal)
	}
	if md.UTCOffset != "-05:00" {
		t.Errorf("offset = %q, want -05:00", md.UTCOffset)
	}
	if md.DeviceMake != "Samsung" {
		t.Errorf("make = %q, want Samsung (inferred from utc_offset tag)", md.DeviceMake)
	}
	if md.Rotation != 90 {
		t.Errorf("rotation = %d, want 90", md.Rotation)
	}
	if md.VideoCodec != "hevc" || md.AudioCodec != "aac" {
		t.Errorf("codecs = %q/%q, want hevc/aac", md.VideoCodec, md.AudioCodec)
	}
	if md.Resolution != "1280x720" {
		t.Errorf("resolution = %q, want 1280x720", md.Resolution)
	}
	if md.Container != "mp4" {
		t.Errorf("container = %q, want mp4 (from extension)", md.Container)
	}
}

func TestMapFFprobe_AppleISO6709GPS(t *testing.T) {
	raw := `{
		"streams": [{"codec_type":"video","codec_name":"h264","width":1920,"height":1080,
			"side_data_list":[{"side_data_type":"Display Matrix","rotation":-180}]}],
		"format": {
			"format_name":"mov,mp4,m4a,3gp,3g2,mj2",
			"duration":"3.0",
			"tags":{
				"creation_time":"2025-03-01T14:00:00.000000Z",
				"com.apple.quicktime.location.ISO6709":"+37.7749-122.4194+010.000/"
			}
		}
	}`
	var res ffprobeResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var md Metadata
	mapFFprobe(&md, res, "clip.mov")

	if md.GPS == nil {
		t.Fatalf("gps should be parsed from ISO6709 tag")
	}
	if md.GPS.Latitude != 37.7749 || md.GPS.Longitude != -122.4194 {
		t.Errorf("gps = %v,%v want 37.7749,-122.4194", md.GPS.Latitude, md.GPS.Longitude)
	}
	if md.GPS.Altitude == nil || *md.GPS.Altitude != 10.0 {
		t.Errorf("altitude = %v, want 10.0", md.GPS.Altitude)
	}
	if md.Rotation != 180 {
		t.Errorf("rotation = %d, want 180 (negated display matrix)", md.Rotation)
	}
	// No device offset tag -> local shown in UTC, with a note.
	if md.CaptureTimeLocal != "2025-03-01T14:00:00Z" {
		t.Errorf("local = %q, want UTC fallback", md.CaptureTimeLocal)
	}
}

// --- offset / coordinate parsing units ---

func TestParseDeviceOffset(t *testing.T) {
	cases := map[string]string{
		"-0500":   "-05:00",
		"-05:00":  "-05:00",
		"+0530":   "+05:30",
		"+0":      "+00:00",
		"-8":      "-08:00",
		"":        "",
		"garbage": "",
	}
	for in, want := range cases {
		if got := parseDeviceOffset(in); got != want {
			t.Errorf("parseDeviceOffset(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseISO6709(t *testing.T) {
	lat, lon, alt, ok := parseISO6709("+37.7749-122.4194+010.000/")
	if !ok {
		t.Fatalf("expected ok")
	}
	if lat != 37.7749 || lon != -122.4194 {
		t.Errorf("lat/lon = %v/%v", lat, lon)
	}
	if alt == nil || *alt != 10.0 {
		t.Errorf("alt = %v, want 10.0", alt)
	}
	if _, _, _, ok := parseISO6709(""); ok {
		t.Errorf("empty string should not parse")
	}
}

func TestNormalizeRotation(t *testing.T) {
	cases := map[int]int{0: 0, 90: 90, -90: 270, 270: 270, 450: 90, -180: 180}
	for in, want := range cases {
		if got := normalizeRotation(in); got != want {
			t.Errorf("normalizeRotation(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestParseUTCDateTime(t *testing.T) {
	t1, ok := parseUTCDateTime("2025:07:04 23:14:40")
	if !ok {
		t.Fatalf("expected parse ok")
	}
	if t1.UTC().Format("2006-01-02T15:04:05Z") != "2025-07-04T23:14:40Z" {
		t.Errorf("got %v", t1.UTC())
	}
}

// --- finalize: the untrusted-mtime fallback must never be silent ---

func TestFinalize_FallsBackToMTimeWithLabel(t *testing.T) {
	md := Metadata{Source: "ffprobe"} // no capture tag found
	mtime := mustTime("2026-01-15T10:00:00Z")
	finalize(&md, mtime)

	if md.CaptureTimeSource != SourceMTime {
		t.Errorf("source = %q, want %q", md.CaptureTimeSource, SourceMTime)
	}
	if md.FileMTime == "" {
		t.Errorf("file_mtime_untrusted must be set")
	}
	if md.CaptureTimeUTC != "2026-01-15T10:00:00Z" {
		t.Errorf("utc = %q", md.CaptureTimeUTC)
	}
	found := false
	for _, n := range md.Notes {
		if strings.Contains(n, "UNTRUSTED") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an UNTRUSTED note in %v", md.Notes)
	}
}

func TestFinalize_KeepsRealCaptureTag(t *testing.T) {
	md := Metadata{Source: "exiftool", CaptureTimeSource: SourceQuickTime,
		CaptureTimeLocal: "2025-07-04T18:14:40-05:00"}
	finalize(&md, mustTime("2026-01-15T10:00:00Z"))
	if md.CaptureTimeSource != SourceQuickTime {
		t.Errorf("source overwritten to %q", md.CaptureTimeSource)
	}
	if md.FileMTime == "" {
		t.Errorf("file_mtime_untrusted should still be recorded for comparison")
	}
}

func mustTime(rfc string) time.Time {
	t, err := time.Parse(time.RFC3339, rfc)
	if err != nil {
		panic(err)
	}
	return t
}
