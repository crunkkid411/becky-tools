// goana.go — exact Go analysis via go/parser + go/ast.
//
// For .go files we don't guess with regexes: we parse the file and walk the AST.
// This gives precise findings for five categories on Go source:
//
//	imports     — an imported package whose name is never referenced (the same
//	              rule the compiler enforces, but reported instead of fatal so a
//	              whole-repo scan keeps going).
//	dead-code   — unexported top-level functions never referenced anywhere in the
//	              file (a conservative same-file reachability check).
//	complexity  — McCabe count over the real statement tree, not a keyword guess.
//	docstrings  — exported funcs/types with no preceding doc comment.
//	deprecated  — calls to symbols on the deprecation list (selector exprs).
//
// Parsing failures degrade gracefully: the file falls back to the generic
// regex analyzers (handled by the caller), so a syntactically broken file never
// crashes the scan.
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
)

// readNormalized reads a file and converts its newlines to LF for consistent
// parsing across platforms.
func readNormalized(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return normalizeNewlines(string(data)), nil
}

// analyzeGo parses one Go file and returns findings for the requested
// categories. ok is false when parsing failed, telling the caller to fall back
// to the generic analyzers for that file. pkgRefs is the package-wide identifier
// reference count (built by collectPkgRefs across every file in the directory)
// so dead-code is judged across the whole package, not just one file.
func analyzeGo(sf sourceFile, src string, want map[string]bool, maxComplexity int, deprecated []string, pkgRefs map[string]int) (findings []Finding, ok bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, sf.path, src, parser.ParseComments)
	if err != nil {
		return nil, false
	}
	lineOf := func(p token.Pos) int { return fset.Position(p).Line }

	if want[catImports] {
		findings = append(findings, goUnusedImports(sf, file, lineOf)...)
	}
	if want[catComplexity] {
		findings = append(findings, goComplexity(sf, file, lineOf, maxComplexity)...)
	}
	if want[catDocstrings] {
		findings = append(findings, goMissingDocs(sf, file, lineOf)...)
	}
	if want[catDeadCode] {
		findings = append(findings, goDeadCode(sf, file, lineOf, pkgRefs)...)
	}
	if want[catDeprecated] {
		findings = append(findings, goDeprecated(sf, file, lineOf, deprecated)...)
	}
	return findings, true
}

// collectPkgRefs counts identifier occurrences across every Go file in a
// directory, so a helper used only from a sibling file is correctly seen as
// referenced. Parse failures for individual files are skipped. The count for a
// function's own declaration ident is included, so refs > 1 means "used".
func collectPkgRefs(dirFiles []sourceFile) map[string]int {
	refs := map[string]int{}
	fset := token.NewFileSet()
	for _, sf := range dirFiles {
		if sf.lang != langGo {
			continue
		}
		data, err := readNormalized(sf.path)
		if err != nil {
			continue
		}
		file, perr := parser.ParseFile(fset, sf.path, data, 0)
		if perr != nil {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				refs[id.Name]++
			}
			return true
		})
	}
	return refs
}

// goUnusedImports reports imports whose package identifier is never used. Blank
// (_) and dot (.) imports are intentionally side-effecting / wildcard, so they
// are exempt.
func goUnusedImports(sf sourceFile, file *ast.File, lineOf func(token.Pos) int) []Finding {
	used := usedSelectorBases(file)
	var findings []Finding
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		name := importLocalName(imp, path)
		if name == "_" || name == "." {
			continue
		}
		if used[name] {
			continue
		}
		findings = append(findings, Finding{
			Category: catImports,
			File:     sf.rel,
			Line:     lineOf(imp.Pos()),
			Severity: sevLow,
			Language: langGo,
			Symbol:   path,
			Source:   "pure-go",
			Fixable:  true,
			Fix:      "remove unused import (gofmt/goimports)",
			Message:  "imported package '" + path + "' is never used",
		})
	}
	return findings
}

// importLocalName returns the identifier an import binds: its alias, or the last
// path segment when unaliased.
func importLocalName(imp *ast.ImportSpec, path string) string {
	if imp.Name != nil {
		return imp.Name.Name
	}
	seg := path
	if i := strings.LastIndex(seg, "/"); i >= 0 {
		seg = seg[i+1:]
	}
	return seg
}

// usedSelectorBases collects every X in an X.Sel selector, a superset of package
// references — enough to tell whether an import is used at all.
func usedSelectorBases(file *ast.File) map[string]bool {
	used := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok {
				used[id.Name] = true
			}
		}
		return true
	})
	return used
}

// goComplexity computes cyclomatic complexity per function body from the AST.
func goComplexity(sf sourceFile, file *ast.File, lineOf func(token.Pos) int, maxComplexity int) []Finding {
	var findings []Finding
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		c := astComplexity(fn.Body)
		if c <= maxComplexity {
			continue
		}
		findings = append(findings, complexityFinding(sf, fn.Name.Name, lineOf(fn.Pos()), c, maxComplexity))
	}
	return findings
}

