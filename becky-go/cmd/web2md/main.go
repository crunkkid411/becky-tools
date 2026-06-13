// becky-web2md — deterministic web page -> clean markdown + YAML frontmatter.
//
//	becky-web2md <url> [--output f] [--vault PATH] [--extract MODE]
//	             [--images] [--images-dir PATH] [--metadata] [--tags t1,t2]
//	             [--batch FILE] [--retry N] [--fallback] [--timeout SEC]
//	             [--css SELECTOR] [--site PROFILE] [--debug] [--verbose]
//
// Extraction runs in an embedded Python helper (trafilatura + bs4 + markdownify);
// the Go side handles flags, frontmatter, image download, file naming, and the
// JSON summary. JSON goes to stdout; diagnostics to stderr; exit 0 on success.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/pyhelpers"
)

// image is one entry in the extraction image manifest. InBody marks images that
// actually appear in the markdown body (so their refs can be rewritten); the
// rest are DOM-swept chrome/asset images kept only for completeness.
type image struct {
	Src       string `json:"src"`
	LocalName string `json:"localname"`
	Alt       string `json:"alt"`
	InBody    bool   `json:"in_body"`
}

// link is one extracted hyperlink (only for --extract links).
type link struct {
	Text string `json:"text"`
	Href string `json:"href"`
}

// codeBlock is one extracted fenced block (only for --extract code).
type codeBlock struct {
	Language string `json:"language"`
	Code     string `json:"code"`
}

// helperResult mirrors web2md.py's stdout JSON.
type helperResult struct {
	Skipped          bool        `json:"skipped"`
	Reason           string      `json:"reason"`
	Title            string      `json:"title"`
	Author           string      `json:"author"`
	Date             string      `json:"date"`
	URL              string      `json:"url"`
	SiteName         string      `json:"sitename"`
	Description      string      `json:"description"`
	Markdown         string      `json:"markdown"`
	Confidence       float64     `json:"confidence"`
	ExtractionMethod string      `json:"extraction_method"`
	WordCount        int         `json:"word_count"`
	Images           []image     `json:"images"`
	Links            []link      `json:"links"`
	Tables           []string    `json:"tables"`
	CodeBlocks       []codeBlock `json:"code_blocks"`
}

// Summary is the becky-web2md JSON contract printed to stdout.
type Summary struct {
	URL              string   `json:"url"`
	Title            string   `json:"title"`
	Author           string   `json:"author"`
	Date             string   `json:"date"`
	OutputFile       string   `json:"output_file"`
	ExtractionMethod string   `json:"extraction_method"`
	Confidence       float64  `json:"confidence"`
	WordCount        int      `json:"word_count"`
	ImagesFound      int      `json:"images_found"`
	ImagesDownloaded int      `json:"images_downloaded"`
	ImagesDir        string   `json:"images_dir,omitempty"`
	Tags             []string `json:"tags"`
	Skipped          bool     `json:"skipped,omitempty"`
	Reason           string   `json:"reason,omitempty"`
}

// BatchSummary wraps per-URL results for --batch mode.
type BatchSummary struct {
	Batch     bool      `json:"batch"`
	Total     int       `json:"total"`
	Succeeded int       `json:"succeeded"`
	Failed    int       `json:"failed"`
	Results   []Summary `json:"results"`
}

const (
	defaultTimeout   = 30
	defaultImagesDir = "assets"
	lowConfidence    = 0.7 // below this --fallback (if set) would try the browser
)

func main() {
	out := flag.String("output", "", "output file (auto-named from title if omitted)")
	vault := flag.String("vault", "", "target Obsidian vault path")
	extract := flag.String("extract", "article", "extract mode: article|full|links|tables|code")
	images := flag.Bool("images", false, "download images locally")
	imagesDir := flag.String("images-dir", defaultImagesDir, "image directory")
	metadata := flag.Bool("metadata", true, "include YAML frontmatter")
	tags := flag.String("tags", "", "additional comma-separated tags")
	batch := flag.String("batch", "", "process newline-separated URL list from file")
	retry := flag.Int("retry", 1, "fetch retry attempts on failure")
	fallback := flag.Bool("fallback", false, "use headless browser when primary fails (best-effort)")
	timeout := flag.Int("timeout", defaultTimeout, "page load timeout in seconds")
	css := flag.String("css", "", "CSS selector override")
	site := flag.String("site", "", "force site-specific extractor (informational)")
	debug := flag.Bool("debug", false, "show extraction method + confidence on stderr")
	verbose := flag.Bool("verbose", false, "show progress on stderr")
	_ = site

	url := parsePositional()
	cfg := config.Load()

	script, err := pyhelpers.Materialize("web2md.py", pyhelpers.Web2md)
	if err != nil {
		beckyio.Fatalf("materialize helper: %v", err)
	}

	opts := convOptions{
		extract:   *extract,
		css:       *css,
		timeout:   *timeout,
		retry:     *retry,
		fallback:  *fallback,
		images:    *images,
		imagesDir: *imagesDir,
		metadata:  *metadata,
		tags:      splitTags(*tags),
		vault:     *vault,
		output:    *out,
		debug:     *debug,
		verbose:   *verbose,
		python:    cfg.Web2mdPython,
		script:    script,
	}

	if *batch != "" {
		runBatch(*batch, opts)
		return
	}

	if url == "" {
		beckyio.Fatalf("usage: becky-web2md <url> [options]  (or --batch FILE)")
	}

	summary, err := convert(url, opts)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	beckyio.PrintJSON(summary)
	if summary.Skipped {
		os.Exit(2)
	}
}

