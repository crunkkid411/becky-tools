package subs

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Style is the burned-in caption look. The defaults are the exact style cli-cut
// shipped and rendered with (helpers/render.py: _sub_force_style): white fill,
// black outline, no shadow, centred, lifted off the bottom edge.
//
// It is expressed as an ffmpeg `subtitles=...:force_style=` string rather than a
// styled .ass file, because force_style restyles a plain .srt in place — no ASS
// writer, no second file format to keep in sync.
type Style struct {
	FontName string // libass matches the font's FAMILY name, not its filename
	FontSize int
	Bold     int // 0/1 — ProximaNova-Semibold already carries the weight
	MarginV  int // distance above the bottom edge; per-creator constant
	Outline  int
}

// DefaultStyle is the shipped cli-cut caption style.
func DefaultStyle() Style {
	return Style{
		FontName: "ProximaNova-Semibold",
		FontSize: 12,
		Bold:     0,
		MarginV:  90,
		Outline:  2,
	}
}

// ForceStyle renders the style as ffmpeg's force_style argument value.
//
// Colours are ASS &HAABBGGRR: PrimaryColour &H00FFFFFF is opaque white fill,
// OutlineColour &H00000000 is opaque black. BorderStyle=1 + Outline=N is a true
// outline (BorderStyle=3 would be an opaque box instead). Alignment=2 is
// bottom-centre.
func (s Style) ForceStyle() string {
	return fmt.Sprintf(
		"FontName=%s,FontSize=%d,Bold=%d,"+
			"PrimaryColour=&H00FFFFFF,OutlineColour=&H00000000,BackColour=&H00000000,"+
			"BorderStyle=1,Outline=%d,Shadow=0,"+
			"Alignment=2,MarginV=%d",
		s.FontName, s.FontSize, s.Bold, s.Outline, s.MarginV,
	)
}

// SubtitlesFilter builds the complete ffmpeg -vf value that burns srtPath with
// this style. The subtitles filter parses its own argument string, so a Windows
// path needs forward slashes with the drive colon escaped, and the whole value
// is single-quoted.
func (s Style) SubtitlesFilter(srtPath string) string {
	return fmt.Sprintf("subtitles=%s:force_style='%s'", EscapeFilterPath(srtPath), s.ForceStyle())
}

// EscapeFilterPath converts a path into the form ffmpeg's subtitles filter
// expects inside a filter graph: forward slashes, escaped drive colon, quoted.
func EscapeFilterPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.ReplaceAll(p, ":", "\\:")
	p = strings.ReplaceAll(p, "'", "\\'")
	return "'" + p + "'"
}
