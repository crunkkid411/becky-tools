// Package exifmeta extracts forensic provenance metadata from a media file:
// capture device, the TRUE capture datetime (with timezone offset), GPS, display
// rotation, duration, resolution, and container/codec. It is evidence-grade: it
// NEVER silently trusts the filesystem mtime as the capture time. Every result
// records capture_time_source so a reviewer can tell whether the timestamp came
// from a real capture tag (exif / quicktime / ffprobe) or fell back to the
// untrusted file mtime.
//
// Source priority:
//  1. exiftool (if present on PATH or a known install) — richest tag coverage,
//     including vendor tags (Samsung/Apple) and the raw QuickTime UTC dates.
//  2. ffprobe -show_format -show_streams (always available) — container/stream
//     tags incl. creation_time and the Android/Samsung UTC-offset tag.
//
// The package only READS the file; it never writes to or modifies the source.
package exifmeta

import (
	"os"
	"os/exec"
	"time"
)

// CaptureTimeSource enumerates where the reported capture time came from. The
// "mtime(untrusted)" value is deliberately verbose so it is impossible to miss
// in a report: a file mtime is rewritten by copy / sync / cloud and must never
// be mistaken for a real capture timestamp.
const (
	SourceEXIF      = "exif"             // EXIF DateTimeOriginal / CreateDate (still-image style tags)
	SourceQuickTime = "quicktime"        // QuickTime/MP4 mvhd creation_time (+ vendor offset)
	SourceFFprobe   = "ffprobe"          // ffprobe container/stream creation_time tag
	SourceMTime     = "mtime(untrusted)" // filesystem modification time — NOT a capture time
)

// GPS holds decoded coordinates when the file carries them. Present is false
// when no GPS tag was found (the JSON then omits the whole gps block).
type GPS struct {
	Present   bool     `json:"-"`
	Latitude  float64  `json:"latitude"`
	Longitude float64  `json:"longitude"`
	Altitude  *float64 `json:"altitude_m,omitempty"` // metres, pointer so "0" vs "absent" is distinguishable
}

// Metadata is the first-class forensic metadata block surfaced by becky-osint.
// Every field is descriptive provenance, never interpretation. Fields are
// omitempty where absence is meaningful so the JSON stays honest about what the
// container actually carried.
type Metadata struct {
	// Provenance of this metadata pass.
	Source        string `json:"source"`         // "exiftool" | "ffprobe" | "ffprobe(exiftool-absent)"
	ExiftoolFound bool   `json:"exiftool_found"` // whether exiftool was available

	// Capture device.
	DeviceMake  string `json:"device_make,omitempty"`  // e.g. "Samsung"
	DeviceModel string `json:"device_model,omitempty"` // e.g. "SM-S938U1"
	DeviceName  string `json:"device_name,omitempty"`  // friendly name when present, e.g. "Galaxy S25 Ultra"

	// TRUE capture time. CaptureTimeLocal is the device-local wall-clock time
	// (RFC3339 with the device's offset applied). CaptureTimeUTC is the same
	// instant in UTC. UTCOffset is the device's timezone offset, e.g. "-05:00".
	CaptureTimeLocal  string `json:"capture_time_local,omitempty"`
	CaptureTimeUTC    string `json:"capture_time_utc,omitempty"`
	UTCOffset         string `json:"utc_offset,omitempty"`
	CaptureTimeSource string `json:"capture_time_source"` // ALWAYS set: exif|quicktime|ffprobe|mtime(untrusted)

	// File modification time, surfaced separately and always labelled untrusted
	// so it can be compared against the capture time but never confused with it.
	FileMTime string `json:"file_mtime_untrusted,omitempty"`

	// Geolocation (only present when the file carried GPS).
	GPS *GPS `json:"gps,omitempty"`

	// Media shape.
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
	Resolution      string  `json:"resolution,omitempty"`  // "WxH"
	Rotation        int     `json:"rotation"`              // display rotation in degrees (0/90/180/270)
	Container       string  `json:"container,omitempty"`   // e.g. "mp4", "mov,mp4,m4a,3gp,3g2,mj2"
	VideoCodec      string  `json:"video_codec,omitempty"` // e.g. "hevc", "h264"
	AudioCodec      string  `json:"audio_codec,omitempty"` // e.g. "aac"

	// Non-fatal notes (tags seen but not parsed, fallbacks taken, etc.).
	Notes []string `json:"notes,omitempty"`
}

