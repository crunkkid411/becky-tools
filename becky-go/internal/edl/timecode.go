package edl

import (
	"fmt"
	"math"
)

// SecondsToTimecode formats a position in seconds as SMPTE non-drop timecode
// "HH:MM:SS:FF" at the given frame rate. The frame field is the whole frame at
// that instant: frames = round(sec*fps), then split into H/M/S/F using the
// rounded integer fps (so 29.97 → 30 frames-per-second labelling, matching
// ffmpeg drawtext's timecode behaviour and CMX3600 non-drop convention).
//
// This is the verification anchor for the forensic lower-third: a clip whose In
// is t seconds gets a burned "ORIG TC" starting at SecondsToTimecode(In, fps),
// which the detective can scrub to in the ORIGINAL file (R-CUT §4a).
//
// fps <= 0 falls back to DefaultFPS so the function never divides by zero.
// Negative seconds are clamped to zero (timecode has no negative form here).
func SecondsToTimecode(sec, fps float64) string {
	if fps <= 0 {
		fps = DefaultFPS
	}
	if sec < 0 {
		sec = 0
	}
	// Integer frames-per-second for the FF field (non-drop labelling).
	ifps := int(math.Round(fps))
	if ifps <= 0 {
		ifps = int(math.Round(DefaultFPS))
	}
	totalFrames := int64(math.Round(sec * float64(ifps)))

	frames := totalFrames % int64(ifps)
	totalSecs := totalFrames / int64(ifps)
	secs := totalSecs % 60
	totalMins := totalSecs / 60
	mins := totalMins % 60
	hours := totalMins / 60

	return fmt.Sprintf("%02d:%02d:%02d:%02d", hours, mins, secs, frames)
}

// secondsToSRTTime formats a position in seconds as an SRT timestamp
// "HH:MM:SS,mmm" (millisecond precision, comma separator). Used by WriteSRT for
// the re-based compilation timeline. Negative input is clamped to zero.
func secondsToSRTTime(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	totalMillis := int64(math.Round(sec * 1000))
	millis := totalMillis % 1000
	totalSecs := totalMillis / 1000
	secs := totalSecs % 60
	totalMins := totalSecs / 60
	mins := totalMins % 60
	hours := totalMins / 60
	return fmt.Sprintf("%02d:%02d:%02d,%03d", hours, mins, secs, millis)
}
