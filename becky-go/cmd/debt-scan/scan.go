// scan.go — the orchestrator: walk the tree, run every requested analyzer over
// each file, merge in external enrichment, and assemble the Report. main.go
// owns flag parsing and output; this file owns the actual scan.
package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"becky-go/internal/beckyio"
)

const toolVersion = "becky-debt-scan v1.0.0"

// scanOptions are the resolved settings for one scan (flags + config merged).
type scanOptions struct {
	root          string          // absolute scan root
	languages     map[string]bool // empty => all
	categories    map[string]bool // which analyzers to run
	categoryList  []string        // ordered list for the report
	minAge        int
	maxComplexity int
	since         string
	exclude       []string
	deprecated    []string
	verbose       bool
}

// runScan performs the whole scan and returns the assembled Report. Fatal I/O
// problems (root unreadable) are surfaced as an error; per-file problems are
// noted and skipped so one bad file never aborts the run.
func runScan(opts scanOptions) (Report, error) {
	notes := map[string]string{}
	now := time.Now()

	gi := detectGit(opts.root)

	files, err := walkSources(opts.root, opts.languages)
	if err != nil {
		return Report{}, err
	}
	files = applyExcludes(files, opts.exclude)

	// --since restricts to changed files (best-effort; degrade with a note).
	if opts.since != "" {
		files = restrictToSince(files, gi, opts.since, notes, opts.verbose)
	}
	if !gi.available {
		notes["git"] = "scan root is not a git repository: TODO ages unavailable; --since ignored"
	}

	beckyio.Logf(opts.verbose, "scanning %d file(s) under %s", len(files), opts.root)

	// Package-wide Go reference index per directory, so dead-code is judged
	// across the whole package (a helper used from a sibling file is not flagged).
	goByDir := groupGoByDir(files)
	pkgRefsCache := map[string]map[string]int{}

	var findings []Finding
	dupSeen := map[string]blockOccurrence{} // shared across files for cross-file dupes

	for _, sf := range files {
		data, rerr := os.ReadFile(sf.path)
		if rerr != nil {
			beckyio.Logf(opts.verbose, "  skip %s: %v", sf.rel, rerr)
			continue
		}
		src := normalizeNewlines(string(data))
		var pkgRefs map[string]int
		if sf.lang == langGo && opts.categories[catDeadCode] {
			dir := filepath.Dir(sf.path)
			if cached, ok := pkgRefsCache[dir]; ok {
				pkgRefs = cached
			} else {
				pkgRefs = collectPkgRefs(goByDir[dir])
				pkgRefsCache[dir] = pkgRefs
			}
		}
		findings = append(findings, scanFile(sf, src, opts, gi, now, dupSeen, pkgRefs)...)
	}

	// External enrichment (additive; records skipped tools as notes).
	langs := detectedLanguages(files)
	extFindings, extNotes := runExternals(opts.root, langs, opts.categories, opts.verbose)
	findings = append(findings, filterExternalByCategory(extFindings, opts.categories)...)
	for k, v := range extNotes {
		notes[k] = v
	}

	sortFindings(findings)

	report := Report{
		Tool:              toolVersion,
		Path:              filepath.ToSlash(opts.root),
		LanguagesDetected: langs,
		FilesScanned:      len(files),
		Categories:        opts.categoryList,
		Findings:          findings,
		Summary:           newSummary(findings),
		Notes:             notes,
	}
	return report, nil
}

