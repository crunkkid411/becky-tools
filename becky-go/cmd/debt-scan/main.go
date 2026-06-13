// becky-debt-scan — deterministic technical-debt scanner. It walks a repo and
// flags nine categories of debt (stale TODOs, unused imports, dead code, missing
// type hints, deprecated functions, naming inconsistencies, high cyclomatic
// complexity, duplicate code blocks, missing docstrings) with exact file:line
// references, then emits a single JSON document to stdout.
//
//	becky-debt-scan [path] [options]
//
// Options:
//
//	--output text|json|markdown   output format (default: json)
//	--languages LIST              restrict to languages (default: auto-detect)
//	--categories LIST             restrict to a subset of the 9 categories
//	--min-age DAYS                min age for a TODO to count as stale (default: 30)
//	--max-complexity N            cyclomatic complexity threshold (default: 10)
//	--since GIT_REF               only scan files changed since the ref
//	--config FILE                 path to a .becky-debt.yaml
//	--fix                         apply safe autofixes (conservative: gofmt -w)
//	--fix-dry-run                 list safe fixes without changing anything
//	--ci                          exit 1 when findings reach the threshold
//	--ci-severity LEVEL           CI threshold severity (default: low)
//	--verbose                     progress on stderr
//
// No LLM, no network: same input -> same output. JSON to stdout, diagnostics to
// stderr, exit 0 on success (non-zero only on a real error, or under --ci when
// the threshold is met). Reuses internal/beckyio for all output.
package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/beckyio"
)

func main() {
	output := flag.String("output", "json", "output format: text, json, markdown")
	languages := flag.String("languages", "", "comma-separated languages to scan (default: auto-detect)")
	categories := flag.String("categories", "", "comma-separated categories (default: all)")
	minAge := flag.Int("min-age", 30, "minimum TODO age in days to count as stale")
	maxComplexity := flag.Int("max-complexity", 10, "cyclomatic complexity threshold")
	since := flag.String("since", "", "only scan files changed since this git ref")
	configPath := flag.String("config", "", "path to a .becky-debt.yaml")
	doFix := flag.Bool("fix", false, "apply safe autofixes (conservative)")
	dryRun := flag.Bool("fix-dry-run", false, "list safe fixes without changing files")
	ci := flag.Bool("ci", false, "exit non-zero when findings reach the CI threshold")
	ciSeverity := flag.String("ci-severity", "low", "CI threshold severity: low, medium, high, critical")
	verbose := flag.Bool("verbose", false, "show progress on stderr")

	path := parsePositional()
	if path == "" {
		path = "."
	}
	root, err := filepath.Abs(path)
	if err != nil {
		beckyio.Fatalf("resolve path %q: %v", path, err)
	}
	if st, serr := os.Stat(root); serr != nil || !st.IsDir() {
		beckyio.Fatalf("path is not a readable directory: %s", root)
	}

	format := strings.ToLower(*output)
	if format != "json" && format != "text" && format != "markdown" {
		beckyio.Fatalf("unknown --output %q (use json, text, or markdown)", *output)
	}

	// Load optional config and merge: CLI flags win, config fills the gaps.
	fileCfg := loadConfigFile(*configPath, root, *verbose)

	opts, ciSevWanted := buildOptions(buildArgs{
		root:          root,
		languages:     *languages,
		categories:    *categories,
		minAge:        *minAge,
		maxComplexity: *maxComplexity,
		since:         *since,
		ciSeverity:    *ciSeverity,
		verbose:       *verbose,
		cfg:           fileCfg,
	})

	report, rerr := runScan(opts)
	if rerr != nil {
		beckyio.Fatalf("scan failed: %v", rerr)
	}

	// Fix handling (dry-run never writes; --fix runs gofmt on eligible files).
	if *dryRun || *doFix {
		plan, targets := planFixes(report.Findings, mustWalk(opts))
		if *dryRun {
			report.FixesPlanned = plan
			beckyio.Logf(*verbose, "fix-dry-run: %d planned action(s)", len(plan))
		}
		if *doFix {
			report.FixesApplied = applyFixes(targets)
			beckyio.Logf(*verbose, "fix: applied %d action(s)", len(report.FixesApplied))
		}
	}

	emit(report, format)

	// CI gate: exit 1 if any finding's severity reaches the threshold.
	if *ci {
		if report.Summary.Total > 0 && maxSeverityRank(report.Findings) >= severityRank(ciSevWanted) {
			beckyio.Logf(true, "ci: %d finding(s) at or above %s — failing", report.Summary.Total, ciSevWanted)
			os.Exit(1)
		}
		beckyio.Logf(*verbose, "ci: below threshold (%s) — passing", ciSevWanted)
	}
}

// buildArgs bundles the raw flag values for option resolution.
type buildArgs struct {
	root          string
	languages     string
	categories    string
	minAge        int
	maxComplexity int
	since         string
	ciSeverity    string
	verbose       bool
	cfg           debtConfig
}

