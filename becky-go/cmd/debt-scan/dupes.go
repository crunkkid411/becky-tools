// dupes.go — category 8: duplicate code blocks.
//
// We slide an N-line window across every file's *normalized* lines, hash each
// window, and report windows whose hash collides with one seen earlier. The
// first occurrence of a block is the canonical home (dup_with points back to it).
// Normalization (collapse whitespace, drop blank/comment-only lines) makes the
// detector robust to reindentation while still demanding a substantive match —
// we require a minimum number of non-trivial lines so we don't flag short, dull
// repetition like a run of closing braces.
package main

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
)

// dupWindowLines is the size of the sliding window in normalized lines.
const dupWindowLines = 6

// dupMinNonTrivial is how many of the window's lines must carry real content
// (not just braces/blank) for the window to be eligible.
const dupMinNonTrivial = 4

// dupMinDistinct is how many *distinct* lines a window must contain so that a run
// of near-identical boilerplate (map entries, struct tags) is not flagged as a
// copy of itself.
const dupMinDistinct = 4

// blockOccurrence records where a normalized window first appeared.
type blockOccurrence struct {
	file string
	line int // 1-based start line of the window
}

// normalizedLine is a source line reduced to its structural fingerprint, with
// the original 1-based line number kept for reporting.
type normalizedLine struct {
	text string
	line int
}

// scanDupes is cross-file: it consumes the whole corpus and returns duplicate
// findings. seen is shared across files (caller passes one map for the run) so
// duplication is detected within and across files. It works on comment-stripped
// source that KEEPS string literals, so distinct data lines aren't collapsed to
// an identical fingerprint.
func scanDupes(sf sourceFile, src string, seen map[string]blockOccurrence) []Finding {
	norm := normalizeForDupes(stripComments(src))
	if len(norm) < dupWindowLines {
		return nil
	}
	var findings []Finding
	reported := map[int]bool{} // avoid stacking overlapping windows on one line
	for i := 0; i+dupWindowLines <= len(norm); i++ {
		window := norm[i : i+dupWindowLines]
		if countNonTrivial(window) < dupMinNonTrivial {
			continue
		}
		if distinctLines(window) < dupMinDistinct {
			continue // window is repetitive boilerplate (e.g. struct-tag runs), not a copy
		}
		h := hashWindow(window)
		startLine := window[0].line
		if prev, ok := seen[h]; ok {
			if reported[startLine] {
				continue
			}
			reported[startLine] = true
			findings = append(findings, Finding{
				Category: catDupes,
				File:     sf.rel,
				Line:     startLine,
				Severity: sevMedium,
				Language: sf.lang,
				DupWith:  prev.file + ":" + itoa(prev.line),
				Source:   "pure-go",
				Message:  itoa(dupWindowLines) + "-line block duplicates " + prev.file + ":" + itoa(prev.line),
			})
		} else {
			seen[h] = blockOccurrence{file: sf.rel, line: startLine}
		}
	}
	return findings
}

// normalizeForDupes reduces masked source to structural lines: whitespace
// collapsed, blank and comment-only lines dropped. Comments are already blanked
// by maskComments, so a comment-only line normalizes to "".
func normalizeForDupes(masked string) []normalizedLine {
	var out []normalizedLine
	for i, raw := range splitLines(masked) {
		t := strings.Join(strings.Fields(raw), " ")
		if t == "" {
			continue
		}
		out = append(out, normalizedLine{text: t, line: i + 1})
	}
	return out
}

// distinctLines counts how many unique line texts a window contains.
func distinctLines(window []normalizedLine) int {
	set := map[string]bool{}
	for _, l := range window {
		set[l.text] = true
	}
	return len(set)
}

// countNonTrivial counts window lines that aren't just punctuation/braces.
func countNonTrivial(window []normalizedLine) int {
	n := 0
	for _, l := range window {
		if isTrivialLine(l.text) {
			continue
		}
		n++
	}
	return n
}

// isTrivialLine reports whether a normalized line is structurally empty content
// (only braces, brackets, parens, semicolons, commas).
func isTrivialLine(t string) bool {
	for _, r := range t {
		switch r {
		case '{', '}', '(', ')', '[', ']', ';', ',', ' ':
		default:
			return false
		}
	}
	return true
}

// hashWindow returns a stable hex hash of a window's normalized text.
func hashWindow(window []normalizedLine) string {
	h := sha1.New()
	for _, l := range window {
		h.Write([]byte(l.text))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}
