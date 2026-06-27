// becky-clipcheck — confirm a saved markdown clip actually CONTAINS its web page.
//
//	becky-clipcheck <file.md> [--url URL] [--json] [--no-ai] [--timeout SEC]
//
// The verification half of the iPhone-history archiver. becky-web2md writes a
// .md; this tool re-fetches the page (clipfetch.py), then deterministically
// scores recall (did the clip drop content?) and precision (did it invent any?).
// The clear cases (pass / fail) are decided by the numbers alone — no model. Only
// the borderline "partial" verdict is escalated to a LOCAL Gemma-4 model for a
// final PASS/FAIL call (AI only where it is absolutely necessary).
//
// Degrade-never-crash: an unreachable page or missing Python yields an honest
// "unverified" result, not a panic. Exit codes: 0 pass/thin, 3 partial,
// 4 fail, 5 unverified (could not fetch), 2 usage, 1 error.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/clipcheck"
	"becky-go/internal/config"
	"becky-go/internal/pyhelpers"
)

// fetchResult mirrors clipfetch.py's stdout JSON.
type fetchResult struct {
	OK         bool     `json:"ok"`
	Reason     string   `json:"reason"`
	Title      string   `json:"title"`
	PageText   string   `json:"page_text"`
	FullText   string   `json:"full_text"`
	MainBlocks []string `json:"main_blocks"`
}

// Output is the becky-clipcheck JSON contract printed to stdout.
type Output struct {
	File      string  `json:"file"`
	URL       string  `json:"url"`
	Verdict   string  `json:"verdict"`
	Recall    float64 `json:"recall"`
	Precision float64 `json:"precision"`
	Coverage  float64 `json:"coverage"`
	Units     int     `json:"units"`
	Covered   int     `json:"covered"`
	MDWords   int     `json:"md_words"`
	PageWords int     `json:"page_words"`
	AIUsed    bool    `json:"ai_used"`
	Reason    string  `json:"reason"`
}

const (
	exitPass       = 0
	exitError      = 1
	exitUsage      = 2
	exitPartial    = 3
	exitFail       = 4
	exitUnverified = 5
)

func main() {
	mdFlag := flag.String("md", "", "the markdown clip to verify (or pass it as the positional argument)")
	urlFlag := flag.String("url", "", "source URL (default: read from the clip's `url:` frontmatter)")
	asJSON := flag.Bool("json", false, "emit JSON instead of a plain-language verdict")
	noAI := flag.Bool("no-ai", false, "skip the local-model adjudication of borderline clips (deterministic only)")
	timeout := flag.Int("timeout", 30, "page fetch timeout in seconds")
	verbose := flag.Bool("verbose", false, "show progress on stderr")
	flag.Parse()

	// Support both `becky-clipcheck --md f --json` and `becky-clipcheck f --json`:
	// Go's flag parser stops at the first positional, so re-parse the flags that
	// follow it (otherwise --json/--no-ai after the filename are silently dropped).
	mdPath := *mdFlag
	if mdPath == "" && flag.NArg() > 0 {
		mdPath = flag.Arg(0)
		if flag.NArg() > 1 {
			_ = flag.CommandLine.Parse(flag.Args()[1:])
		}
	}
	if mdPath == "" {
		fmt.Fprintln(os.Stderr, "usage: becky-clipcheck <file.md> [--url URL] [--json] [--no-ai]")
		os.Exit(exitUsage)
	}

	url, body, err := readClip(mdPath)
	if err != nil {
		beckyio.Fatalf("read clip %s: %v", mdPath, err)
	}
	if *urlFlag != "" {
		url = *urlFlag
	}
	if url == "" {
		beckyio.Fatalf("no source URL: the clip has no `url:` frontmatter; pass --url")
	}

	cfg := config.Load()
	fr, ferr := fetchPage(cfg.Web2mdPython, url, *timeout, *verbose)
	if ferr != nil || !fr.OK {
		reason := "could not fetch the page to verify against"
		if ferr != nil {
			reason = ferr.Error()
		} else if fr.Reason != "" {
			reason = fr.Reason
		}
		out := Output{File: mdPath, URL: url, Verdict: "unverified", Reason: reason}
		emit(out, *asJSON)
		os.Exit(exitUnverified)
	}

	res := clipcheck.Score(body, clipcheck.PageContent{
		PageText: fr.PageText, FullText: fr.FullText, MainBlocks: fr.MainBlocks,
	})

	out := Output{
		File: mdPath, URL: url,
		Verdict: res.Verdict, Recall: res.Recall, Precision: res.Precision,
		Coverage: res.Coverage, Units: res.Units, Covered: res.Covered,
		MDWords: res.MDWords, PageWords: res.PageWords, Reason: res.Reason,
	}

	// Only the genuinely borderline case asks the local model.
	if res.Verdict == clipcheck.VerdictPartial && !*noAI && fr.PageText != "" {
		logf := func(format string, a ...any) { beckyio.Logf(*verbose, format, a...) }
		if v, reason, ok := adjudicate(cfg, fr.PageText, body, logf); ok {
			out.Verdict = v
			out.Reason = reason
			out.AIUsed = true
		}
	}

	emit(out, *asJSON)
	os.Exit(exitFor(out.Verdict))
}

