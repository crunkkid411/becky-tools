// ocr.go — the optional burned-in-timestamp signal (Signal C). becky-dates does
// NOT run the OCR model; it consumes an already-produced becky-ocr ocr.json. The
// TimestampSource seam keeps that boundary clean and optional: leave it unset and
// signal C is simply absent (the tool degrades, exit 0). The default impl, which
// reads an ocr.json file, lives in the cmd layer; here we provide the interface,
// the candidate type, and the deterministic date-text parser they share.
package datetri

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// OCRDateCandidate is one burned-in date/clock read from a frame.
type OCRDateCandidate struct {
	Text           string  // the OCR'd line text, e.g. "07/04/2025 6:14 PM"
	Confidence     float64 // OCR read confidence (0..1)
	FrameTimestamp float64 // clip-relative time (seconds) of the source frame
}

// TimestampSource yields burned-in date/clock candidates for a clip. The default
// impl reads a becky-ocr ocr.json file (pure-Go, cloud-testable). It can be left
// unset → signal C is simply absent (degrade, exit 0).
type TimestampSource interface {
	BurnedInDates(sourceFile string) []OCRDateCandidate
}

var (
	// ocrMDYRe: a M/D/Y or M-D-Y or M.D.Y date as a camera/stream overlay burns
	// it in (US convention). 1-2 digit month/day, 2 or 4 digit year.
	ocrMDYRe = regexp.MustCompile(`\b(\d{1,2})[/.\-](\d{1,2})[/.\-](\d{2,4})\b`)
	// ocrYMDRe: an ISO-ish Y-M-D overlay.
	ocrYMDRe = regexp.MustCompile(`\b(\d{4})[/.\-](\d{1,2})[/.\-](\d{1,2})\b`)
	// ocrClockRe: an HH:MM(:SS) clock with optional AM/PM, parsed for the precise
	// wall-clock when a date is also present.
	ocrClockRe = regexp.MustCompile(`(?i)\b(\d{1,2}):(\d{2})(?::(\d{2}))?\s*(am|pm)?\b`)
)

// ParseOCRDate parses a burned-in date string into a time + precision. It tries
// ISO Y-M-D first (unambiguous), then US M/D/Y. The clock, if present, is folded
// in and Precise is set. Returns ok=false when no plausible date is present.
func ParseOCRDate(text string) (t time.Time, precise bool, ok bool) {
	s := strings.TrimSpace(text)
	if s == "" {
		return time.Time{}, false, false
	}

	var y, mo, d int
	if m := ocrYMDRe.FindStringSubmatch(s); m != nil {
		y, mo, d = atoi(m[1]), atoi(m[2]), atoi(m[3])
	} else if m := ocrMDYRe.FindStringSubmatch(s); m != nil {
		mo, d, y = atoi(m[1]), atoi(m[2]), normYear(m[3])
	} else {
		return time.Time{}, false, false
	}

	if !plausible(y, mo, d) {
		return time.Time{}, false, false
	}

	hh, mm, ss := 0, 0, 0
	if cm := ocrClockRe.FindStringSubmatch(s); cm != nil {
		hh, mm, ss = atoi(cm[1]), atoi(cm[2]), atoi(cm[3])
		ap := strings.ToLower(cm[4])
		if ap == "pm" && hh < 12 {
			hh += 12
		} else if ap == "am" && hh == 12 {
			hh = 0
		}
		if plausibleClock(hh, mm, ss) {
			precise = true
		} else {
			hh, mm, ss = 0, 0, 0
		}
	}

	return time.Date(y, time.Month(mo), d, hh, mm, ss, 0, time.Local), precise, true
}

// normYear expands a 2-digit year to 4 digits (00-69 -> 2000s, 70-99 -> 1900s),
// matching Go's time-layout convention; a 4-digit year passes through.
func normYear(s string) int {
	n, _ := strconv.Atoi(s)
	if len(s) <= 2 {
		if n < 70 {
			return 2000 + n
		}
		return 1900 + n
	}
	return n
}

// SignalFromOCR builds a Signal from an OCR candidate, scaling trust by whether
// the read confidence meets minConf. Returns ok=false when the text has no
// parseable date.
func SignalFromOCR(c OCRDateCandidate, minConf float64) (Signal, bool) {
	t, precise, ok := ParseOCRDate(c.Text)
	if !ok {
		return Signal{}, false
	}
	trust := TrustWeak
	if c.Confidence >= minConf {
		trust = TrustStrong
	}
	return Signal{
		Source:         SourceOCR,
		Trust:          trust,
		Time:           t,
		Raw:            strings.TrimSpace(c.Text),
		OCRConfidence:  c.Confidence,
		FrameTimestamp: c.FrameTimestamp,
		TimePrecise:    precise,
	}, true
}