// scanFile runs every requested analyzer over one file and returns its findings.
func scanFile(sf sourceFile, src string, opts scanOptions, gi gitInfo, now time.Time, dupSeen map[string]blockOccurrence, pkgRefs map[string]int) []Finding {
	masked := maskComments(src)
	var out []Finding
	want := opts.categories

	if want[catTODO] {
		var ages map[int]int
		if gi.available {
			ages = blameAges(gi.root, sf.path, now)
		}
		out = append(out, scanTODOs(sf, src, opts.minAge, ages)...)
	}

	// Go gets exact AST analysis for these five; fall back to regex on parse fail.
	goHandled := map[string]bool{}
	if sf.lang == langGo {
		want5 := map[string]bool{
			catImports: want[catImports], catComplexity: want[catComplexity],
			catDocstrings: want[catDocstrings], catDeadCode: want[catDeadCode],
			catDeprecated: want[catDeprecated],
		}
		if astFindings, ok := analyzeGo(sf, src, want5, opts.maxComplexity, opts.deprecated, pkgRefs); ok {
			out = append(out, astFindings...)
			for c, on := range want5 {
				if on {
					goHandled[c] = true
				}
			}
		}
	}

	if want[catImports] && !goHandled[catImports] {
		out = append(out, scanImports(sf, src, masked)...)
	}
	if want[catTypes] {
		out = append(out, scanTypes(sf, masked)...)
	}
	if want[catDeprecated] && !goHandled[catDeprecated] {
		out = append(out, scanDeprecated(sf, masked, opts.deprecated)...)
	}
	if want[catNaming] {
		out = append(out, scanNaming(sf, masked)...)
	}
	if want[catComplexity] && !goHandled[catComplexity] {
		out = append(out, scanComplexity(sf, src, masked, opts.maxComplexity)...)
	}
	if want[catDupes] {
		out = append(out, scanDupes(sf, src, dupSeen)...)
	}
	if want[catDocstrings] && !goHandled[catDocstrings] {
		out = append(out, scanDocstrings(sf, src, masked)...)
	}
	return out
}

// groupGoByDir buckets Go source files by their containing directory so the
// package-wide reference index can be built per package.
func groupGoByDir(files []sourceFile) map[string][]sourceFile {
	byDir := map[string][]sourceFile{}
	for _, f := range files {
		if f.lang != langGo {
			continue
		}
		dir := filepath.Dir(f.path)
		byDir[dir] = append(byDir[dir], f)
	}
	return byDir
}

// restrictToSince narrows files to those changed since opts.since, degrading
// gracefully (full set + note) when git can't answer.
func restrictToSince(files []sourceFile, gi gitInfo, since string, notes map[string]string, verbose bool) []sourceFile {
	allow, err := changedFiles(gi, since)
	if err != nil {
		notes["since"] = "--since " + since + " ignored: " + err.Error() + " (scanned full tree)"
		beckyio.Logf(verbose, "since: %v (scanning full tree)", err)
		return files
	}
	narrowed := filterByPaths(files, allow)
	notes["since"] = "limited to " + itoa(len(narrowed)) + " file(s) changed since " + since
	beckyio.Logf(verbose, "since %s: %d changed file(s)", since, len(narrowed))
	return narrowed
}

// applyExcludes drops files whose relative path contains any exclude substring.
func applyExcludes(files []sourceFile, exclude []string) []sourceFile {
	if len(exclude) == 0 {
		return files
	}
	var kept []sourceFile
	for _, f := range files {
		skip := false
		for _, ex := range exclude {
			if ex != "" && strings.Contains(f.rel, strings.TrimSuffix(ex, "*")) {
				skip = true
				break
			}
		}
		if !skip {
			kept = append(kept, f)
		}
	}
	return kept
}

// filterExternalByCategory keeps only external findings whose category is wanted.
func filterExternalByCategory(findings []Finding, want map[string]bool) []Finding {
	var kept []Finding
	for _, f := range findings {
		if want[f.Category] {
			kept = append(kept, f)
		}
	}
	return kept
}

// sortFindings orders findings deterministically: file, then line, then category.
func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Category != b.Category {
			return a.Category < b.Category
		}
		return a.Message < b.Message
	})
}

// normalizeNewlines converts CRLF/CR to LF so line numbers and offsets are
// consistent regardless of how the file was saved on Windows.
func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// maxSeverityRank returns the highest severity rank among findings (0 if none).
func maxSeverityRank(findings []Finding) int {
	max := 0
	for _, f := range findings {
		if r := severityRank(f.Severity); r > max {
			max = r
		}
	}
	return max
}
