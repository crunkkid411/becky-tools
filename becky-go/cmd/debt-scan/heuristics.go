// heuristics.go — categories 2,4,5,9 for the non-Go languages plus the shared
// deprecated-symbol scan. (Go's unused imports / dead code / missing docs come
// from go/ast in goana.go, which is exact.)
//
//	imports     — an imported name never referenced in the rest of the file.
//	types       — Python function parameters / returns with no type annotation.
//	deprecated  — calls to symbols matching a deprecation list (built-ins + config).
//	docstrings  — exported/public functions with no leading doc comment/docstring.
package main

import (
	"regexp"
	"strings"
)

// --- category 2: unused imports (Python / TS / JS / Rust heuristic) ---

var pyImport = regexp.MustCompile(`(?m)^\s*(?:from\s+[.\w]+\s+import\s+(.+)|import\s+(.+))$`)
var tsImport = regexp.MustCompile(`(?m)^\s*import\s+(?:type\s+)?(.+?)\s+from\s+['"][^'"]+['"]`)
var rustUse = regexp.MustCompile(`(?m)^\s*use\s+([\w:]+(?:::\{[^}]*\})?)\s*;`)

// scanImports flags imported names that never appear again in the file body.
// Conservative: only the simple, unambiguous import shapes are checked; anything
// it cannot parse cleanly is skipped (reported as nothing, never a false fix).
func scanImports(sf sourceFile, raw, masked string) []Finding {
	switch sf.lang {
	case langPython:
		return unusedFrom(sf, masked, pyImport, splitPyNames)
	case langTS, langJS:
		return unusedFrom(sf, masked, tsImport, splitTSNames)
	case langRust:
		return unusedFrom(sf, masked, rustUse, splitRustNames)
	default:
		return nil
	}
}

// unusedFrom is the shared engine: for each import statement it extracts the
// bound names via split, then flags any name not referenced elsewhere in the
// comment-masked body.
func unusedFrom(sf sourceFile, masked string, pat *regexp.Regexp, split func(string) []string) []Finding {
	var findings []Finding
	for _, m := range pat.FindAllStringSubmatchIndex(masked, -1) {
		clause := ""
		for g := 1; g*2+1 < len(m); g++ {
			if m[g*2] >= 0 {
				clause = masked[m[g*2]:m[g*2+1]]
				break
			}
		}
		line := byteToLine(masked, m[0])
		stmtSpan := masked[m[0]:m[1]]
		// Body with this import statement blanked, to count later references.
		body := masked[:m[0]] + strings.Repeat(" ", len(stmtSpan)) + masked[m[1]:]
		for _, name := range split(clause) {
			if name == "" || name == "*" {
				continue
			}
			if wordBoundaryContains(body, name) {
				continue
			}
			findings = append(findings, Finding{
				Category: catImports,
				File:     sf.rel,
				Line:     line,
				Severity: sevLow,
				Language: sf.lang,
				Symbol:   name,
				Source:   "pure-go",
				Message:  "imported '" + name + "' is never used in this file",
			})
		}
	}
	return findings
}

// splitPyNames extracts bound names from a Python import clause, honoring `as`.
func splitPyNames(clause string) []string {
	clause = strings.Trim(strings.TrimSpace(clause), "()")
	var out []string
	for _, part := range strings.Split(clause, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if i := strings.Index(part, " as "); i >= 0 {
			part = strings.TrimSpace(part[i+4:])
		}
		// `import a.b.c` binds `a`.
		if i := strings.Index(part, "."); i >= 0 {
			part = part[:i]
		}
		out = append(out, part)
	}
	return out
}

// splitTSNames extracts names from a TS/JS import clause (default, namespace,
// and named { a, b as c } forms).
func splitTSNames(clause string) []string {
	clause = strings.TrimSpace(clause)
	var out []string
	if i := strings.Index(clause, "{"); i >= 0 {
		if j := strings.Index(clause, "}"); j > i {
			for _, part := range strings.Split(clause[i+1:j], ",") {
				part = strings.TrimSpace(part)
				if k := strings.Index(part, " as "); k >= 0 {
					part = strings.TrimSpace(part[k+4:])
				}
				if part != "" {
					out = append(out, part)
				}
			}
		}
		clause = strings.TrimSpace(clause[:i])
		clause = strings.TrimRight(clause, ", ")
	}
	if strings.HasPrefix(clause, "* as ") {
		out = append(out, strings.TrimSpace(strings.TrimPrefix(clause, "* as ")))
	} else if clause != "" && !strings.ContainsAny(clause, "{}*") {
		out = append(out, strings.TrimSpace(clause))
	}
	return out
}

