// filename.go — a deterministic, offline parser for date tokens burned into a
// media file's BASENAME by a phone or screen recorder. These naming conventions
// (Samsung "20250704_181431", Apple "IMG_20240301", macOS "Screen Recording
// 2025-07-04 at ...", "2025-07-04 19.14.31") are written at capture/save time and
// usually survive a copy, so a filename token is a MID-trust signal: stronger
// than the rewritten mtime, weaker than a real container capture tag.
//
// The basename MUST be derived with pathx.Base (both '/' and '\' as separators)
// before calling here — a corpus path may be a Windows C:\... path even on Linux.
package datetri

import (
	"regexp"
	"strconv"
	"time"
)

// FilenameDate is the result of parsing a basename: the calendar date, an
// optional wall-clock time, and whether the time portion was present/precise.
type FilenameDate struct {
	Time    time.Time // local-zone date (and time when Precise)
	Precise bool      // true when HHMMSS was present in the token
	Raw     string    // the matched token text, for display
}

var (
	// dateTimeCompact: YYYYMMDD optionally followed by a separator and HHMMSS.
	// Matches "20250704_181431", "20250704-181431", "20250704181431", "20250704".
	dateTimeCompactRe = regexp.MustCompile(`(?:^|[^0-9])(\d{4})(\d{2})(\d{2})(?:[ _.\-T]?(\d{2})(\d{2})(\d{2}))?(?:$|[^0-9])`)

	// dashedDate: YYYY-MM-DD or YYYY.MM.DD, optionally followed by a time using
	// '.' or ':' as the time separator (macOS uses "19.14.31"). Matches
	// "2025-07-04 19.14.31", "2025-07-04", "2025.07.04", "2025-07-04 at 9.01.05".
	dashedDateRe = regexp.MustCompile(`(?:^|[^0-9])(\d{4})[.\-](\d{2})[.\-](\d{2})(?:(?:\s+at\s+|[ _T])(\d{1,2})[.:](\d{2})(?:[.:](\d{2}))?)?`)
)

// minYear / maxYearOffset bound a plausible capture date: reject obviously-wrong
// tokens (a part number "20259999", a year before consumer video) so a junk
// match never becomes a signal.
const minYear = 1990

// ParseFilenameDate extracts a date token from a basename and returns it with ok.
// It returns ok=false when no plausible date token is present. The basename
// should already be pathx.Base'd. Implausible dates (year < 1990, year > now+1,
// month/day out of range) are rejected.
func ParseFilenameDate(base string) (FilenameDate, bool) {
	if d, ok := parseDashed(base); ok {
		return d, true
	}
	if d, ok := parseCompact(base); ok {
		return d, true
	}
	return FilenameDate{}, false
}

func parseCompact(base string) (FilenameDate, bool) {
	m := dateTimeCompactRe.FindStringSubmatch(base)
	if m == nil {
		return FilenameDate{}, false
	}
	y, mo, d := atoi(m[1]), atoi(m[2]), atoi(m[3])
	if !plausible(y, mo, d) {
		return FilenameDate{}, false
	}
	precise := m[4] != ""
	hh, mm, ss := atoi(m[4]), atoi(m[5]), atoi(m[6])
	if precise && !plausibleClock(hh, mm, ss) {
		precise = false
		hh, mm, ss = 0, 0, 0
	}
	return FilenameDate{
		Time:    time.Date(y, time.Month(mo), d, hh, mm, ss, 0, time.Local),
		Precise: precise,
		Raw:     trimToken(m[0]),
	}, true
}

func parseDashed(base string) (FilenameDate, bool) {
	m := dashedDateRe.FindStringSubmatch(base)
	if m == nil {
		return FilenameDate{}, false
	}
	y, mo, d := atoi(m[1]), atoi(m[2]), atoi(m[3])
	if !plausible(y, mo, d) {
		return FilenameDate{}, false
	}
	precise := m[4] != ""
	hh, mm, ss := atoi(m[4]), atoi(m[5]), atoi(m[6])
	if precise && !plausibleClock(hh, mm, ss) {
		precise = false
		hh, mm, ss = 0, 0, 0
	}
	return FilenameDate{
		Time:    time.Date(y, time.Month(mo), d, hh, mm, ss, 0, time.Local),
		Precise: precise,
		Raw:     trimToken(m[0]),
	}, true
}

// plausible rejects an implausible calendar date so junk numeric runs in a
// filename (part numbers, ids) are not mistaken for dates.
func plausible(y, mo, d int) bool {
	if y < minYear || y > time.Now().Year()+1 {
		return false
	}
	if mo < 1 || mo > 12 || d < 1 || d > 31 {
		return false
	}
	// Reject impossible day-of-month (e.g. 2025-02-30) by round-tripping.
	t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
	return t.Year() == y && int(t.Month()) == mo && t.Day() == d
}

func plausibleClock(hh, mm, ss int) bool {
	return hh >= 0 && hh < 24 && mm >= 0 && mm < 60 && ss >= 0 && ss < 60
}

func atoi(s string) int {
	if s == "" {
		return 0
	}
	n, _ := strconv.Atoi(s)
	return n
}

// trimToken strips the leading/trailing non-digit boundary chars the regex may
// have captured, leaving the date token itself for display.
func trimToken(s string) string {
	start, end := 0, len(s)
	for start < end && !isDigit(s[start]) {
		start++
	}
	for end > start && !isDigit(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
