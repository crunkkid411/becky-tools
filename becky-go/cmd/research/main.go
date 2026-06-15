// becky-research — turn a research question (or a dropped reading list of URLs)
// into a single, fully-cited report. Plan → fan-out search → fetch+cache → RRF
// rank/dedup → verify → cited synthesis (SPEC-DEEP-RESEARCH.md).
//
//	becky-research "<question>"        # autonomous topic research
//	becky-research --urls urls.txt     # synthesize a reading list
//	becky-research --urls-stdin        #   (one URL/path per line on stdin)
//
// Options: --out <dir>, --max-subquestions, --max-queries-per, --max-sources,
// --offline, --self-upgrade, --format md|json|both, --verbose.
//
// becky-research is becky's one AGENTIC + ONLINE tool: it opts out of the offline
// invariant via an explicit, logged network step (search + fetch), but keeps a
// DETERMINISTIC output format over the captured snapshot and degrades, never
// crashes. becky's offline forensic tools never call the network. The model and
// search/fetch backends live behind interfaces; without them wired this binary
// runs model-free (deterministic plan + sources-only) and says so. The local agent
// wires research_helper.py (llama-server) + a SearXNG/web2md backend per SPEC §5.
//
// stdout: findings JSON. stderr: plain-English headline. report.md → run dir.
// Exit codes: 0 = report produced (possibly partial/degraded); 2 = bad invocation;
// 3 = unsalvageable (and even then JSON carries the degrade reason — never a panic).
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"becky-go/internal/pathx"
	"becky-go/internal/research"
)

// exit codes (SPEC §4).
const (
	exitOK      = 0
	exitBadArgs = 2
	exitHard    = 3
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run is the testable entry point: it returns the exit code instead of calling
// os.Exit, so the CLI surface can be unit-tested.
func run(args []string) int {
	opts, code, done := parseFlags(args)
	if done {
		return code
	}

	cfg, err := buildConfig(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-research:", err)
		fmt.Fprintln(os.Stderr, "give a \"<question>\" or --urls <file> / --urls-stdin.")
		return exitBadArgs
	}

	backends := defaultBackends() // model-free fallback; local agent wires the real ones
	rep, runErr := research.Run(cfg, backends)

	if err := writeReportMD(opts, cfg, rep); err != nil {
		fmt.Fprintln(os.Stderr, "becky-research: could not write report.md:", err)
		// non-fatal: the JSON below is still the machine-readable source of truth
	}
	if opts.format != "md" {
		emitJSON(rep)
	}
	fmt.Fprint(os.Stderr, research.PlainSummary(rep))

	if runErr != nil {
		// Unsalvageable setup failure — JSON already carries the degrade reason.
		return exitHard
	}
	return exitOK
}

// cliOptions holds the parsed flags.
type cliOptions struct {
	question    string
	urlsFile    string
	urlsStdin   bool
	out         string
	maxSub      int
	maxPer      int
	maxSources  int
	offline     bool
	selfUpgrade bool
	format      string
	verbose     bool
}

// parseFlags parses argv. done=true means the caller should return code (help/parse
// error). Otherwise opts is valid-enough to hand to buildConfig for validation.
func parseFlags(args []string) (cliOptions, int, bool) {
	fs := flag.NewFlagSet("becky-research", flag.ContinueOnError)
	var o cliOptions
	fs.StringVar(&o.urlsFile, "urls", "", "reading-list mode: file with one URL/path per line")
	fs.BoolVar(&o.urlsStdin, "urls-stdin", false, "reading-list mode: read URLs from stdin")
	fs.StringVar(&o.out, "out", "", "run dir (default .research/<slug>)")
	fs.IntVar(&o.maxSub, "max-subquestions", 5, "R1 cap on sub-questions")
	fs.IntVar(&o.maxPer, "max-queries-per", 3, "R2 cap on queries per sub-question")
	fs.IntVar(&o.maxSources, "max-sources", 25, "hard cap on fetched URLs")
	fs.BoolVar(&o.offline, "offline", false, "no live search/fetch; use only the cached snapshot")
	selfUp := fs.String("self-upgrade", "on", "becky-dependency watch: on|off")
	fs.StringVar(&o.format, "format", "both", "md|json|both")
	fs.BoolVar(&o.verbose, "verbose", false, "stage headlines to stderr")
	// Reorder so flags precede the positional question: Go's flag pkg otherwise
	// stops at the first non-flag token, which would swallow trailing --flags into
	// the question (a real trap for a non-dev mixing order).
	flags, positionals := splitArgs(args, boolFlags)
	if err := fs.Parse(flags); err != nil {
		return o, exitBadArgs, true
	}
	o.selfUpgrade = strings.ToLower(strings.TrimSpace(*selfUp)) != "off"
	o.question = strings.TrimSpace(strings.Join(append(fs.Args(), positionals...), " "))
	o.format = strings.ToLower(strings.TrimSpace(o.format))
	if o.format != "md" && o.format != "json" && o.format != "both" {
		o.format = "both"
	}
	return o, exitOK, false
}

// boolFlags are the flags that take NO value, so splitArgs knows not to consume the
// following token as their argument when reordering.
var boolFlags = map[string]bool{"urls-stdin": true, "offline": true, "verbose": true}

// splitArgs separates flag tokens (and their values) from positional tokens so the
// flag package can parse regardless of order. A token is a flag if it starts with
// "-"; a non-bool flag in "--name value" form also claims the next token. "--"
// ends flag parsing: everything after is positional. Unknown flags are left in the
// flag stream so flag.Parse reports them (we don't silently drop a typo).
func splitArgs(args []string, bools map[string]bool) (flags, positionals []string) {
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--":
			positionals = append(positionals, args[i+1:]...)
			return flags, positionals
		case strings.HasPrefix(a, "-"):
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				break // "--name=value" — value is inline
			}
			if !bools[name] && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				flags = append(flags, args[i]) // claim the value token
			}
		default:
			positionals = append(positionals, a)
		}
		i++
	}
	return flags, positionals
}

