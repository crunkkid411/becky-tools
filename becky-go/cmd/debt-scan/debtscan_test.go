// debtscan_test.go — table-driven tests for the deterministic core: the
// complexity counter, TODO marker detection, duplicate-window hashing, and the
// naming-style classifier, plus a couple of end-to-end analyzer checks on small
// synthetic sources. These cover the load-bearing heuristics so a refactor can't
// silently change what counts as debt.
package main

import "testing"

// --- complexity counter ---

func TestCyclomatic(t *testing.T) {
	cases := []struct {
		name string
		body string
		lang string
		want int
	}{
		{"straight line", "x := 1\ny := 2\nreturn x + y", langGo, 1},
		{"one if", "if x > 0 { return 1 }\nreturn 0", langGo, 2},
		{"if and for", "for i := range xs {\n if i > 0 { do() }\n}", langGo, 3},
		{"short circuit", "if a && b || c { return 1 }", langGo, 4},
		{"switch cases", "switch x {\ncase 1:\nf()\ncase 2:\ng()\n}", langGo, 3},
		{"python elif", "if a:\n  p()\nelif b:\n  q()\nwhile c:\n  r()", langPython, 4},
		{"ternary ts", "const x = a ? b : c\nconst y = d ? e : f", langTS, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cyclomatic(maskComments(tc.body), tc.lang)
			if got != tc.want {
				t.Errorf("cyclomatic(%q) = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// Comments and strings must not inflate complexity.
func TestCyclomaticIgnoresCommentsAndStrings(t *testing.T) {
	body := `// if for while case
s := "if a && b || c"
return s`
	got := cyclomatic(maskComments(body), langGo)
	if got != 1 {
		t.Errorf("complexity with branch words only in comments/strings = %d, want 1", got)
	}
}

// --- TODO detection ---

func TestScanTODOs(t *testing.T) {
	cases := []struct {
		name      string
		lang      string
		src       string
		wantCount int
		wantLine  int
	}{
		{"go todo", langGo, "package p\n// TODO: fix this\nfunc f() {}", 1, 2},
		{"python fixme", langPython, "x = 1  # FIXME later\n", 1, 1},
		{"hack marker", langGo, "func f() {} // HACK around bug\n", 1, 1},
		{"no marker", langGo, "// just a comment\nfunc f() {}", 0, 0},
		{"todo in string not comment", langGo, `s := "TODO not a comment"` + "\n", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sf := sourceFile{rel: "x", lang: tc.lang}
			got := scanTODOs(sf, tc.src, 30, nil)
			if len(got) != tc.wantCount {
				t.Fatalf("scanTODOs found %d, want %d (%+v)", len(got), tc.wantCount, got)
			}
			if tc.wantCount > 0 && got[0].Line != tc.wantLine {
				t.Errorf("TODO line = %d, want %d", got[0].Line, tc.wantLine)
			}
		})
	}
}

// Stale ageing: a marker older than min-age is medium; younger is low.
func TestTODOStaleAge(t *testing.T) {
	sf := sourceFile{rel: "x", lang: langGo}
	src := "// TODO old\n// TODO new\n"
	ages := map[int]int{1: 90, 2: 5}
	got := scanTODOs(sf, src, 30, ages)
	if len(got) != 2 {
		t.Fatalf("want 2 findings, got %d", len(got))
	}
	if got[0].Severity != sevMedium || got[0].AgeDays != 90 {
		t.Errorf("old TODO: sev=%s age=%d, want medium/90", got[0].Severity, got[0].AgeDays)
	}
	if got[1].Severity != sevLow || got[1].AgeDays != 5 {
		t.Errorf("new TODO: sev=%s age=%d, want low/5", got[1].Severity, got[1].AgeDays)
	}
}

// --- duplicate hashing ---

func TestHashWindowStableAndDistinct(t *testing.T) {
	a := []normalizedLine{{text: "alpha", line: 1}, {text: "beta", line: 2}}
	b := []normalizedLine{{text: "alpha", line: 9}, {text: "beta", line: 10}}
	c := []normalizedLine{{text: "alpha", line: 1}, {text: "gamma", line: 2}}
	if hashWindow(a) != hashWindow(b) {
		t.Error("same text, different line numbers should hash equal")
	}
	if hashWindow(a) == hashWindow(c) {
		t.Error("different text should hash differently")
	}
}

func TestScanDupesDetectsRepeatedBlock(t *testing.T) {
	// Two identical 6-line blocks separated by a unique block.
	block := "alpha := 1\nbeta := 2\ngamma := 3\ndelta := 4\nepsilon := 5\nzeta := 6\n"
	src := block + "unique1 := 11\nunique2 := 12\n" + block
	sf := sourceFile{rel: "dup.go", lang: langGo}
	seen := map[string]blockOccurrence{}
	got := scanDupes(sf, src, seen)
	if len(got) == 0 {
		t.Fatal("expected a duplicate finding for the repeated block")
	}
	if got[0].DupWith == "" {
		t.Error("duplicate finding should carry dup_with reference")
	}
}

func TestScanDupesIgnoresBoilerplate(t *testing.T) {
	// A run of near-identical map entries must not be flagged as self-duplicate.
	src := "var m = map[string]bool{\n" +
		"a: true,\nb: true,\nc: true,\nd: true,\ne: true,\nf: true,\ng: true,\n}\n"
	sf := sourceFile{rel: "m.go", lang: langGo}
	got := scanDupes(sf, src, map[string]blockOccurrence{})
	if len(got) != 0 {
		t.Errorf("boilerplate map entries flagged as dupes: %d findings", len(got))
	}
}

// --- naming classifier ---

func TestClassifyStyle(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"snake_case", "snake"},
		{"camelCase", "camel"},
		{"PascalCase", "camel"},
		{"lowercase", ""},
		{"CONSTANT_CASE", ""},
		{"x", ""},
		{"my_var2", "snake"},
		{"getHTTPResponse", "camel"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyStyle(tc.name); got != tc.want {
				t.Errorf("classifyStyle(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestScanNamingMixedFile(t *testing.T) {
	// A Python file that is mostly snake_case but has one camelCase def.
	src := "def good_name():\n  pass\ndef another_good():\n  pass\ndef badName():\n  pass\n"
	sf := sourceFile{rel: "x.py", lang: langPython}
	got := scanNaming(sf, maskComments(src))
	found := false
	for _, f := range got {
		if f.Symbol == "badName" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected camelCase 'badName' flagged in snake_case file; got %+v", got)
	}
}

// --- comment masking ---

func TestMaskComments(t *testing.T) {
	src := `code // comment with "quote"
"a string literal"
/* block */ more`
	masked := maskComments(src)
	if len(masked) != len(src) {
		t.Fatalf("mask changed length: %d vs %d", len(masked), len(src))
	}
	// The branch-word-bearing comment/string content must be gone.
	if got := cyclomatic(masked, langGo); got != 1 {
		t.Errorf("masked complexity = %d, want 1", got)
	}
}

// --- Go AST analysis end-to-end ---

func TestAnalyzeGoUnusedImport(t *testing.T) {
	src := `package p

import (
	"fmt"
	"strings"
)

func used() string { return fmt.Sprintf("%d", 1) }
`
	sf := sourceFile{rel: "p.go", path: "p.go", lang: langGo}
	want := map[string]bool{catImports: true, catComplexity: true, catDocstrings: true}
	findings, ok := analyzeGo(sf, src, want, 10, nil, map[string]int{})
	if !ok {
		t.Fatal("analyzeGo failed to parse valid source")
	}
	var sawUnused bool
	for _, f := range findings {
		if f.Category == catImports && f.Symbol == "strings" {
			sawUnused = true
		}
	}
	if !sawUnused {
		t.Errorf("expected unused import 'strings' flagged; got %+v", findings)
	}
}

func TestAnalyzeGoDeadCodePackageWide(t *testing.T) {
	src := `package p

func helper() int { return 1 }

func caller() int { return helper() }
`
	sf := sourceFile{rel: "p.go", path: "p.go", lang: langGo}
	// pkgRefs says helper is referenced (count 2: decl + call), caller once.
	pkgRefs := map[string]int{"helper": 2, "caller": 1}
	findings, ok := analyzeGo(sf, src, map[string]bool{catDeadCode: true}, 10, nil, pkgRefs)
	if !ok {
		t.Fatal("parse failed")
	}
	for _, f := range findings {
		if f.Symbol == "helper" {
			t.Errorf("helper is referenced package-wide and must not be flagged dead")
		}
	}
}

// --- config YAML parsing ---

func TestParseDebtYAML(t *testing.T) {
	src := `min_age: 45
max_complexity: 8
languages: [go, python]
ci_severity: high
exclude:
  - generated/
  - "_test.go"
`
	cfg := parseDebtYAML(src)
	if cfg.MinAge == nil || *cfg.MinAge != 45 {
		t.Errorf("min_age = %v, want 45", cfg.MinAge)
	}
	if cfg.MaxComplexity == nil || *cfg.MaxComplexity != 8 {
		t.Errorf("max_complexity = %v, want 8", cfg.MaxComplexity)
	}
	if len(cfg.Languages) != 2 || cfg.Languages[0] != "go" {
		t.Errorf("languages = %v, want [go python]", cfg.Languages)
	}
	if cfg.CISeverity != "high" {
		t.Errorf("ci_severity = %q, want high", cfg.CISeverity)
	}
	if len(cfg.Exclude) != 2 {
		t.Errorf("exclude = %v, want 2 items", cfg.Exclude)
	}
}

// --- deprecated boundary check ---

func TestScanDeprecatedBoundary(t *testing.T) {
	// "import sherpa_onnx" must NOT match deprecated 'imp' inside "import".
	noHit := "import sherpa_onnx\n"
	sf := sourceFile{rel: "x.py", lang: langPython}
	if got := scanDeprecated(sf, noHit, nil); len(got) != 0 {
		t.Errorf("'import' falsely matched deprecated 'imp': %+v", got)
	}
	// A real dotted use of imp should match.
	hit := "x = imp.load_module('m')\n"
	if got := scanDeprecated(sf, hit, nil); len(got) != 1 {
		t.Errorf("real 'imp.' use not flagged: %+v", got)
	}
}
