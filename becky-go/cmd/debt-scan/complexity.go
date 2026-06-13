// complexity.go — category 7: high cyclomatic complexity.
//
// We approximate McCabe cyclomatic complexity by counting decision points in a
// function body and adding one (the base path). Decision points are the branch
// keywords if/elif/for/while/case/catch/when and the short-circuit operators
// && and ||. This is the standard "count the branches" estimate; it matches the
// number radon/gocyclo report closely enough to triage debt. A function whose
// estimate exceeds --max-complexity is flagged, severity rising with the count.
package main

import "regexp"

// branchKeyword matches a decision-point keyword as a whole word. `else if`
// counts via the `if`; bare `else` does not add a path, so it is excluded.
var branchKeyword = regexp.MustCompile(`\b(if|elif|for|while|case|catch|when)\b`)

// shortCircuit matches the && and || operators (each adds one path).
var shortCircuit = regexp.MustCompile(`&&|\|\|`)

// ternary matches the C-style ?: operator (one extra path), avoiding `?.`
// (optional chaining) and `??` (nullish coalescing) in TS/JS.
var ternary = regexp.MustCompile(`\?[^.?]`)

// cyclomatic returns the estimated complexity of a comment-masked function body.
func cyclomatic(maskedBody, lang string) int {
	c := 1
	c += len(branchKeyword.FindAllStringIndex(maskedBody, -1))
	c += len(shortCircuit.FindAllStringIndex(maskedBody, -1))
	if lang == langTS || lang == langJS || lang == langRust || lang == langGo {
		c += len(ternary.FindAllStringIndex(maskedBody, -1))
	}
	return c
}

// scanComplexity flags every function in a (non-Go) file whose estimated
// complexity exceeds maxComplexity. Go is handled with exact AST data in
// goana.go; this generic path serves Python/Rust/TS/JS.
func scanComplexity(sf sourceFile, raw, masked string, maxComplexity int) []Finding {
	var blocks []codeBlock
	switch sf.lang {
	case langPython:
		blocks = extractPythonFuncs(raw, masked)
	default:
		blocks = extractBraceFuncs(raw, masked)
	}
	var findings []Finding
	for _, b := range blocks {
		maskedBody := maskComments(b.body)
		c := cyclomatic(maskedBody, sf.lang)
		if c <= maxComplexity {
			continue
		}
		findings = append(findings, complexityFinding(sf, b.name, b.startLine, c, maxComplexity))
	}
	return findings
}

// complexityFinding builds a finding for an over-complex function.
func complexityFinding(sf sourceFile, name string, line, c, threshold int) Finding {
	sev := sevMedium
	if c >= threshold*2 {
		sev = sevHigh
	}
	if c >= threshold*4 {
		sev = sevCritical
	}
	return Finding{
		Category:   catComplexity,
		File:       sf.rel,
		Line:       line,
		Severity:   sev,
		Language:   sf.lang,
		Symbol:     name,
		Complexity: c,
		Source:     "pure-go",
		Message:    "function '" + name + "' has cyclomatic complexity " + itoa(c) + " (threshold " + itoa(threshold) + ")",
	}
}