// buildOptions resolves flags + config into scanOptions and the CI severity.
func buildOptions(a buildArgs) (scanOptions, string) {
	minAge := a.minAge
	if isFlagDefault("min-age") && a.cfg.MinAge != nil {
		minAge = *a.cfg.MinAge
	}
	maxComplexity := a.maxComplexity
	if isFlagDefault("max-complexity") && a.cfg.MaxComplexity != nil {
		maxComplexity = *a.cfg.MaxComplexity
	}

	langList := splitCSV(a.languages)
	if len(langList) == 0 {
		langList = a.cfg.Languages
	}
	catList := splitCSV(a.categories)
	if len(catList) == 0 {
		catList = a.cfg.Categories
	}
	catList = normalizeCategories(catList)

	ciSev := strings.ToLower(a.ciSeverity)
	if isFlagDefault("ci-severity") && a.cfg.CISeverity != "" {
		ciSev = strings.ToLower(a.cfg.CISeverity)
	}

	opts := scanOptions{
		root:          a.root,
		languages:     normalizeLanguages(langList),
		categories:    stringSet(catList),
		categoryList:  catList,
		minAge:        minAge,
		maxComplexity: maxComplexity,
		since:         a.since,
		exclude:       a.cfg.Exclude,
		deprecated:    a.cfg.Deprecated,
		verbose:       a.verbose,
	}
	return opts, ciSev
}

// normalizeCategories validates and orders the category list; an empty input
// expands to every category.
func normalizeCategories(cats []string) []string {
	if len(cats) == 0 {
		return append([]string{}, allCategories...)
	}
	valid := stringSet(allCategories)
	var out []string
	for _, c := range allCategories { // keep canonical order
		if contains(cats, c) && valid[c] {
			out = append(out, c)
		}
	}
	if len(out) == 0 { // user passed only unknown names: fall back to all
		return append([]string{}, allCategories...)
	}
	return out
}

// normalizeLanguages maps friendly aliases (ts/js/py) to canonical names and
// returns a set; empty input means "all".
func normalizeLanguages(langs []string) map[string]bool {
	if len(langs) == 0 {
		return map[string]bool{}
	}
	alias := map[string]string{
		"ts": langTS, "typescript": langTS,
		"js": langJS, "javascript": langJS,
		"py": langPython, "python": langPython,
		"go": langGo, "golang": langGo,
		"rs": langRust, "rust": langRust,
	}
	set := map[string]bool{}
	for _, l := range langs {
		l = strings.ToLower(strings.TrimSpace(l))
		if canon, ok := alias[l]; ok {
			set[canon] = true
		}
	}
	return set
}

// loadConfigFile finds and loads a .becky-debt.yaml. With no --config it looks
// for one at the scan root. Parse errors are noted to stderr, never fatal.
func loadConfigFile(configPath, root string, verbose bool) debtConfig {
	path := configPath
	if path == "" {
		candidate := filepath.Join(root, ".becky-debt.yaml")
		if _, err := os.Stat(candidate); err == nil {
			path = candidate
		}
	}
	if path == "" {
		return debtConfig{}
	}
	cfg, err := loadDebtConfig(path)
	if err != nil {
		beckyio.Logf(true, "warning: could not read config %s: %v", path, err)
		return debtConfig{}
	}
	beckyio.Logf(verbose, "loaded config: %s", path)
	return cfg
}

// mustWalk re-walks the (already-validated) tree for the fix engine's rel->path
// map. Cheap relative to the scan and keeps fix logic independent of scan state.
func mustWalk(opts scanOptions) []sourceFile {
	files, err := walkSources(opts.root, opts.languages)
	if err != nil {
		return nil
	}
	return applyExcludes(files, opts.exclude)
}

// emit writes the report in the chosen format. JSON via beckyio; text/markdown
// via the renderers — all to stdout.
func emit(report Report, format string) {
	switch format {
	case "text":
		renderText(os.Stdout, report)
	case "markdown":
		renderMarkdown(os.Stdout, report)
	default:
		beckyio.PrintJSON(report)
	}
}

// parsePositional parses flags and returns the first positional arg (the path).
// Mirrors the pattern used by the other becky tools so a path can appear before
// or after the flags.
func parsePositional() string {
	flag.Parse()
	rest := flag.Args()
	if len(rest) == 0 {
		return ""
	}
	path := rest[0]
	if len(rest) > 1 {
		_ = flag.CommandLine.Parse(rest[1:])
	}
	return path
}

// isFlagDefault reports whether the named flag was left at its default (not set
// on the command line), so config can fill it in.
func isFlagDefault(name string) bool {
	set := true
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = false
		}
	})
	return set
}

// splitCSV splits a comma-separated flag value into trimmed, non-empty parts.
func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
