// becky-deslop — deterministic AI-writing-tell remover (no LLM).
//
//	becky-deslop [file...] [options]
//	  --stdin             read from stdin instead of files
//	  --output FILE       write cleaned text to FILE (default: stdout)
//	  --format MODE       minimal | full | aggressive (default: full)
//	  --check             CI mode: exit 1 if any tells are found, 0 if clean
//	  --json              emit findings as JSON instead of cleaned text
//	  --rules FILE        replace the embedded ruleset with a custom JSON file
//	  --no-color          disable ANSI color in the --check summary
//	  --verbose           progress to stderr
//
// Pure pattern matching: a regex + replacement dictionary embedded in the
// binary (rules.go), markdown-aware skip zones (markdown.go), and structural
// heuristics (structural.go). Same input always yields same output.
//
// I/O contract (house rules): cleaned text or JSON to stdout, diagnostics to
// stderr, exit 0 on success, non-zero on error or (with --check) on tells.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"becky-go/internal/beckyio"
)

// jsonOutput is the --json document shape required by the task spec.
type jsonOutput struct {
	Findings []Finding      `json:"findings"`
	Counts   map[string]int `json:"counts"`
	Clean    bool           `json:"clean"`
	Score    int            `json:"score"`
	Files    []string       `json:"files,omitempty"`
}

func main() {
	var (
		useStdin = flag.Bool("stdin", false, "read from stdin")
		output   = flag.String("output", "", "write cleaned text to FILE (default: stdout)")
		format   = flag.String("format", "full", "minimal | full | aggressive")
		check    = flag.Bool("check", false, "CI mode: exit 1 if any tells are found")
		asJSON   = flag.Bool("json", false, "emit findings as JSON")
		rulesF   = flag.String("rules", "", "custom rules JSON file (replaces embedded set)")
		noColor  = flag.Bool("no-color", false, "disable colored summary output")
		verbose  = flag.Bool("verbose", false, "progress to stderr")
	)

	files := parseArgs()

	if *format != "minimal" && *format != "full" && *format != "aggressive" {
		beckyio.Fatalf("invalid --format %q (want minimal|full|aggressive)", *format)
	}

	rules, err := loadRules(*rulesF)
	if err != nil {
		beckyio.Fatalf("loading rules: %v", err)
	}
	beckyio.Logf(*verbose, "loaded %d rules, format=%s", len(rules), *format)

	text, names, err := readInput(*useStdin, files)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}

	res := process(text, rules, *format)
	beckyio.Logf(*verbose, "found %d tells (score %d) across %d categories",
		len(res.Findings), res.Score, nonZeroCats(res.Counts))

	// --json takes precedence: emit findings, never the cleaned body, to stdout.
	if *asJSON {
		out := jsonOutput{
			Findings: ensureFindings(res.Findings),
			Counts:   res.Counts,
			Clean:    res.Clean,
			Score:    res.Score,
			Files:    names,
		}
		beckyio.PrintJSON(out)
		exitForCheck(*check, res.Clean)
		return
	}

	// --check without --json: stay quiet on stdout, summarize to stderr.
	if *check {
		summarizeToStderr(res, *noColor)
		exitForCheck(true, res.Clean)
		return
	}

	// Default: write cleaned text to --output or stdout.
	if err := writeOutput(*output, res.Cleaned); err != nil {
		beckyio.Fatalf("writing output: %v", err)
	}
	beckyio.Logf(*verbose, "wrote %d bytes of cleaned text", len(res.Cleaned))
}

// parseArgs runs flag.Parse and returns positional file arguments, re-parsing
// any flags interleaved after the first positional (matches the other tools).
func parseArgs() []string {
	flag.Parse()
	args := flag.Args()
	var files []string
	for len(args) > 0 {
		if strings.HasPrefix(args[0], "-") {
			_ = flag.CommandLine.Parse(args)
			args = flag.Args()
			continue
		}
		files = append(files, args[0])
		args = args[1:]
	}
	return files
}

// loadRules returns the compiled ruleset: embedded by default, or fully replaced
// by a custom JSON file when --rules is given.
func loadRules(path string) ([]Rule, error) {
	if path == "" {
		return builtinRules(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	jf, err := parseRuleFile(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	rules, err := rulesFromJSON(jf)
	if err != nil {
		return nil, fmt.Errorf("compile rules from %s: %w", path, err)
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("%s contained no rules", path)
	}
	return rules, nil
}

// parseRuleFile decodes a custom --rules JSON document.
func parseRuleFile(data []byte) (jsonRuleFile, error) {
	var jf jsonRuleFile
	if err := json.Unmarshal(data, &jf); err != nil {
		return jsonRuleFile{}, err
	}
	return jf, nil
}

// readInput returns combined input text and source names. With --stdin, or with
// no file arguments, it reads stdin; otherwise it concatenates named files
// (blank-line separated so paragraph logic still works).
func readInput(useStdin bool, files []string) (string, []string, error) {
	if useStdin || len(files) == 0 {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", nil, fmt.Errorf("reading stdin: %w", err)
		}
		return string(data), []string{"<stdin>"}, nil
	}
	var parts []string
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return "", nil, fmt.Errorf("reading %s: %w", f, err)
		}
		parts = append(parts, string(data))
	}
	return strings.Join(parts, "\n\n"), files, nil
}

// writeOutput sends cleaned text to a file or stdout.
func writeOutput(path, text string) error {
	if path == "" {
		_, err := os.Stdout.WriteString(text)
		return err
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

// exitForCheck applies --check exit semantics: exit 1 when tells were found.
func exitForCheck(check, clean bool) {
	if check && !clean {
		os.Exit(1)
	}
	os.Exit(0)
}

// summarizeToStderr prints one line per finding to stderr for --check runs that
// did not request JSON. Honours --no-color.
func summarizeToStderr(res Result, noColor bool) {
	if res.Clean {
		fmt.Fprintln(os.Stderr, colorize("clean: no AI tells detected", "32", noColor))
		return
	}
	fmt.Fprintf(os.Stderr, "%s\n",
		colorize(fmt.Sprintf("%d AI tells found (score %d):", len(res.Findings), res.Score), "31", noColor))
	for _, f := range res.Findings {
		fmt.Fprintf(os.Stderr, "  input:%d:%d  [%s] %q -> %s\n",
			f.Line, f.Col, f.Category, f.Match, f.Suggestion)
	}
}

// colorize wraps s in an ANSI SGR code unless color is disabled.
func colorize(s, code string, noColor bool) string {
	if noColor {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// ensureFindings normalizes a nil slice to empty so JSON shows "findings": [].
func ensureFindings(f []Finding) []Finding {
	if f == nil {
		return []Finding{}
	}
	return f
}

// nonZeroCats counts how many categories have at least one finding.
func nonZeroCats(counts map[string]int) int {
	n := 0
	for _, v := range counts {
		if v > 0 {
			n++
		}
	}
	return n
}
