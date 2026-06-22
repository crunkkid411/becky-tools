// ssformat.go — single-shot output rendering. Plain mode prints ONLY the answer
// body with NO ANSI color codes (single-shot output is consumed by scripts/files;
// lipgloss styling is for the human window only — SPEC-ASK-SINGLESHOT.md §4.2).
// JSON mode (handled in singleshot.go) emits the §4.2 object.
package main

import "regexp"

// ansiEscape matches CSI/SGR escape sequences (e.g. lipgloss color codes). The
// router's replies are styled for the TUI; the single-shot renderer strips that so
// a pipe/file consumer gets clean text. (Belt-and-suspenders: most single-shot
// answers are commands or the vision description, which carry no styling.)
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// plainAnswer strips ANSI escapes from a (possibly TUI-styled) router reply so it
// is safe for stdout consumption by scripts.
func plainAnswer(s string) string {
	return ansiEscape.ReplaceAllString(s, "")
}

// formatPlain renders the answer body for plain (non-JSON) mode: just the answer,
// already ANSI-free.
func formatPlain(res singleShotResult) string {
	return plainAnswer(res.Answer)
}
