// todos.go — category 1: stale TODO/FIXME/HACK/XXX markers.
//
// Every TODO-class marker in a comment is a finding. When the scan root is a git
// repo we date each marker with `git blame --porcelain` (the author-time of the
// line) and only flag markers older than --min-age days as "stale"; fresher ones
// are reported at low severity. Outside a git repo we cannot age them, so every
// marker is reported (with no age) and noted as such.
package main

import (
	"regexp"
	"strings"
	"time"
)

// todoMarker matches a TODO-class marker as a whole word within comment text.
var todoMarker = regexp.MustCompile(`\b(TODO|FIXME|HACK|XXX|BUG|REFACTOR)\b`)

// blockComment matches a single-line /* ... */ fragment.
var blockComment = regexp.MustCompile(`/\*.*?\*/`)

// scanTODOs returns TODO findings for one file. ages, when non-nil, maps a
// 1-based line number to the marker's age in days (from git blame); a missing
// entry means the age is unknown.
func scanTODOs(sf sourceFile, src string, minAgeDays int, ages map[int]int) []Finding {
	var findings []Finding
	lines := splitLines(src)
	for i, line := range lines {
		lineNo := i + 1
		comment := commentOnLine(line, sf.lang)
		if comment == "" {
			continue
		}
		loc := todoMarker.FindStringIndex(comment)
		if loc == nil {
			continue
		}
		marker := comment[loc[0]:loc[1]]
		age, haveAge := -1, false
		if ages != nil {
			if a, ok := ages[lineNo]; ok {
				age, haveAge = a, true
			}
		}
		sev := sevLow
		stale := false
		if haveAge && age >= minAgeDays {
			sev = sevMedium
			stale = true
		}
		msg := strings.TrimSpace(comment)
		if len(msg) > 160 {
			msg = msg[:160] + "…"
		}
		f := Finding{
			Category: catTODO,
			File:     sf.rel,
			Line:     lineNo,
			Severity: sev,
			Language: sf.lang,
			Symbol:   marker,
			Source:   "pure-go",
			Message:  todoMessage(marker, stale, haveAge, age, minAgeDays, msg),
		}
		if haveAge {
			f.AgeDays = age
		}
		findings = append(findings, f)
	}
	return findings
}

// todoMessage builds the human message for a TODO finding.
func todoMessage(marker string, stale, haveAge bool, age, minAge int, text string) string {
	switch {
	case stale:
		return marker + " is " + itoa(age) + " days old (>= " + itoa(minAge) + "): " + text
	case haveAge:
		return marker + " (" + itoa(age) + " days old): " + text
	default:
		return marker + " marker (age unknown — not a git repo): " + text
	}
}

// commentOnLine returns the comment portion of a single line for the language,
// or "" if the line has no comment. Approximate but adequate: it ignores the
// rare case of a // sequence appearing inside a string on the same line.
func commentOnLine(line, lang string) string {
	if m := blockComment.FindString(line); m != "" {
		return m
	}
	switch lang {
	case langPython:
		if idx := strings.Index(line, "#"); idx >= 0 {
			return line[idx:]
		}
	default:
		if idx := strings.Index(line, "//"); idx >= 0 {
			return line[idx:]
		}
		if idx := strings.Index(line, "#"); idx >= 0 { // shell-style in some configs
			return line[idx:]
		}
	}
	return ""
}

// blameAges runs `git blame --porcelain` once per file and returns a map of
// 1-based line -> age in days. Best-effort: any error returns nil so the caller
// falls back to ageless reporting.
func blameAges(gitRoot, file string, now time.Time) map[int]int {
	out, err := runGit(gitRoot, "blame", "--porcelain", "--", file)
	if err != nil {
		return nil
	}
	return parseBlamePorcelain(out, now)
}

// parseBlamePorcelain turns porcelain blame output into line->ageDays. The
// porcelain format emits "<sha> <orig> <final> <count>" header lines followed by
// "author-time <unix>" attributes; the final line number applies to the next
// "\t"-prefixed content line.
func parseBlamePorcelain(out string, now time.Time) map[int]int {
	ages := map[int]int{}
	var curLine, curTime int
	for _, ln := range splitLines(out) {
		switch {
		case len(ln) > 0 && ln[0] >= '0' && ln[0] <= '9':
			fields := strings.Fields(ln)
			if len(fields) >= 3 && len(fields[0]) >= 7 {
				curLine = atoi(fields[2]) // final line number
			}
		case strings.HasPrefix(ln, "author-time "):
			curTime = atoi(strings.TrimPrefix(ln, "author-time "))
		case strings.HasPrefix(ln, "\t"):
			if curLine > 0 && curTime > 0 {
				days := int(now.Sub(time.Unix(int64(curTime), 0)).Hours() / 24)
				if days < 0 {
					days = 0
				}
				ages[curLine] = days
			}
		}
	}
	return ages
}