// Extractor runs the external probe tools. Paths come from config so the package
// never hardcodes binary locations.
type Extractor struct {
	Exiftool string // path to exiftool ("" => auto-detect / disabled)
	FFprobe  string // path to ffprobe (required for the fallback path)
}

// NewExtractor builds an Extractor, auto-detecting exiftool from PATH and the
// common install locations on this machine when an explicit path is not given.
func NewExtractor(exiftool, ffprobe string) Extractor {
	if exiftool == "" {
		exiftool = detectExiftool()
	}
	return Extractor{Exiftool: exiftool, FFprobe: ffprobe}
}

// detectExiftool returns the exiftool binary to use, or "" if none is found.
func detectExiftool() string {
	if p, err := exec.LookPath("exiftool"); err == nil {
		return p
	}
	for _, c := range exiftoolFallbacks {
		if fileExists(c) {
			return c
		}
	}
	return ""
}

// exiftoolFallbacks are common install locations checked when exiftool is not on
// PATH. PATH is preferred; these are only a safety net.
var exiftoolFallbacks = []string{
	`C:\Users\only1\bin\exiftool.exe`,
	`C:\ProgramData\chocolatey\bin\exiftool.exe`,
	`C:\Windows\exiftool.exe`,
	`C:\tools\exiftool\exiftool.exe`,
}

// Extract returns forensic metadata for path. It prefers exiftool, falling back
// to ffprobe when exiftool is absent or fails. The filesystem mtime is always
// captured (as the labelled-untrusted fallback) so the capture time can never be
// silently missing. Returns an error only when no probe could run at all.
func (e Extractor) Extract(path string) (Metadata, error) {
	mtime := fileMTime(path)

	if e.Exiftool != "" {
		md, err := e.extractExiftool(path)
		if err == nil {
			md.ExiftoolFound = true
			finalize(&md, mtime)
			return md, nil
		}
		// exiftool present but failed (corrupt tags, unsupported container):
		// degrade to ffprobe rather than aborting the whole osint run.
		md2, ferr := e.extractFFprobe(path)
		if ferr != nil {
			return Metadata{}, err // surface the original exiftool error
		}
		md2.ExiftoolFound = true
		md2.Notes = append(md2.Notes, "exiftool failed ("+err.Error()+"); used ffprobe")
		finalize(&md2, mtime)
		return md2, nil
	}

	md, err := e.extractFFprobe(path)
	if err != nil {
		return Metadata{}, err
	}
	md.ExiftoolFound = false
	md.Source = "ffprobe(exiftool-absent)"
	md.Notes = append(md.Notes,
		"exiftool not found on PATH or common locations; vendor/EXIF tag coverage is reduced")
	finalize(&md, mtime)
	return md, nil
}

// finalize fills the always-untrusted mtime field and, when no capture tag was
// found, falls back to mtime with the explicit untrusted source label.
func finalize(md *Metadata, mtime time.Time) {
	if !mtime.IsZero() {
		md.FileMTime = mtime.Format(time.RFC3339)
	}
	if md.CaptureTimeSource == "" {
		if !mtime.IsZero() {
			md.CaptureTimeLocal = mtime.Local().Format(time.RFC3339)
			md.CaptureTimeUTC = mtime.UTC().Format(time.RFC3339)
		}
		md.CaptureTimeSource = SourceMTime
		md.Notes = append(md.Notes,
			"no capture tag found; capture_time falls back to file mtime which is UNTRUSTED "+
				"(rewritten by copy/sync/cloud)")
	}
}

// fileMTime returns the file modification time, or the zero time on error.
func fileMTime(path string) time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
