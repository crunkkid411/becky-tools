// categorize.go — cheap, deterministic heuristics that tag a recognized OCR line
// with a candidate_* category. These are search/ranking HINTS, not conclusions:
// they let `becky find` and corpus triage answer "show me every frame with a
// candidate_address / TX-plate-shaped text / a storefront name / a burned-in
// timestamp" — turning 500 GB of mostly-b-roll into something rank-by-actionability.
//
// Per FORENSIC-OUTPUT-PHILOSOPHY: a category is plainly labeled "candidate_*". The
// CONCLUSION (this IS the address) is reached upstream by corroborating the OCR
// read against fixtures/listings/other passes + confidence — not asserted here from
// a regex alone. This file only proposes; corroboration concludes.
package main

import (
	"regexp"
	"strings"
)

// Category constants — the labels stored in ocr_text.category and emitted per line.
const (
	catAddress   = "candidate_address"
	catPlate     = "candidate_plate"
	catBusiness  = "candidate_business"
	catTimestamp = "candidate_timestamp"
	catText      = "text"
)

var (
	// addressRe: a leading street number followed by a street name and a US street
	// suffix (St/Ave/Cir/Dr/Rd/Ln/Blvd/Ct/Way/Pl/Ter/Hwy/Pkwy), suffix optionally
	// abbreviated with a period. Case-insensitive; matches "2601 Chatham Cir".
	addressRe = regexp.MustCompile(`(?i)\b\d{1,6}\s+([A-Za-z0-9.'-]+\s+){0,4}` +
		`(st|street|ave|avenue|blvd|boulevard|cir|circle|dr|drive|rd|road|ln|lane|` +
		`ct|court|way|pl|place|ter|terrace|hwy|highway|pkwy|parkway|trl|trail|sq|square)\b\.?`)

	// timestampRe: a burned-in date and/or clock time. Covers M/D/Y or Y-M-D dates
	// and HH:MM(:SS) clocks (optionally with AM/PM) — the kind a camera or stream
	// overlay burns in. Useful against F3 (untrusted file mtime).
	timestampRe = regexp.MustCompile(`(?i)\b(` +
		`\d{1,4}[/.-]\d{1,2}[/.-]\d{1,4}` + // 7/4/2025, 2025-07-04
		`|\d{1,2}:\d{2}(:\d{2})?\s*(am|pm)?` + // 18:14:31, 5:17 PM
		`)\b`)

	// plateBlockRe / plateStatePref: a US-plate-shaped short alnum block (5-8 chars
	// mixing letters and digits, no spaces), or an explicit state prefix
	// (e.g. "TX 7KZ123"). Deliberately loose; a plate read is hard scene text and
	// usually lands in the low-confidence list anyway.
	plateBlockRe   = regexp.MustCompile(`^[A-Z0-9]{5,8}$`)
	plateStatePref = regexp.MustCompile(`^(TX|TEX|TEXAS|CA|FL|NY|IN|GA|AZ|OH|MI|NC|WA|CO|OR|NV)\s+[A-Z0-9]{3,7}$`)
)

// categorize returns the best-fit candidate_* category for a recognized line. The
// order encodes priority: a timestamp pattern is unambiguous; an address pattern is
// high-value; a plate shape is specific; a title-case multiword phrase reads as
// signage/business; everything else is plain text.
func categorize(text string) string {
	t := strings.TrimSpace(text)
	if t == "" {
		return catText
	}
	if isTimestamp(t) {
		return catTimestamp
	}
	if addressRe.MatchString(t) {
		return catAddress
	}
	if isPlate(t) {
		return catPlate
	}
	if isBusiness(t) {
		return catBusiness
	}
	return catText
}

// isTimestamp reports a burned-in date/clock pattern, but only when the line is
// SHORT and mostly the timestamp — a long sentence that happens to mention a time
// stays plain text (so chat lines aren't all mislabeled).
func isTimestamp(t string) bool {
	if len(t) > 24 {
		return false
	}
	return timestampRe.MatchString(t)
}

// isPlate reports a US-plate-shaped token: either an explicit "STATE ABC123" or a
// single 5-8 char block that mixes at least one letter and one digit (so a pure
// word like "ENTER" or a pure number like "12345" is not flagged a plate).
func isPlate(t string) bool {
	up := strings.ToUpper(strings.TrimSpace(t))
	if plateStatePref.MatchString(up) {
		return true
	}
	block := strings.ReplaceAll(up, " ", "")
	if !plateBlockRe.MatchString(block) {
		return false
	}
	hasLetter, hasDigit := false, false
	for _, r := range block {
		if r >= 'A' && r <= 'Z' {
			hasLetter = true
		}
		if r >= '0' && r <= '9' {
			hasDigit = true
		}
	}
	return hasLetter && hasDigit
}

// isBusiness reports a storefront/signage-style phrase: 2-6 words that are mostly
// capitalized (Title Case or ALL CAPS), no terminal sentence punctuation, and not a
// chat handle (a leading "@" is chat, not signage). A heuristic for ranking, not a
// claim that the text names a real business.
func isBusiness(t string) bool {
	if strings.HasPrefix(t, "@") {
		return false
	}
	if strings.ContainsAny(t, ".!?") {
		return false
	}
	words := strings.Fields(t)
	if len(words) < 2 || len(words) > 6 {
		return false
	}
	capped := 0
	for _, w := range words {
		r := []rune(w)
		if len(r) == 0 {
			continue
		}
		first := r[0]
		// Count a word as "capitalized" if it starts uppercase or is all-caps.
		if (first >= 'A' && first <= 'Z') || w == strings.ToUpper(w) {
			capped++
		}
	}
	// Most words capitalized -> reads like a sign/name.
	return float64(capped)/float64(len(words)) >= 0.6
}
