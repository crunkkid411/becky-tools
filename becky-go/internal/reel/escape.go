package reel

import (
	"math"
	"strconv"
	"strings"
)

// ntscRationals maps the drop-frame NTSC family to its EXACT rational string.
// A container/edit rate that lands within ntscTolerance of one of these is the
// SAME rate edl.NormalizeRate already snapped it to (30000/1001, not the
// rounded 2997/100 = 29.970000) — formatRate must emit that exact fraction, not
// a 3-decimal truncation of it, or it reintroduces the very container-vs-edit
// grid mismatch NormalizeRate exists to remove. Measured: emitting "29.970"
// made a real render's own r_frame_rate come back as 2997/100 instead of
// 30000/1001.
var ntscRationals = []struct {
	val      float64
	fraction string
}{
	{24000.0 / 1001.0, "24000/1001"},
	{30000.0 / 1001.0, "30000/1001"},
	{60000.0 / 1001.0, "60000/1001"},
	{120000.0 / 1001.0, "120000/1001"},
}

const ntscTolerance = 0.001

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

// formatRate renders a frame rate for ffmpeg options that accept a <rational>/
// <video_rate> (timecode_rate, fps=). Whole rates print as integers ("30"); the
// NTSC family prints as its EXACT fraction ("30000/1001") — both option types are
// AVRational-parsed (av_parse_video_rate), so ffmpeg reads "30000/1001" as
// precisely as it reads "30". Any other fractional rate keeps up to 3 decimals
// trimmed of trailing zeros ("24.5").
func formatRate(fps float64) string {
	if fps == float64(int64(fps)) {
		return strconv.FormatInt(int64(fps), 10)
	}
	for _, ntsc := range ntscRationals {
		if math.Abs(fps-ntsc.val) < ntscTolerance {
			return ntsc.fraction
		}
	}
	s := strconv.FormatFloat(fps, 'f', 3, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}

// itoa is a tiny int->string helper for filter coordinate building.
func itoa(n int) string { return strconv.Itoa(n) }
