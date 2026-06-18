// Package quotes is the deterministic engine behind becky-quotes: it reads a
// full transcript .srt, lets a pluggable Selector pick the passages that matter,
// recursively expands sentence context around each pick, snaps every region to
// VERBATIM cue timestamps copied from the source, merges overlaps, and emits a
// small <video-stem>_QUOTES.srt plus a JSON summary.
//
// This file owns the cue model and the source-of-truth parse. The hard rule it
// protects is timestamp identity (SPEC §2/§6): every start/end the tool emits
// must be the *byte-identical* timecode string of a real cue boundary in the
// input .srt — never rounded, never synthesized.
//
// Why a dedicated raw parser instead of internal/sidecar.ParseSubtitle:
// sidecar is built for YouTube auto-subs — it de-duplicates "rolling caption"
// overlap and rounds every boundary to 3 decimals (round3). That rounding would
// break the identity invariant, and the rolling-dedup would drop/rewrite cue
// text. A forensic Parakeet .srt is plain, non-overlapping SubRip, so we parse it
// verbatim here and KEEP the exact original timecode strings. internal/sidecar
// stays the reused parser for becky's *segment* shape (text + seconds) wherever
// rounding is acceptable; this engine needs the un-rounded source strings, so the
// timecode tokens are preserved exactly as written.
package quotes

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Cue is one SubRip cue with its timestamps kept BOTH as the verbatim source
// strings (StartRaw/EndRaw — emitted unchanged to guarantee identity) and as
// seconds (Start/End — used only for ordering, merge gaps, and duration caps).
type Cue struct {
	Index    int     // 1-based position in the source file (for provenance)
	Start    float64 // seconds, for math only
	End      float64 // seconds, for math only
	StartRaw string  // verbatim "HH:MM:SS,mmm" from the source — emitted as-is
	EndRaw   string  // verbatim "HH:MM:SS,mmm" from the source — emitted as-is
	Text     string  // cleaned spoken text of this cue
}

// cueTimingRE matches an SRT timing line, capturing the two verbatim timecode
// tokens. Tolerates "." or "," as the millisecond separator and an optional
// 1-2 digit hour. We capture the RAW token (including its original separator) so
// the emitted file is byte-identical to the source's boundaries.
var cueTimingRE = regexp.MustCompile(
	`(\d{1,2}:\d{2}:\d{2}[.,]\d{1,3})\s*-->\s*(\d{1,2}:\d{2}:\d{2}[.,]\d{1,3})`)

// tagStripRE removes SRT/VTT inline markup so matching text is clean. Mirrors
// internal/sidecar's tag handling.
var tagStripRE = regexp.MustCompile(`<[^>]*>|\{\\[^}]*\}`)

// ParseSRTFile reads an .srt from disk (read-only) and returns its cues with
// verbatim timecode strings preserved. A missing/short file is a normal error
// (degrade-never-crash); the caller turns it into a clear stderr note + nonzero
// exit, never a panic.
func ParseSRTFile(path string) ([]Cue, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read srt %s: %w", path, err)
	}
	cues := ParseSRT(string(data))
	if len(cues) == 0 {
		return nil, fmt.Errorf("no cues parsed from %s", path)
	}
	return cues, nil
}

// ParseSRT parses SubRip text into cues, keeping the verbatim "-->" timecode
// strings. Robust to BOM, CRLF, blank-line and index-number noise (same shape
// the Python prototype's parse_srt handled). Cue text is cleaned of inline tags
// and collapsed whitespace; the timecode strings are left untouched.
func ParseSRT(content string) []Cue {
	content = strings.TrimPrefix(content, "\ufeff") // strip UTF-8 BOM
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := strings.Split(content, "\n")

	cues := make([]Cue, 0, 256)
	i := 0
	for i < len(lines) {
		m := cueTimingRE.FindStringSubmatch(lines[i])
		if m == nil {
			i++
			continue
		}
		startRaw, endRaw := m[1], m[2]
		i++
		var textLines []string
		for i < len(lines) {
			line := lines[i]
			if strings.TrimSpace(line) == "" {
				i++
				break
			}
			if cueTimingRE.MatchString(line) {
				break // next cue without a blank separator
			}
			textLines = append(textLines, line)
			i++
		}
		text := cleanCueText(strings.Join(textLines, " "))
		cues = append(cues, Cue{
			Index:    len(cues) + 1,
			Start:    timecodeToSeconds(startRaw),
			End:      timecodeToSeconds(endRaw),
			StartRaw: startRaw,
			EndRaw:   endRaw,
			Text:     text,
		})
	}
	return cues
}

// cleanCueText strips inline markup, unescapes the common HTML entities, and
// collapses whitespace — the same normalization internal/sidecar applies to cue
// text, kept local so the timecode strings here stay verbatim.
func cleanCueText(s string) string {
	s = tagStripRE.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

// timecodeToSeconds converts "HH:MM:SS,mmm" / "HH:MM:SS.mmm" to seconds for the
// numeric (math-only) layer. The verbatim string is what gets emitted; this is
// only for ordering, merge-gap and duration-cap arithmetic.
func timecodeToSeconds(tc string) float64 {
	tc = strings.Replace(tc, ",", ".", 1)
	parts := strings.Split(tc, ":")
	if len(parts) != 3 {
		return 0
	}
	h := atof(parts[0])
	m := atof(parts[1])
	s := atof(parts[2])
	return h*3600 + m*60 + s
}

func atof(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}
