// Package sidecar detects and parses the files yt-dlp (and similar downloaders)
// leave next to a video — subtitle tracks (.srt/.vtt/.json3) and metadata
// (.info.json / .live_chat.json) — so video ingestion can REUSE an already-made
// transcript + rich metadata instead of re-running ASR on a 500 GB library.
//
// This file holds subtitle parsing. The output is the SAME {text, segments[]}
// shape becky-transcribe emits (start/end seconds + text), so a sidecar
// transcript is a drop-in first-pass transcript for search/triage. YouTube
// auto-subs use a "rolling caption" style where consecutive cues overlap and
// repeat text; ParseSubtitle de-duplicates that into clean, monotonic segments.
package sidecar

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Segment mirrors one becky-transcribe segment: {start, end, text} in seconds.
type Segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// Subtitle is the parsed result of one subtitle sidecar.
type Subtitle struct {
	Path     string    // the sidecar file the segments came from
	Format   string    // "srt" | "vtt" | "json3"
	Text     string    // full transcript text (segments joined by space)
	Segments []Segment // caption-sized, de-duplicated, time-ordered
}

// subtitleExtPriority is the search order for a video's subtitle sidecar. English
// tracks first, then language-neutral, then word-level json3 last (json3 is rich
// but yt-dlp only writes it with --sub-format json3, so it is rarely present).
// Within the same stem, an exact ".en.srt" beats a bare ".srt", etc.
var subtitleExtPriority = []string{
	".en.srt", ".srt",
	".en.vtt", ".vtt",
	".en.json3", ".json3",
}

// FindSubtitle returns the best subtitle sidecar for videoPath, or "" if none.
// It looks for files in the same directory whose name is the video's basename
// (without extension) plus one of the known subtitle suffixes, honoring
// subtitleExtPriority. Matching is case-insensitive on the suffix so ".EN.SRT"
// is found too. A language variant like "<stem>.en-US.srt" is also accepted as a
// fallback when no exact-priority match exists.
func FindSubtitle(videoPath string) string {
	dir := filepath.Dir(videoPath)
	stem := stemOf(videoPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	// Build a name list for this directory once.
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	lowerStem := strings.ToLower(stem)

	// 1) Exact priority match (stem + known suffix).
	for _, suf := range subtitleExtPriority {
		want := lowerStem + suf
		for _, n := range names {
			if strings.ToLower(n) == want {
				return filepath.Join(dir, n)
			}
		}
	}
	// 2) Fallback: any "<stem>.<lang>.srt|vtt|json3" (e.g. en-US, en-orig).
	for _, ext := range []string{".srt", ".vtt", ".json3"} {
		for _, n := range names {
			ln := strings.ToLower(n)
			if strings.HasPrefix(ln, lowerStem+".") && strings.HasSuffix(ln, ext) {
				return filepath.Join(dir, n)
			}
		}
	}
	return ""
}

// ParseSubtitle parses a subtitle sidecar (.srt/.vtt/.json3) into a Subtitle.
// The format is chosen by extension; json3 carries word-level timing (collapsed
// to caption segments here for the becky-transcribe shape). Overlapping/rolling
// captions are de-duplicated so the text reads once, in order.
func ParseSubtitle(path string) (Subtitle, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Subtitle{}, fmt.Errorf("read subtitle %s: %w", path, err)
	}
	ext := strings.ToLower(filepath.Ext(path))
	// ".en.srt" -> ext is ".srt"; ".json3" handled directly.
	var (
		segs   []Segment
		format string
	)
	switch ext {
	case ".srt":
		segs = parseSRTVTT(string(data))
		format = "srt"
	case ".vtt":
		segs = parseSRTVTT(string(data))
		format = "vtt"
	case ".json3":
		segs = parseJSON3(data)
		format = "json3"
	default:
		return Subtitle{}, fmt.Errorf("unsupported subtitle format: %s", ext)
	}

	segs = dedupRolling(segs)
	return Subtitle{
		Path:     path,
		Format:   format,
		Text:     joinSegments(segs),
		Segments: segs,
	}, nil
}

// cueTimeRE matches an SRT (00:00:01,200) or VTT (00:00:01.200 / 00:01.200)
// "start --> end" timing line, tolerating optional VTT cue settings after end.
var cueTimeRE = regexp.MustCompile(
	`(\d{1,2}:\d{2}:\d{2}[.,]\d{1,3}|\d{1,2}:\d{2}[.,]\d{1,3})\s*-->\s*(\d{1,2}:\d{2}:\d{2}[.,]\d{1,3}|\d{1,2}:\d{2}[.,]\d{1,3})`)

// tagRE strips SRT/VTT inline markup (e.g. <c>, </c>, <00:00:01.000> word timing,
// {\an8} ASS positioning) so the text is clean.
var tagRE = regexp.MustCompile(`<[^>]*>|\{\\[^}]*\}`)