// splitRustNames extracts the leaf name(s) from a `use` path.
func splitRustNames(clause string) []string {
	clause = strings.TrimSpace(clause)
	if i := strings.Index(clause, "::{"); i >= 0 {
		inner := clause[i+3:]
		inner = strings.TrimSuffix(inner, "}")
		var out []string
		for _, part := range strings.Split(inner, ",") {
			part = strings.TrimSpace(part)
			if part == "self" || part == "" {
				continue
			}
			if k := strings.Index(part, " as "); k >= 0 {
				part = strings.TrimSpace(part[k+4:])
			}
			out = append(out, part)
		}
		return out
	}
	segs := strings.Split(clause, "::")
	last := strings.TrimSpace(segs[len(segs)-1])
	if last == "*" || last == "" {
		return nil
	}
	return []string{last}
}

// --- category 4: missing type hints (Python) ---

var pyDefSig = regexp.MustCompile(`(?m)^\s*(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(([^)]*)\)\s*(->\s*[^:]+)?:`)

// scanTypes flags Python functions whose parameters or return lack annotations.
// Only Python is checked here; Go/Rust are statically typed; TS-without-types is
// a deeper analysis we leave to tsc (see externals).
func scanTypes(sf sourceFile, masked string) []Finding {
	if sf.lang != langPython {
		return nil
	}
	var findings []Finding
	for _, m := range pyDefSig.FindAllStringSubmatchIndex(masked, -1) {
		name := masked[m[2]:m[3]]
		params := masked[m[4]:m[5]]
		hasReturn := m[6] >= 0
		line := byteToLine(masked, m[0])
		missing := pyMissingParamTypes(params)
		if len(missing) == 0 && hasReturn {
			continue
		}
		if name == "__init__" && len(missing) == 0 {
			continue // __init__ rarely annotates -> None; don't nag
		}
		var parts []string
		if len(missing) > 0 {
			parts = append(parts, "params "+strings.Join(missing, ", "))
		}
		if !hasReturn {
			parts = append(parts, "return")
		}
		findings = append(findings, Finding{
			Category: catTypes,
			File:     sf.rel,
			Line:     line,
			Severity: sevLow,
			Language: sf.lang,
			Symbol:   name,
			Source:   "pure-go",
			Message:  "function '" + name + "' missing type hints: " + strings.Join(parts, "; "),
		})
	}
	return findings
}

// pyMissingParamTypes returns the names of parameters that lack a `: type`.
func pyMissingParamTypes(params string) []string {
	var missing []string
	for _, p := range strings.Split(params, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		base := p
		if i := strings.Index(base, "="); i >= 0 { // strip default
			base = strings.TrimSpace(base[:i])
		}
		name := strings.TrimLeft(base, "*") // *args / **kwargs
		if name == "self" || name == "cls" || name == "" {
			continue
		}
		if !strings.Contains(base, ":") {
			missing = append(missing, strings.TrimLeft(strings.Split(name, " ")[0], "*"))
		}
	}
	return missing
}

// --- category 5: deprecated functions ---

// builtinDeprecated are well-known deprecated/dangerous calls across the
// supported languages. Extendable via config.Deprecated.
var builtinDeprecated = map[string][]string{
	langPython: {"imp", "asyncio.coroutine", "collections.Mapping", "os.popen2", "cgi.escape"},
	langGo:     {"ioutil.ReadFile", "ioutil.WriteFile", "ioutil.ReadAll", "ioutil.Discard", "ioutil.NopCloser", "ioutil.TempFile", "ioutil.TempDir"},
	langTS:     {"componentWillMount", "componentWillReceiveProps", "substr"},
	langJS:     {"componentWillMount", "componentWillReceiveProps", "substr"},
	langRust:   {"std::mem::uninitialized", "try!"},
}

// scanDeprecated flags references to deprecated symbols (whole-word) in the body.
func scanDeprecated(sf sourceFile, masked string, extra []string) []Finding {
	symbols := append([]string{}, builtinDeprecated[sf.lang]...)
	symbols = append(symbols, extra...)
	if len(symbols) == 0 {
		return nil
	}
	var findings []Finding
	lines := splitLines(masked)
	for i, line := range lines {
		for _, sym := range symbols {
			if sym == "" || !strings.Contains(line, sym) {
				continue
			}
			if !symbolPresent(line, sym) { // boundary check (handles dotted names)
				continue
			}
			findings = append(findings, Finding{
				Category: catDeprecated,
				File:     sf.rel,
				Line:     i + 1,
				Severity: sevMedium,
				Language: sf.lang,
				Symbol:   sym,
				Source:   "pure-go",
				Message:  "uses deprecated symbol '" + sym + "'",
			})
		}
	}
	return findings
}

