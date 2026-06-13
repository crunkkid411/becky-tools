// naming.go — category 6: naming inconsistencies.
//
// Each language has a dominant identifier convention (snake_case for Python/Rust,
// camelCase/PascalCase for Go/TS/JS). We detect declared identifiers in a file,
// classify each as snake_case or camelCase, and flag the minority style when a
// file clearly mixes the two. We report the off-convention declarations so the
// finding points at a real line, not just "this file is inconsistent".
package main

import (
	"regexp"
	"strings"
)

// declPattern finds identifier *declarations* per language so we judge names the
// author chose, not names they merely call. Capture group 1 is the identifier.
var declPattern = map[string]*regexp.Regexp{
	langPython: regexp.MustCompile(`(?m)^\s*(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)|^\s*([A-Za-z_][A-Za-z0-9_]*)\s*[:=]`),
	langRust:   regexp.MustCompile(`(?m)\b(?:fn|let|const|static)\s+(?:mut\s+)?([A-Za-z_][A-Za-z0-9_]*)`),
	langGo:     regexp.MustCompile(`(?m)\bfunc\s+(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)|\b([A-Za-z_][A-Za-z0-9_]*)\s*:=`),
	langTS:     regexp.MustCompile(`(?m)\b(?:function|const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)`),
	langJS:     regexp.MustCompile(`(?m)\b(?:function|const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)`),
}

// preferredStyle is the idiomatic style per language: "snake" or "camel".
var preferredStyle = map[string]string{
	langPython: "snake",
	langRust:   "snake",
	langGo:     "camel",
	langTS:     "camel",
	langJS:     "camel",
}

// namedIdent is a declared identifier with its line and detected style.
type namedIdent struct {
	name  string
	line  int
	style string // "snake", "camel", or "" (ambiguous: single word / all-caps)
}

// scanNaming flags identifiers that violate the file's preferred convention,
// but only when the file actually mixes styles (so a fully snake_case Python
// file with an idiomatic dunder is left alone). Returns at most one finding per
// off-style identifier.
func scanNaming(sf sourceFile, masked string) []Finding {
	pat := declPattern[sf.lang]
	if pat == nil {
		return nil
	}
	idents := collectIdents(masked, pat)
	snake, camel := 0, 0
	for _, id := range idents {
		switch id.style {
		case "snake":
			snake++
		case "camel":
			camel++
		}
	}
	// Only judge a file that has a clear majority and a real minority.
	if snake == 0 || camel == 0 {
		return nil
	}
	want := preferredStyle[sf.lang]
	var findings []Finding
	seen := map[string]bool{}
	for _, id := range idents {
		if id.style == "" || id.style == want {
			continue
		}
		key := id.name + ":" + itoa(id.line)
		if seen[key] {
			continue
		}
		seen[key] = true
		findings = append(findings, Finding{
			Category: catNaming,
			File:     sf.rel,
			Line:     id.line,
			Severity: sevLow,
			Language: sf.lang,
			Symbol:   id.name,
			Source:   "pure-go",
			Message:  "identifier '" + id.name + "' uses " + id.style + "_case in a file that is mostly " + want + "Case",
		})
	}
	return findings
}

// collectIdents extracts declared identifiers (with line numbers and style) from
// masked source using the language's declaration pattern.
func collectIdents(masked string, pat *regexp.Regexp) []namedIdent {
	var out []namedIdent
	for _, m := range pat.FindAllStringSubmatchIndex(masked, -1) {
		// The pattern may have multiple capture groups (alternation); take the
		// first non-empty one.
		name := ""
		var start int
		for g := 1; g*2+1 < len(m); g++ {
			if m[g*2] >= 0 {
				name = masked[m[g*2]:m[g*2+1]]
				start = m[g*2]
				break
			}
		}
		if name == "" || isCommonKeyword(name) {
			continue
		}
		out = append(out, namedIdent{
			name:  name,
			line:  byteToLine(masked, start),
			style: classifyStyle(name),
		})
	}
	return out
}

// classifyStyle labels an identifier as snake, camel, or "" when ambiguous
// (single lowercase word, ALL_CAPS constant, or single char).
func classifyStyle(name string) string {
	if name == "" || len(name) < 2 {
		return ""
	}
	hasUnderscore := strings.Contains(strings.Trim(name, "_"), "_")
	hasInnerUpper := false
	for i, r := range name {
		if i > 0 && r >= 'A' && r <= 'Z' {
			hasInnerUpper = true
			break
		}
	}
	upperCount := 0
	for _, r := range name {
		if r >= 'A' && r <= 'Z' {
			upperCount++
		}
	}
	allCaps := upperCount > 0 && strings.ToUpper(name) == name
	switch {
	case allCaps:
		return "" // CONSTANT_CASE is its own convention; don't penalize
	case hasUnderscore:
		return "snake"
	case hasInnerUpper:
		return "camel"
	default:
		return "" // single lowercase word fits either convention
	}
}

// commonKeywords are control words the declaration regexes can accidentally
// capture; we never treat them as identifiers.
var commonKeywords = map[string]bool{
	"if": true, "for": true, "while": true, "return": true, "else": true,
	"func": true, "fn": true, "let": true, "const": true, "var": true,
	"def": true, "async": true, "await": true, "type": true, "struct": true,
}

func isCommonKeyword(s string) bool { return commonKeywords[s] }