// buildConfig validates the invocation and assembles the research.Config. A run
// needs EITHER a question OR a URL source; neither is a bad invocation (exit 2).
func buildConfig(o cliOptions) (research.Config, error) {
	urls, err := loadURLs(o)
	if err != nil {
		return research.Config{}, err
	}
	if o.question == "" && len(urls) == 0 {
		return research.Config{}, fmt.Errorf("no question and no URLs")
	}
	out := o.out
	if out == "" {
		out = ".research/" + slug(o.question, urls)
	}
	return research.Config{
		Question:        o.question,
		URLs:            urls,
		RunDir:          out,
		MaxSubquestions: o.maxSub,
		MaxQueriesPer:   o.maxPer,
		MaxSources:      o.maxSources,
		Offline:         o.offline,
		SelfUpgrade:     o.selfUpgrade,
	}, nil
}

// loadURLs reads the reading-list URLs from --urls and/or stdin (one per line,
// blank lines and # comments skipped). Returns an error only on a real read fault.
func loadURLs(o cliOptions) ([]string, error) {
	var urls []string
	if o.urlsFile != "" {
		f, err := os.Open(o.urlsFile)
		if err != nil {
			return nil, fmt.Errorf("open --urls %q: %w", o.urlsFile, err)
		}
		defer f.Close()
		urls = append(urls, scanURLs(f)...)
	}
	if o.urlsStdin {
		urls = append(urls, scanURLs(os.Stdin)...)
	}
	return urls, nil
}

// scanURLs reads non-blank, non-comment lines.
func scanURLs(f *os.File) []string {
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// defaultBackends returns the model-free fallback (deterministic plan + the cache,
// no live search/fetch, sources-only). The local agent replaces this with real
// research_helper.py + SearXNG/web2md backends per SPEC §5/§8.
func defaultBackends() research.Backends {
	return research.Backends{
		Search: nil, // no live search until a backend is wired
		Fetch:  nil, // no live fetch until a backend is wired
		Helper: nil, // sources-only until the model helper is wired
	}
}

// writeReportMD renders report.md into the run dir (best-effort).
func writeReportMD(o cliOptions, cfg research.Config, rep research.Report) error {
	if o.format == "json" {
		return nil
	}
	if err := os.MkdirAll(cfg.RunDir, 0o755); err != nil {
		return fmt.Errorf("mkdir run dir: %w", err)
	}
	path := cfg.RunDir + "/report.md"
	if err := os.WriteFile(path, []byte(research.RenderMarkdown(rep)), 0o644); err != nil {
		return fmt.Errorf("write %q: %w", pathx.Base(path), err)
	}
	return nil
}

// emitJSON writes the findings JSON to stdout (the machine-readable contract).
func emitJSON(rep research.Report) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rep); err != nil {
		fmt.Fprintln(os.Stderr, "becky-research: encode JSON:", err)
	}
}

// slug derives a short, filesystem-safe run-dir name from the question (or first
// URL), using pathx.Base so a Windows-style path input still yields a clean slug.
func slug(question string, urls []string) string {
	src := question
	if src == "" && len(urls) > 0 {
		src = pathx.Base(urls[0])
	}
	src = strings.ToLower(src)
	var b strings.Builder
	for _, r := range src {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		}
		if b.Len() >= 40 {
			break
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "run"
	}
	return s
}