// parseSRTVTT parses both SRT and WebVTT into ordered raw segments (pre-dedup).
// It scans for "time --> time" lines and accumulates the following text lines
// until a blank line or the next timing line. Index numbers, "WEBVTT" headers,
// NOTE/STYLE/REGION blocks, and cue settings are ignored.
func parseSRTVTT(content string) []Segment {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := strings.Split(content, "\n")

	var segs []Segment
	i := 0
	for i < len(lines) {
		m := cueTimeRE.FindStringSubmatch(lines[i])
		if m == nil {
			i++
			continue
		}
		start := parseTimecode(m[1])
		end := parseTimecode(m[2])
		i++
		var textLines []string
		for i < len(lines) {
			line := lines[i]
			if strings.TrimSpace(line) == "" {
				i++
				break
			}
			if cueTimeRE.MatchString(line) {
				break // next cue without a blank separator
			}
			textLines = append(textLines, line)
			i++
		}
		text := cleanCaptionText(strings.Join(textLines, " "))
		if text != "" && end >= start {
			segs = append(segs, Segment{Start: start, End: end, Text: text})
		}
	}
	return segs
}

// cleanCaptionText strips inline tags, unescapes common HTML entities, and
// collapses whitespace.
func cleanCaptionText(s string) string {
	s = tagRE.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

// parseTimecode turns "HH:MM:SS,mmm" / "HH:MM:SS.mmm" / "MM:SS.mmm" into seconds.
func parseTimecode(tc string) float64 {
	tc = strings.Replace(tc, ",", ".", 1)
	parts := strings.Split(tc, ":")
	var h, m float64
	var sPart string
	switch len(parts) {
	case 3:
		h = atof(parts[0])
		m = atof(parts[1])
		sPart = parts[2]
	case 2:
		m = atof(parts[0])
		sPart = parts[1]
	default:
		return 0
	}
	sec := atof(sPart)
	return h*3600 + m*60 + sec
}

func atof(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

// dedupRolling collapses YouTube's rolling-caption TEXT overlap into clean
// segments WITHOUT ever altering the cue timings.
//
// Auto-subs emit each line twice: once as it "rolls in" (overlapping the prior
// cue's window) and once settled. Consecutive cues therefore share leading text
// and overlap in time. For each cue we drop any prefix words already emitted by
// the previous segment and keep only the genuinely new tail; a cue whose text is
// wholly a duplicate is dropped. The cue's ORIGINAL .srt start/end is preserved
// verbatim — we never clamp or interpolate the time. A search hit therefore seeks
// to exactly the timestamp the .srt lists (deterministic), and seeking to the cue
// start is always safe: you land at the beginning of the line, never past the
// quote. (Overlapping cue windows are fine for search, seek, and add_clip.)
func dedupRolling(segs []Segment) []Segment {
	if len(segs) == 0 {
		return []Segment{}
	}
	sort.SliceStable(segs, func(i, j int) bool { return segs[i].Start < segs[j].Start })

	out := make([]Segment, 0, len(segs))
	var prevWords []string
	for _, s := range segs {
		words := strings.Fields(s.Text)
		newWords := dropCommonPrefix(prevWords, words)
		text := strings.Join(newWords, " ")
		prevWords = words
		if text == "" {
			continue // wholly a duplicate of the previous cue: drop the line
		}
		// Keep the cue's literal .srt timing. End is only floored to Start as a
		// guard against a malformed (end < start) cue; the start is never moved.
		end := s.End
		if end < s.Start {
			end = s.Start
		}
		out = append(out, Segment{Start: round3(s.Start), End: round3(end), Text: text})
	}
	if len(out) == 0 {
		return []Segment{}
	}
	return out
}

// dropCommonPrefix returns cur with the longest leading run of words it shares
// (in order) with the tail of prev removed. This is what turns rolling captions
// ("we're doing" then "we're doing the live streams") into the incremental new
// text ("the live streams"). Comparison is case-insensitive on the word.
func dropCommonPrefix(prev, cur []string) []string {
	if len(prev) == 0 {
		return cur
	}
	maxK := len(cur)
	if len(prev) < maxK {
		maxK = len(prev)
	}
	for k := maxK; k > 0; k-- {
		if equalFold(prev[len(prev)-k:], cur[:k]) {
			return cur[k:]
		}
	}
	return cur
}

func equalFold(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}

func joinSegments(segs []Segment) string {
	parts := make([]string, 0, len(segs))
	for _, s := range segs {
		if s.Text != "" {
			parts = append(parts, s.Text)
		}
	}
	return strings.Join(parts, " ")
}

// stemOf returns the file name without its extension.
func stemOf(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func round3(f float64) float64 {
	return float64(int(f*1000+0.5)) / 1000
}