// convOptions bundles the per-conversion settings (avoids long arg lists).
type convOptions struct {
	extract   string
	css       string
	timeout   int
	retry     int
	fallback  bool
	images    bool
	imagesDir string
	metadata  bool
	tags      []string
	vault     string
	output    string
	debug     bool
	verbose   bool
	python    string
	script    string
}

// convert runs the full pipeline for a single URL and returns its summary.
func convert(url string, o convOptions) (Summary, error) {
	beckyio.Logf(o.verbose, "converting %s (extract=%s)", url, o.extract)

	res, err := runHelperWithRetry(url, o)
	if err != nil {
		return Summary{}, err
	}
	if res.Skipped {
		if o.fallback {
			beckyio.Logf(o.verbose, "primary skipped (%s); --fallback requested but browser path is best-effort/not built", res.Reason)
		}
		return Summary{URL: url, Skipped: true, Reason: res.Reason, Tags: o.tags}, nil
	}

	if o.debug {
		fmt.Fprintf(os.Stderr, "[debug] method=%s confidence=%.3f words=%d images=%d\n",
			res.ExtractionMethod, res.Confidence, res.WordCount, len(res.Images))
	}
	if o.fallback && res.Confidence < lowConfidence {
		beckyio.Logf(o.verbose, "confidence %.3f < %.2f; --fallback set but headless browser path is best-effort/not built — using primary result",
			res.Confidence, lowConfidence)
	}

	markdown := res.Markdown
	imagesDownloaded := 0
	finalImagesDir := ""

	outPath := resolveOutputPath(url, res, o)

	if o.images && len(res.Images) > 0 && isContentMode(o.extract) {
		dir := imageDirFor(outPath, o.imagesDir)
		n, rewritten, derr := downloadImages(res.Images, dir, markdown, outPath, o)
		if derr != nil {
			beckyio.Logf(o.verbose, "image download issue: %v", derr)
		}
		markdown = rewritten
		imagesDownloaded = n
		if n > 0 {
			finalImagesDir = dir
		}
	}

	doc := assembleDocument(url, res, markdown, o)
	if err := writeFile(outPath, doc); err != nil {
		return Summary{}, fmt.Errorf("write output %s: %w", outPath, err)
	}
	beckyio.Logf(o.verbose, "wrote %s (%d bytes)", outPath, len(doc))

	return Summary{
		URL:              url,
		Title:            res.Title,
		Author:           res.Author,
		Date:             res.Date,
		OutputFile:       outPath,
		ExtractionMethod: res.ExtractionMethod,
		Confidence:       res.Confidence,
		WordCount:        res.WordCount,
		ImagesFound:      len(res.Images),
		ImagesDownloaded: imagesDownloaded,
		ImagesDir:        finalImagesDir,
		Tags:             allTags(res, o.tags),
	}, nil
}

// isContentMode reports whether the extract mode produces a body worth imaging.
func isContentMode(mode string) bool {
	return mode == "article" || mode == "full" || mode == ""
}

func runHelperWithRetry(url string, o convOptions) (helperResult, error) {
	var lastErr error
	attempts := o.retry
	if attempts < 1 {
		attempts = 1
	}
	for i := 0; i < attempts; i++ {
		if i > 0 {
			beckyio.Logf(o.verbose, "retry %d/%d for %s", i, attempts-1, url)
			time.Sleep(time.Duration(i) * 500 * time.Millisecond)
		}
		res, err := runHelper(url, o)
		if err == nil {
			// A transient skip is worth retrying; a clean failure is not.
			if res.Skipped && i < attempts-1 && isTransient(res.Reason) {
				lastErr = fmt.Errorf("%s", res.Reason)
				continue
			}
			return res, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return helperResult{}, lastErr
	}
	return helperResult{}, fmt.Errorf("conversion failed for %s", url)
}

func isTransient(reason string) bool {
	r := strings.ToLower(reason)
	return strings.Contains(r, "unreachable") || strings.Contains(r, "timeout") ||
		strings.Contains(r, "timed out") || strings.Contains(r, "reset") ||
		strings.Contains(r, "temporarily")
}

func runHelper(url string, o convOptions) (helperResult, error) {
	args := []string{o.script, url, "--extract", o.extract,
		"--timeout", fmt.Sprintf("%d", o.timeout)}
	if o.css != "" {
		args = append(args, "--css", o.css)
	}
	cmd := exec.Command(o.python, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	if o.verbose {
		cmd.Stderr = io.MultiWriter(&stderr, os.Stderr)
	} else {
		cmd.Stderr = &stderr
	}
	if err := cmd.Run(); err != nil {
		return helperResult{}, fmt.Errorf("web2md helper failed: %v\n%s", err, tail(stderr.String()))
	}
	res, ok := parseHelperJSON(stdout.String())
	if !ok {
		return helperResult{}, fmt.Errorf("could not parse web2md helper output:\n%s", tail(stdout.String()))
	}
	return res, nil
}

// parseHelperJSON tolerates leading log noise by scanning bottom-up for the
// first line that unmarshals into the expected shape.
func parseHelperJSON(s string) (helperResult, bool) {
	if r, ok := tryUnmarshal(strings.TrimSpace(s)); ok {
		return r, true
	}
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if r, ok := tryUnmarshal(line); ok {
			return r, true
		}
	}
	return helperResult{}, false
}

func tryUnmarshal(s string) (helperResult, bool) {
	if !strings.HasPrefix(s, "{") {
		return helperResult{}, false
	}
	var r helperResult
	if json.Unmarshal([]byte(s), &r) == nil &&
		(r.Skipped || r.Markdown != "" || r.Title != "" || r.ExtractionMethod != "") {
		return r, true
	}
	return helperResult{}, false
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 800 {
		return s[len(s)-800:]
	}
	return s
}