// astComplexity is McCabe complexity: 1 + decision points (if/for/range/case/
// &&/||).
func astComplexity(body *ast.BlockStmt) int {
	c := 1
	ast.Inspect(body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.CaseClause, *ast.CommClause:
			c++
		case *ast.BinaryExpr:
			if s.Op == token.LAND || s.Op == token.LOR {
				c++
			}
		}
		return true
	})
	return c
}

// goMissingDocs flags exported top-level funcs and type specs without a doc
// comment.
func goMissingDocs(sf sourceFile, file *ast.File, lineOf func(token.Pos) int) []Finding {
	var findings []Finding
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !d.Name.IsExported() {
				continue
			}
			if d.Doc != nil && len(d.Doc.List) > 0 {
				continue
			}
			// Methods implementing standard interfaces (Error/String/etc.) and
			// any method on an unexported type are idiomatically doc-free; don't
			// nag about them.
			if d.Recv != nil && (interfaceMethod[d.Name.Name] || !exportedReceiver(d.Recv)) {
				continue
			}
			findings = append(findings, Finding{
				Category: catDocstrings,
				File:     sf.rel,
				Line:     lineOf(d.Pos()),
				Severity: sevLow,
				Language: langGo,
				Symbol:   d.Name.Name,
				Source:   "pure-go",
				Message:  "exported function '" + d.Name.Name + "' has no doc comment",
			})
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			if d.Doc != nil && len(d.Doc.List) > 0 {
				continue
			}
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() || ts.Doc != nil {
					continue
				}
				findings = append(findings, Finding{
					Category: catDocstrings,
					File:     sf.rel,
					Line:     lineOf(ts.Pos()),
					Severity: sevLow,
					Language: langGo,
					Symbol:   ts.Name.Name,
					Source:   "pure-go",
					Message:  "exported type '" + ts.Name.Name + "' has no doc comment",
				})
			}
		}
	}
	return findings
}

// interfaceMethod names methods that satisfy ubiquitous stdlib interfaces and so
// don't warrant a per-method doc comment.
var interfaceMethod = map[string]bool{
	"Error": true, "String": true, "Read": true, "Write": true, "Close": true,
	"ServeHTTP": true, "MarshalJSON": true, "UnmarshalJSON": true, "Len": true,
	"Less": true, "Swap": true,
}

// exportedReceiver reports whether a method's receiver type is exported. A
// method on an unexported type is package-internal in practice.
func exportedReceiver(recv *ast.FieldList) bool {
	if recv == nil || len(recv.List) == 0 {
		return false
	}
	t := recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	id, ok := t.(*ast.Ident)
	if !ok {
		return false
	}
	return ast.IsExported(id.Name)
}

// goDeadCode flags unexported top-level functions that are never referenced
// anywhere in their package (pkgRefs counts idents across every file in the
// directory), so a helper used from a sibling file is correctly spared. The
// declaration's own ident is counted, so a package-wide count of 1 means the
// only occurrence is the declaration itself: genuinely dead.
func goDeadCode(sf sourceFile, file *ast.File, lineOf func(token.Pos) int, pkgRefs map[string]int) []Finding {
	var findings []Finding
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil { // skip methods: interface satisfaction etc.
			continue
		}
		name := fn.Name.Name
		if name == "main" || name == "init" || fn.Name.IsExported() {
			continue
		}
		if pkgRefs[name] > 1 { // referenced somewhere beyond its own declaration
			continue
		}
		findings = append(findings, Finding{
			Category: catDeadCode,
			File:     sf.rel,
			Line:     lineOf(fn.Pos()),
			Severity: sevLow,
			Language: langGo,
			Symbol:   name,
			Source:   "pure-go",
			Message:  "unexported function '" + name + "' is never referenced in its package",
		})
	}
	return findings
}

// goDeprecated flags selector-expression calls to deprecated symbols.
func goDeprecated(sf sourceFile, file *ast.File, lineOf func(token.Pos) int, extra []string) []Finding {
	symbols := map[string]bool{}
	for _, s := range builtinDeprecated[langGo] {
		symbols[s] = true
	}
	for _, s := range extra {
		symbols[s] = true
	}
	var findings []Finding
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		full := x.Name + "." + sel.Sel.Name
		if symbols[full] {
			findings = append(findings, Finding{
				Category: catDeprecated,
				File:     sf.rel,
				Line:     lineOf(sel.Pos()),
				Severity: sevMedium,
				Language: langGo,
				Symbol:   full,
				Source:   "pure-go",
				Message:  "uses deprecated symbol '" + full + "'",
			})
		}
		return true
	})
	return findings
}
