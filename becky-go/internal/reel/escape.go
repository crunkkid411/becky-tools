package reel

import (
	"strconv"
	"strings"
)

// escape.go centralizes the ffmpeg filtergraph quoting that the lower-third
// burn needs on Windows. The rules follow R-CUT §4a (proven live) and mirror
// the technique in cmd/export/escapeSubsPath (forward-slash path + escaped
// colon), which we cannot import because it lives in package main.

// escapeFontPath converts a font path into the form ffmpeg's filtergraph
// expects: forward slashes with the drive-letter colon escaped, wrapped in
// single quotes — e.g. C:\Windows\Fonts\consola.ttf -> 'C\:/Windows/Fonts/consola.ttf'.
func escapeFontPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.ReplaceAll(p, ":", "\\:")
	return "'" + p + "'"
}

// escapeColons escapes ':' for use inside a single-quoted filter option value
// (e.g. the timecode 'HH\:MM\:SS\:FF'). The colon is a key/value separator in
// the filtergraph, so it must be backslash-escaped even inside quotes.
func escapeColons(s string) string {
	return strings.ReplaceAll(s, ":", "\\:")
}

// escapeDrawtextText escapes the characters that are special inside a
// single-quoted drawtext text= value: backslash, single quote, percent, and
// colon. Newlines/tabs are flattened upstream (edl.oneLine / the engine), so we
// only guard the inline-special set here. This keeps a quote text or metadata
// line from breaking the filtergraph.
func escapeDrawtextText(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`'`, `\'`,
		`%`, `\%`,
		`:`, `\:`,
	)
	return r.Replace(s)
}

// formatRate renders a frame rate for ffmpeg options (timecode_rate, fps).
// Whole rates print as integers ("30"); fractional rates keep up to 3 decimals
// trimmed of trailing zeros ("29.97").
func formatRate(fps float64) string {
	if fps == float64(int64(fps)) {
		return strconv.FormatInt(int64(fps), 10)
	}
	s := strconv.FormatFloat(fps, 'f', 3, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}

// itoa is a tiny int->string helper for filter coordinate building.
func itoa(n int) string { return strconv.Itoa(n) }