// exitFor maps a verdict to a process exit code so scripts can branch on it.
func exitFor(verdict string) int {
	switch verdict {
	case clipcheck.VerdictPass, clipcheck.VerdictThin:
		return exitPass
	case clipcheck.VerdictPartial:
		return exitPartial
	case clipcheck.VerdictFail:
		return exitFail
	default:
		return exitUnverified
	}
}

// emit prints the verdict as JSON (--json) or a tight plain-language report.
func emit(o Output, asJSON bool) {
	if asJSON {
		beckyio.PrintJSON(o)
		return
	}
	fmt.Printf("becky-clipcheck: %s — %s\n", strings.ToUpper(o.Verdict), o.Reason)
	fmt.Printf("  file : %s\n", o.File)
	fmt.Printf("  url  : %s\n", o.URL)
	if o.Verdict != "unverified" {
		fmt.Printf("  recall %.2f  precision %.2f  (%d/%d content blocks present)\n",
			o.Recall, o.Precision, o.Covered, o.Units)
	}
	if o.AIUsed {
		fmt.Println("  (borderline — adjudicated by the local Gemma-4 model)")
	}
}

var urlFMRe = regexp.MustCompile(`(?m)^\s*url:\s*"?([^"\n]+?)"?\s*$`)

// readClip returns the source URL (from `url:` frontmatter) and the body text
// (everything after the YAML frontmatter block) of a markdown clip.
func readClip(path string) (url, body string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	fm, body := splitFrontmatter(text)
	if m := urlFMRe.FindStringSubmatch(fm); m != nil {
		url = strings.TrimSpace(m[1])
	}
	return url, body, nil
}

// splitFrontmatter separates a leading `---\n ... \n---\n` YAML block from the
// body. When there is no frontmatter the whole text is the body.
func splitFrontmatter(text string) (fm, body string) {
	if !strings.HasPrefix(text, "---") {
		return "", text
	}
	rest := text[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return "", text
	}
	fm = rest[:idx]
	after := rest[idx+len("\n---"):]
	if nl := strings.IndexByte(after, '\n'); nl >= 0 {
		body = after[nl+1:]
	}
	return fm, body
}

// fetchPage runs clipfetch.py and parses its JSON (tolerating leading log noise).
func fetchPage(python, url string, timeout int, verbose bool) (fetchResult, error) {
	script, err := pyhelpers.Materialize("clipfetch.py", pyhelpers.Clipfetch)
	if err != nil {
		return fetchResult{}, fmt.Errorf("materialize helper: %w", err)
	}
	cmd := exec.Command(python, script, url, "--timeout", fmt.Sprintf("%d", timeout))
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	beckyio.Logf(verbose, "clipcheck: fetching %s for verification", url)
	if err := cmd.Run(); err != nil {
		return fetchResult{}, fmt.Errorf("clipfetch helper failed: %v\n%s", err, tail(stderr.String()))
	}
	fr, ok := parseFetchJSON(stdout.String())
	if !ok {
		return fetchResult{}, fmt.Errorf("could not parse clipfetch output:\n%s", tail(stdout.String()))
	}
	return fr, nil
}

func parseFetchJSON(s string) (fetchResult, bool) {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var fr fetchResult
		if json.Unmarshal([]byte(line), &fr) == nil && (fr.OK || fr.Reason != "") {
			return fr, true
		}
	}
	return fetchResult{}, false
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 600 {
		return s[len(s)-600:]
	}
	return s
}
