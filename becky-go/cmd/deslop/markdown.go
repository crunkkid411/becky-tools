// markdown.go — markdown-aware skip-zone detection.
//
// becky-deslop must never alter or flag content inside:
//   - fenced code blocks (``` ... ``` or ~~~ ... ~~~)
//   - inline code spans (` ... `)
//   - YAML frontmatter (a leading --- ... --- block at the very top)
//
// We compute these as byte ranges over the original text. The engine then
// rejects any match that overlaps a skip range. Working on byte offsets keeps
// line/col reporting and replacement splicing consistent with Go's regexp,
// which returns byte indices.
package main

import "strings"

// skipRange is a half-open byte interval [Start, End) that must not be touched.
type skipRange struct {
	Start int
	End   int
}

// computeSkipRanges scans the text once and returns the protected byte ranges,
// sorted by Start. Fenced blocks take priority; inside a fenced block we do not
// also scan for inline code (the whole block is already protected).
func computeSkipRanges(text string) []skipRange {
	var ranges []skipRange

	// 1. YAML frontmatter: a "---" line as the very first line, closed by the
	//    next "---" or "..." line. Protect the entire span incl. delimiters.
	if fmEnd := frontmatterEnd(text); fmEnd > 0 {
		ranges = append(ranges, skipRange{Start: 0, End: fmEnd})
	}

	// 2. Fenced code blocks (line-based).
	fenced := fencedCodeRanges(text)
	ranges = append(ranges, fenced...)

	// 3. Inline code spans, but only outside fenced blocks.
	ranges = append(ranges, inlineCodeRanges(text, fenced)...)

	sortRanges(ranges)
	return ranges
}

// frontmatterEnd returns the byte offset just past the closing frontmatter
// delimiter, or 0 if the text has no leading YAML frontmatter.
func frontmatterEnd(text string) int {
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return 0
	}
	offset := 0
	first := true
	for _, ln := range splitKeepEnds(text) {
		trimmed := strings.TrimRight(ln, "\r\n")
		if first {
			first = false
			offset += len(ln)
			continue
		}
		if trimmed == "---" || trimmed == "..." {
			return offset + len(ln)
		}
		offset += len(ln)
	}
	return 0 // unterminated frontmatter: treat as normal text
}

// fencedCodeRanges returns byte ranges for ``` / ~~~ fenced blocks. A fence
// opens on a line whose first non-space run is 3+ backticks or tildes and
// closes on the next line with a matching fence of the same character.
func fencedCodeRanges(text string) []skipRange {
	var ranges []skipRange
	offset := 0
	inFence := false
	var fenceChar byte
	var blockStart int

	for _, ln := range splitKeepEnds(text) {
		trimmed := strings.TrimLeft(strings.TrimRight(ln, "\r\n"), " \t")
		fc, ok := fenceMarker(trimmed)
		if !inFence {
			if ok {
				inFence = true
				fenceChar = fc
				blockStart = offset // include the opening fence line
			}
		} else if ok && fc == fenceChar {
			ranges = append(ranges, skipRange{Start: blockStart, End: offset + len(ln)})
			inFence = false
		}
		offset += len(ln)
	}
	if inFence {
		ranges = append(ranges, skipRange{Start: blockStart, End: len(text)})
	}
	return ranges
}

// fenceMarker reports whether a trimmed line opens/closes a fence and which
// character it uses.
func fenceMarker(trimmed string) (byte, bool) {
	if len(trimmed) < 3 {
		return 0, false
	}
	c := trimmed[0]
	if c != '`' && c != '~' {
		return 0, false
	}
	n := 0
	for n < len(trimmed) && trimmed[n] == c {
		n++
	}
	if n < 3 {
		return 0, false
	}
	return c, true
}

// inlineCodeRanges finds `...` spans on a single line, skipping any that fall
// inside an already-protected fenced range. Follows the CommonMark rule that a
// code span is delimited by equal-length backtick runs.
func inlineCodeRanges(text string, fenced []skipRange) []skipRange {
	var ranges []skipRange
	i := 0
	n := len(text)
	for i < n {
		if text[i] == '`' && !inAnyRange(i, fenced) {
			runLen := 0
			for i+runLen < n && text[i+runLen] == '`' {
				runLen++
			}
			open := i
			j := i + runLen
			closed := false
			for j < n && text[j] != '\n' {
				if text[j] == '`' {
					cl := 0
					for j+cl < n && text[j+cl] == '`' {
						cl++
					}
					if cl == runLen {
						ranges = append(ranges, skipRange{Start: open, End: j + cl})
						i = j + cl
						closed = true
						break
					}
					j += cl
					continue
				}
				j++
			}
			if !closed {
				i = open + runLen // no closer on this line: not a span
			}
			continue
		}
		i++
	}
	return ranges
}

// inSkip reports whether [start,end) overlaps any protected range.
func inSkip(start, end int, ranges []skipRange) bool {
	for _, r := range ranges {
		if start < r.End && end > r.Start {
			return true
		}
	}
	return false
}

// inAnyRange reports whether byte position pos is inside any range.
func inAnyRange(pos int, ranges []skipRange) bool {
	for _, r := range ranges {
		if pos >= r.Start && pos < r.End {
			return true
		}
	}
	return false
}

// splitKeepEnds splits text into lines, keeping the trailing newline on each so
// byte offsets stay exact when summed.
func splitKeepEnds(text string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			lines = append(lines, text[start:i+1])
			start = i + 1
		}
	}
	if start < len(text) {
		lines = append(lines, text[start:])
	}
	return lines
}

// sortRanges sorts by Start ascending (insertion sort; ranges are few).
func sortRanges(r []skipRange) {
	for i := 1; i < len(r); i++ {
		for j := i; j > 0 && r[j-1].Start > r[j].Start; j-- {
			r[j-1], r[j] = r[j], r[j-1]
		}
	}
}
