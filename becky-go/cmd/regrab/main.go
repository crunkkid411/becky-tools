// becky-regrab — re-grab a page the deterministic downloader missed, using a
// LOCAL Gemma-4 model to pull the content out of the page's visible text, then
// verify the result.
//
//	becky-regrab <url> [--output f] [--vault PATH] [--json] [--timeout SEC]
//
// This is the recovery half of the iPhone-history archiver. becky-web2md is the
// fast deterministic path; when it comes back empty or thin (cluttered listing
// pages, odd layouts, sites trafilatura can't parse — but whose content IS in the
// HTML), becky-regrab asks the smart local model to extract the content. Crucially
// the model's output is then clipcheck-scored against the live page, so a model
// that drops or invents content is CAUGHT, not trusted blindly (deterministic
// verification still guards the model).
//
// If the page has no extractable text at all (a JS-only SPA whose HTML is empty),
// no model can help — that is reported honestly as "unrecoverable (needs a browser
// render)" rather than writing a junk file.
//
// Exit codes mirror becky-clipcheck: 0 pass/thin, 3 partial, 4 fail,
// 5 unrecoverable, 2 usage, 1 error. Degrade-never-crash.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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

// Output is the becky-regrab JSON contract.
type Output struct {
	File      string  `json:"file"`
	URL       string  `json:"url"`
	Verdict   string  `json:"verdict"`
	Method    string  `json:"method"`
	Recall    float64 `json:"recall"`
	Precision float64 `json:"precision"`
	MDWords   int     `json:"md_words"`
	Reason    string  `json:"reason"`
}

const (
	exitPass          = 0
	exitError         = 1
	exitUsage         = 2
	exitPartial       = 3
	exitFail          = 4
	exitUnrecoverable = 5

	// Below this many chars of visible text there is nothing for the model to
	// recover (a JS-only page) — honest "unrecoverable", not a model call.
	minRecoverableChars = 300

	recoverMethod = "gemma4-recover"
)

func main() {
	out := flag.String("output", "", "output .md file (auto-named from title if omitted)")
	vault := flag.String("vault", "", "target folder (Obsidian vault)")
	asJSON := flag.Bool("json", false, "emit JSON instead of a plain-language result")
	timeout := flag.Int("timeout", 30, "page fetch timeout in seconds")
	verbose := flag.Bool("verbose", false, "show progress on stderr")
	flag.Parse()

	// Support `becky-regrab <url> --vault x` (flags after the positional).
	url := ""
	if flag.NArg() > 0 {
		url = flag.Arg(0)
		if flag.NArg() > 1 {
			_ = flag.CommandLine.Parse(flag.Args()[1:])
		}
	}
	if url == "" {
		fmt.Fprintln(os.Stderr, "usage: becky-regrab <url> [--output f] [--vault PATH] [--json]")
		os.Exit(exitUsage)
	}
	url = cleanURL(url)
	logf := func(format string, a ...any) { beckyio.Logf(*verbose, format, a...) }

	cfg := config.Load()

	fr, err := fetchPage(cfg.Web2mdPython, url, *timeout, *verbose)
	if err != nil || !fr.OK {
		reason := "could not fetch the page"
		if err != nil {
			reason = err.Error()
		} else if fr.Reason != "" {
			reason = fr.Reason
		}
		emit(Output{URL: url, Verdict: "unrecoverable", Method: "fetch", Reason: reason}, *asJSON)
		os.Exit(exitUnrecoverable)
	}
	if len(strings.TrimSpace(fr.FullText)) < minRecoverableChars {
		emit(Output{URL: url, Verdict: "unrecoverable", Method: "fetch",
			Reason: "page has no extractable text (likely a JavaScript-only page that needs a browser render)"}, *asJSON)
		os.Exit(exitUnrecoverable)
	}

	// The model extracts the content from the page's visible text.
	markdown, err := gemmaExtract(cfg, url, fr.Title, fr.FullText, logf)
	if err != nil {
		beckyio.Fatalf("local model could not recover the page: %v", err)
	}
	if strings.TrimSpace(markdown) == "" {
		emit(Output{URL: url, Verdict: "fail", Method: recoverMethod, Reason: "the local model returned no content"}, *asJSON)
		os.Exit(exitFail)
	}

	outPath := resolveOutput(url, fr.Title, *vault, *out)
	doc := assembleDoc(url, fr.Title, markdown)
	if err := writeFile(outPath, doc); err != nil {
		beckyio.Fatalf("write %s: %v", outPath, err)
	}
	logf("regrab wrote %s (%d bytes)", outPath, len(doc))

	// Verify the model's output against the live page (no extra fetch — reuse the
	// content we already have). A model that dropped or invented content is caught.
	res := clipcheck.Score(markdown, clipcheck.PageContent{
		PageText: fr.PageText, FullText: fr.FullText, MainBlocks: fr.MainBlocks,
	})
	o := Output{
		File: outPath, URL: url, Verdict: res.Verdict, Method: recoverMethod,
		Recall: res.Recall, Precision: res.Precision, MDWords: res.MDWords, Reason: res.Reason,
	}
	emit(o, *asJSON)
	os.Exit(exitFor(res.Verdict))
}