// symbolPresent checks that sym appears with non-identifier boundaries on both
// sides (so `myioutil` doesn't match `ioutil`, and `import` doesn't match `imp`).
// A trailing `.` is allowed because dotted calls like `imp.load` are real uses;
// a trailing identifier rune (as in `import`) is not.
func symbolPresent(line, sym string) bool {
	idx := 0
	for {
		j := strings.Index(line[idx:], sym)
		if j < 0 {
			return false
		}
		start := idx + j
		end := start + len(sym)
		beforeOK := start == 0 || !isIdentRune(rune(line[start-1]))
		afterOK := end >= len(line) || !isIdentRune(rune(line[end]))
		if beforeOK && afterOK {
			return true
		}
		idx = start + 1
		if idx >= len(line) {
			return false
		}
	}
}

// --- category 9: missing docstrings (non-Go) ---

// pubFn detects public declarations that should carry docs in Rust/TS/JS.
var pubFn = regexp.MustCompile(`(?m)^\s*(pub(?:\([^)]*\))?\s+fn|export\s+(?:default\s+)?(?:async\s+)?function|export\s+(?:default\s+)?class)\s+`)

// scanDocstrings flags public/exported functions lacking a leading doc comment
// (Rust ///, TS/JS /** or // immediately above; Python a """ first statement).
func scanDocstrings(sf sourceFile, raw, masked string) []Finding {
	switch sf.lang {
	case langPython:
		return pyMissingDocstrings(sf, raw, masked)
	case langRust, langTS, langJS:
		return braceMissingDocstrings(sf, raw, masked)
	default:
		return nil
	}
}

// pyMissingDocstrings flags public (non-underscore) defs whose first body line
// isn't a string literal.
func pyMissingDocstrings(sf sourceFile, raw, masked string) []Finding {
	rawLines := splitLines(raw)
	var findings []Finding
	for _, m := range pyDef.FindAllStringSubmatchIndex(masked, -1) {
		name := masked[m[4]:m[5]]
		if strings.HasPrefix(name, "_") {
			continue // private/dunder: don't require docs
		}
		defLine := byteToLine(masked, m[0])
		first := firstBodyLine(rawLines, defLine)
		if first == "" || strings.HasPrefix(first, `"""`) || strings.HasPrefix(first, "'''") ||
			strings.HasPrefix(first, `r"""`) || strings.HasPrefix(first, `"`) {
			continue
		}
		findings = append(findings, docstringFinding(sf, name, defLine))
	}
	return findings
}

// braceMissingDocstrings flags public functions/classes with no doc comment on
// the line(s) immediately above the declaration.
func braceMissingDocstrings(sf sourceFile, raw, masked string) []Finding {
	rawLines := splitLines(raw)
	var findings []Finding
	for _, m := range pubFn.FindAllStringIndex(masked, -1) {
		declLine := byteToLine(masked, m[0])
		name := declName(rawLines, declLine)
		if hasDocAbove(rawLines, declLine) {
			continue
		}
		findings = append(findings, docstringFinding(sf, name, declLine))
	}
	return findings
}

// firstBodyLine returns the trimmed first non-blank line strictly after defLine
// (1-based), used to check for a Python docstring.
func firstBodyLine(lines []string, defLine int) string {
	for i := defLine; i < len(lines); i++ { // lines[defLine] is the line after defLine
		t := strings.TrimSpace(lines[i])
		if t == "" {
			continue
		}
		if strings.HasSuffix(t, "(") || strings.HasSuffix(t, ",") || strings.HasSuffix(t, "->") {
			continue // continued signature line
		}
		return t
	}
	return ""
}

// hasDocAbove reports whether the line directly above declLine (1-based) is a
// doc comment.
func hasDocAbove(lines []string, declLine int) bool {
	if declLine < 2 {
		return false
	}
	prev := strings.TrimSpace(lines[declLine-2])
	return strings.HasPrefix(prev, "///") || strings.HasPrefix(prev, "//!") ||
		strings.HasPrefix(prev, "*/") || strings.HasPrefix(prev, "*") ||
		strings.HasPrefix(prev, "/**") || strings.HasPrefix(prev, "//")
}

// declName pulls the declared name from a declaration line, best-effort.
func declName(lines []string, declLine int) string {
	if declLine-1 >= len(lines) || declLine < 1 {
		return ""
	}
	m := identAfterKeyword.FindStringSubmatch(lines[declLine-1])
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

var identAfterKeyword = regexp.MustCompile(`(?:fn|function|class)\s+([A-Za-z_$][A-Za-z0-9_$]*)`)

func docstringFinding(sf sourceFile, name string, line int) Finding {
	msg := "exported function/class"
	if name != "" {
		msg = "exported '" + name + "'"
	}
	return Finding{
		Category: catDocstrings,
		File:     sf.rel,
		Line:     line,
		Severity: sevLow,
		Language: sf.lang,
		Symbol:   name,
		Source:   "pure-go",
		Message:  msg + " has no doc comment/docstring",
	}
}
