// lex.go — light lexical helpers shared by the language-agnostic analyzers.
//
// These are deliberately approximate: a regex/keyword pass, not a full parser.
// They are good enough for debt heuristics (complexity counts, comment masking,
// identifier scanning) while staying stdlib-only and language-agnostic. Where a
// language has a real parser available (Go's go/ast), the Go analyzer uses it
// instead — these helpers are the fallback for Python/Rust/TS/JS.
package main

import (
	"sort"
	"strings"
	"unicode"
)

// sortStrings sorts a string slice in place (ascending).
func sortStrings(s []string) { sort.Strings(s) }

// sortSources sorts source files by relative path for deterministic output.
func sortSources(s []sourceFile) {
	sort.Slice(s, func(i, j int) bool { return s[i].rel < s[j].rel })
}

// maskComments returns the source with comment and string content blanked out
// (replaced by spaces, preserving line/column positions) so keyword scans don't
// trip on the word "if" inside a comment or a string literal. Handles //, #,
// /* */ comments and "..." '...' `...` strings — the common shapes across Go,
// Python, Rust, TS and JS. It is intentionally conservative.
func maskComments(src string) string {
	out := []byte(src)
	n := len(out)
	i := 0
	blank := func(j int) {
		if out[j] != '\n' {
			out[j] = ' '
		}
	}
	for i < n {
		c := out[i]
		switch {
		case c == '/' && i+1 < n && out[i+1] == '/':
			for i < n && out[i] != '\n' {
				blank(i)
				i++
			}
		case c == '#':
			for i < n && out[i] != '\n' {
				blank(i)
				i++
			}
		case c == '/' && i+1 < n && out[i+1] == '*':
			blank(i)
			blank(i + 1)
			i += 2
			for i < n && !(out[i] == '*' && i+1 < n && out[i+1] == '/') {
				blank(i)
				i++
			}
			if i+1 < n {
				blank(i)
				blank(i + 1)
				i += 2
			}
		case c == '"' || c == '\'' || c == '`':
			quote := c
			blank(i)
			i++
			for i < n && out[i] != quote {
				if out[i] == '\\' && i+1 < n && quote != '`' {
					blank(i)
					blank(i + 1)
					i += 2
					continue
				}
				blank(i)
				i++
			}
			if i < n {
				blank(i)
				i++
			}
		default:
			i++
		}
	}
	return string(out)
}

// splitLines splits source into lines; line index 0 corresponds to line 1.
func splitLines(src string) []string {
	return strings.Split(src, "\n")
}

// stripComments blanks out only comments (not strings), preserving line/column
// positions. Used by duplicate detection, which needs string literals intact so
// that distinct map/array entries don't all collapse to the same fingerprint.
func stripComments(src string) string {
	out := []byte(src)
	n := len(out)
	i := 0
	blank := func(j int) {
		if out[j] != '\n' {
			out[j] = ' '
		}
	}
	for i < n {
		c := out[i]
		switch {
		case c == '/' && i+1 < n && out[i+1] == '/':
			for i < n && out[i] != '\n' {
				blank(i)
				i++
			}
		case c == '#':
			for i < n && out[i] != '\n' {
				blank(i)
				i++
			}
		case c == '/' && i+1 < n && out[i+1] == '*':
			blank(i)
			blank(i + 1)
			i += 2
			for i < n && !(out[i] == '*' && i+1 < n && out[i+1] == '/') {
				blank(i)
				i++
			}
			if i+1 < n {
				blank(i)
				blank(i + 1)
				i += 2
			}
		case c == '"' || c == '\'' || c == '`':
			// Skip over the string literal without blanking it.
			quote := c
			i++
			for i < n && out[i] != quote {
				if out[i] == '\\' && i+1 < n && quote != '`' {
					i += 2
					continue
				}
				i++
			}
			if i < n {
				i++
			}
		default:
			i++
		}
	}
	return string(out)
}

// isIdentRune reports whether r can appear in an identifier.
func isIdentRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// wordBoundaryContains reports whether haystack contains word as a whole token
// (surrounded by non-identifier runes). Used for unused-import reference checks
// and deprecated-symbol scans without false substring matches.
func wordBoundaryContains(haystack, word string) bool {
	if word == "" {
		return false
	}
	idx := 0
	for {
		j := strings.Index(haystack[idx:], word)
		if j < 0 {
			return false
		}
		start := idx + j
		end := start + len(word)
		beforeOK := start == 0 || !isIdentRune(rune(haystack[start-1]))
		afterOK := end == len(haystack) || !isIdentRune(rune(haystack[end]))
		if beforeOK && afterOK {
			return true
		}
		idx = start + 1
		if idx >= len(haystack) {
			return false
		}
	}
}

// codeBlock represents a brace/indent-delimited function body span by line
// numbers (1-based, inclusive). Used by the generic complexity counter.
type codeBlock struct {
	name      string
	startLine int
	endLine   int
	body      string
}