func exitFor(verdict string) int {
	switch verdict {
	case clipcheck.VerdictPass, clipcheck.VerdictThin:
		return exitPass
	case clipcheck.VerdictPartial:
		return exitPartial
	case clipcheck.VerdictFail:
		return exitFail
	default:
		return exitUnrecoverable
	}
}

func emit(o Output, asJSON bool) {
	if asJSON {
		beckyio.PrintJSON(o)
		return
	}
	fmt.Printf("becky-regrab: %s (%s) — %s\n", strings.ToUpper(o.Verdict), o.Method, o.Reason)
	if o.File != "" {
		fmt.Printf("  file : %s\n", o.File)
	}
	fmt.Printf("  url  : %s\n", o.URL)
	if o.Verdict != "unrecoverable" {
		fmt.Printf("  recall %.2f  precision %.2f  words %d\n", o.Recall, o.Precision, o.MDWords)
	}
}

// cleanURL trims trailing junk that breaks a fetch (a stray comma/space/quote
// from a mangled synced URL) without touching legitimate trailing characters
// like ')' in a Wikipedia path.
func cleanURL(u string) string {
	return strings.TrimRight(strings.TrimSpace(u), " \t\r\n,\"'")
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
	beckyio.Logf(verbose, "regrab: fetching %s", url)
	if err := cmd.Run(); err != nil {
		return fetchResult{}, fmt.Errorf("clipfetch helper failed: %v\n%s", err, tail(stderr.String()))
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var fr fetchResult
		if json.Unmarshal([]byte(line), &fr) == nil && (fr.OK || fr.Reason != "") {
			return fr, nil
		}
	}
	return fetchResult{}, fmt.Errorf("could not parse clipfetch output:\n%s", tail(stdout.String()))
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 600 {
		return s[len(s)-600:]
	}
	return s
}

// resolveOutput picks the .md path: explicit --output, else a slug of the title.
func resolveOutput(url, title, vault, out string) string {
	if out != "" {
		if vault != "" && !filepath.IsAbs(out) {
			return filepath.Join(vault, out)
		}
		return out
	}
	name := slug(title)
	if name == "" {
		name = "recovered-clip"
	}
	name += ".md"
	if vault != "" {
		return filepath.Join(vault, name)
	}
	return name
}

func writeFile(path, content string) error {
	if d := filepath.Dir(path); d != "" && d != "." {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// assembleDoc builds the .md: YAML frontmatter + the model's markdown body.
func assembleDoc(url, title, markdown string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("title: \"" + yamlEscape(title) + "\"\n")
	b.WriteString("url: \"" + yamlEscape(url) + "\"\n")
	b.WriteString("date: " + time.Now().Format("2006-01-02") + "\n")
	b.WriteString("tags: [\"web-clip\", \"recovered\"]\n")
	b.WriteString("extraction_method: " + recoverMethod + "\n")
	b.WriteString("clipped: " + time.Now().Format("2006-01-02") + "\n")
	b.WriteString("---\n\n")
	if title != "" && !strings.HasPrefix(strings.TrimLeft(markdown, "\n "), "#") {
		b.WriteString("# " + title + "\n\n")
	}
	b.WriteString(markdown)
	if !strings.HasSuffix(markdown, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

func yamlEscape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return strings.ReplaceAll(s, "\n", " ")
}

// slug turns a title into a filesystem-safe base name (spaces kept, Obsidian-friendly).
func slug(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		switch {
		case (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-':
			b.WriteRune(r)
			prevSpace = false
		default:
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
		}
	}
	out := strings.TrimSpace(b.String())
	if len(out) > 80 {
		out = strings.TrimSpace(out[:80])
	}
	return out
}
