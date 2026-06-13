// funcs.go — generic function-span extraction for the non-Go languages.
//
// Complexity, docstring and naming heuristics all need to know where each
// function starts and ends. Go gets exact spans from go/ast (see goana.go); the
// other languages use these regex-anchored extractors:
//   - brace languages (Rust, TS, JS): find a signature line, then balance {}.
//   - Python: find a `def`, then take the indented suite beneath it.
//
// Both operate on comment-masked source so braces inside strings/comments don't
// throw off the balance.
package main

import (
	"regexp"
	"strings"
)

// braceFuncSig matches a function/method signature line in Rust/TS/JS up to the
// opening brace. It is permissive on purpose: anything that looks like a named
// callable followed by ( ... ) { is treated as a function head.
var braceFuncSig = regexp.MustCompile(
	`(?m)^[ \t]*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+|export\s+|default\s+|static\s+|const\s+|unsafe\s+|public\s+|private\s+|protected\s+)*` +
		`(?:fn|function)?\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*(?:<[^>]*>)?\s*\([^;{]*\)\s*(?:->[^={;]+|:\s*[A-Za-z0-9_<>,\[\] .]+)?\s*\{`)

// pyDef matches a Python def/async def signature.
var pyDef = regexp.MustCompile(`(?m)^([ \t]*)(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)

// extractBraceFuncs returns function blocks from brace-delimited source. masked
// is the comment/string-masked copy used for brace balancing; raw is the
// original used for the body text in findings.
func extractBraceFuncs(raw, masked string) []codeBlock {
	var blocks []codeBlock
	rawLines := splitLines(raw)
	locs := braceFuncSig.FindAllStringSubmatchIndex(masked, -1)
	for _, loc := range locs {
		openBrace := loc[1] - 1 // index of the matched '{'
		name := masked[loc[2]:loc[3]]
		endByte := matchBrace(masked, openBrace)
		if endByte < 0 {
			continue
		}
		startLine := byteToLine(masked, loc[0])
		endLine := byteToLine(masked, endByte)
		body := joinLines(rawLines, startLine, endLine)
		blocks = append(blocks, codeBlock{
			name:      name,
			startLine: startLine,
			endLine:   endLine,
			body:      body,
		})
	}
	return blocks
}

// extractPythonFuncs returns function blocks from Python source using
// indentation. masked is used to find defs without matching ones inside strings.
func extractPythonFuncs(raw, masked string) []codeBlock {
	var blocks []codeBlock
	rawLines := splitLines(raw)
	maskedLines := splitLines(masked)
	locs := pyDef.FindAllStringSubmatchIndex(masked, -1)
	for _, loc := range locs {
		indent := masked[loc[2]:loc[3]]
		name := masked[loc[4]:loc[5]]
		startLine := byteToLine(masked, loc[0]) // 1-based
		// The body runs until a non-blank line dedents to <= the def's indent.
		endLine := startLine
		for idx := startLine; idx < len(maskedLines); idx++ {
			line := maskedLines[idx] // 0-based idx == source line idx+1
			if strings.TrimSpace(line) == "" {
				continue
			}
			if leadingWidth(line) <= len(indent) {
				break
			}
			endLine = idx + 1
		}
		body := joinLines(rawLines, startLine, endLine)
		blocks = append(blocks, codeBlock{
			name:      name,
			startLine: startLine,
			endLine:   endLine,
			body:      body,
		})
	}
	return blocks
}

// matchBrace returns the byte index of the '}' that closes the '{' at open, or
// -1 if unbalanced. Operates on masked source so braces in strings are gone.
func matchBrace(masked string, open int) int {
	depth := 0
	for i := open; i < len(masked); i++ {
		switch masked[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// byteToLine returns the 1-based line number containing byte index pos.
func byteToLine(s string, pos int) int {
	if pos > len(s) {
		pos = len(s)
	}
	return strings.Count(s[:pos], "\n") + 1
}

// joinLines returns lines start..end (1-based, inclusive) joined by "\n".
func joinLines(lines []string, start, end int) string {
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return ""
	}
	return strings.Join(lines[start-1:end], "\n")
}

// leadingWidth counts leading spaces/tabs (each counts as one; we only compare
// relative indentation within a single function).
func leadingWidth(line string) int {
	n := 0
	for _, r := range line {
		if r == ' ' || r == '\t' {
			n++
			continue
		}
		break
	}
	return n
}
