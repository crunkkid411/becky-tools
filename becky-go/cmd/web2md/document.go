// document.go — frontmatter assembly, output-path resolution, image download,
// batch processing, and small string helpers for becky-web2md.
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"becky-go/internal/beckyio"
)

const (
	maxTitleSlug     = 80
	imageHTTPTimeout = 45 * time.Second
	maxImageBytes    = 50 << 20 // 50 MiB safety cap per image
)

var nonSlugChars = regexp.MustCompile(`[^A-Za-z0-9 _-]+`)
var multiSpace = regexp.MustCompile(`\s+`)

// parsePositional parses leading flags, extracts the first positional argument,
// then re-parses any flags after it (Go's flag stops at the first non-flag
// token, enabling `becky-web2md <url> --images`).
func parsePositional() string {
	flag.Parse()
	rest := flag.Args()
	if len(rest) == 0 {
		return ""
	}
	url := rest[0]
	if len(rest) > 1 {
		_ = flag.CommandLine.Parse(rest[1:])
	}
	return url
}

// assembleDocument builds the final .md text: YAML frontmatter + markdown body.
func assembleDocument(url string, res helperResult, markdown string, o convOptions) string {
	body := markdown
	// Ensure a top-level H1 with the title if the body lacks any heading.
	if res.Title != "" && !startsWithHeading(body) {
		body = "# " + res.Title + "\n\n" + body
	}
	if !o.metadata {
		return ensureTrailingNewline(body)
	}
	fm := buildFrontmatter(url, res, o)
	return fm + "\n" + ensureTrailingNewline(body)
}

func startsWithHeading(md string) bool {
	t := strings.TrimLeft(md, "\n ")
	return strings.HasPrefix(t, "#")
}

// buildFrontmatter emits a YAML frontmatter block. Values are quoted/escaped so
// titles with colons or quotes stay valid YAML.
func buildFrontmatter(url string, res helperResult, o convOptions) string {
	var b strings.Builder
	b.WriteString("---\n")
	writeYAML(&b, "title", res.Title)
	if res.Author != "" {
		writeYAML(&b, "author", res.Author)
	}
	date := res.Date
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	b.WriteString("date: " + date + "\n")
	writeYAML(&b, "url", firstNonEmpty(res.URL, url))
	if res.SiteName != "" {
		writeYAML(&b, "source", res.SiteName)
	}
	if res.Description != "" {
		writeYAML(&b, "description", res.Description)
	}
	tags := allTags(res, o.tags)
	b.WriteString("tags: [" + strings.Join(quoteAll(tags), ", ") + "]\n")
	b.WriteString(fmt.Sprintf("extraction_method: %s\n", res.ExtractionMethod))
	b.WriteString(fmt.Sprintf("confidence: %.3f\n", res.Confidence))
	b.WriteString(fmt.Sprintf("word_count: %d\n", res.WordCount))
	b.WriteString("clipped: " + time.Now().Format("2006-01-02") + "\n")
	b.WriteString("---\n")
	return b.String()
}

// writeYAML writes `key: "value"` with backslashes and double quotes escaped.
func writeYAML(b *strings.Builder, key, value string) {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	value = strings.ReplaceAll(value, "\n", " ")
	b.WriteString(key + ": \"" + value + "\"\n")
}

func quoteAll(ss []string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, "\""+strings.ReplaceAll(s, "\"", "")+"\"")
	}
	return out
}

// allTags merges the default web-clip tag and user --tags, de-duplicated.
func allTags(res helperResult, userTags []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		t = strings.TrimSpace(t)
		if t == "" || seen[strings.ToLower(t)] {
			return
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	add("web-clip")
	for _, t := range userTags {
		add(t)
	}
	if len(out) == 0 {
		out = []string{"web-clip"}
	}
	return out
}

func splitTags(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// resolveOutputPath determines where the .md goes: explicit --output, else an
// auto-named file from the title, placed inside --vault if given.
func resolveOutputPath(url string, res helperResult, o convOptions) string {
	if o.output != "" {
		return ensureMdExt(maybeJoinVault(o.vault, o.output))
	}
	name := slugify(res.Title)
	if name == "" {
		name = slugify(urlStem(url))
	}
	if name == "" {
		name = "web-clip"
	}
	return maybeJoinVault(o.vault, name+".md")
}

func maybeJoinVault(vault, name string) string {
	if vault != "" && !filepath.IsAbs(name) {
		return filepath.Join(vault, name)
	}
	return name
}

func ensureMdExt(p string) string {
	if strings.EqualFold(filepath.Ext(p), ".md") {
		return p
	}
	return p + ".md"
}

// slugify turns a title into a filesystem-safe, Obsidian-friendly base name.
func slugify(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = nonSlugChars.ReplaceAllString(s, " ")
	s = multiSpace.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	if len(s) > maxTitleSlug {
		s = strings.TrimSpace(s[:maxTitleSlug])
	}
	return s
}

func urlStem(url string) string {
	url = strings.TrimRight(url, "/")
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")
	parts := strings.Split(url, "/")
	last := parts[len(parts)-1]
	if i := strings.IndexAny(last, "?#"); i >= 0 {
		last = last[:i]
	}
	last = strings.TrimSuffix(last, ".html")
	last = strings.TrimSuffix(last, ".htm")
	if last == "" && len(parts) > 0 {
		last = parts[0]
	}
	return last
}

// imageDirFor resolves the assets directory relative to the output file's dir.
func imageDirFor(outPath, imagesDir string) string {
	if filepath.IsAbs(imagesDir) {
		return imagesDir
	}
	return filepath.Join(filepath.Dir(outPath), imagesDir)
}

// downloadImages fetches images into dir and rewrites their references in the
// markdown to the relative local path. Returns (#downloaded, rewritten md, err).
// Body images (those present in the markdown) are preferred; only when none
// exist do we fall back to the DOM-swept manifest so the assets folder is still
// populated.
func downloadImages(images []image, dir, markdown, outPath string, o convOptions) (int, string, error) {
	body := filterBodyImages(images)
	if len(body) > 0 {
		images = body
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, markdown, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	client := &http.Client{Timeout: imageHTTPTimeout}
	rel, err := filepath.Rel(filepath.Dir(outPath), dir)
	if err != nil || rel == "" {
		rel = filepath.Base(dir)
	}
	count := 0
	var lastErr error
	for _, img := range images {
		local := filepath.Join(dir, img.LocalName)
		if err := fetchImage(client, img.Src, local); err != nil {
			lastErr = err
			beckyio.Logf(o.verbose, "skip image %s: %v", img.Src, err)
			continue
		}
		count++
		relPath := filepath.ToSlash(filepath.Join(rel, img.LocalName))
		markdown = rewriteImageRef(markdown, img.Src, relPath)
		beckyio.Logf(o.verbose, "downloaded %s -> %s", img.Src, relPath)
	}
	return count, markdown, lastErr
}

// filterBodyImages returns only the images that appear in the markdown body.
func filterBodyImages(images []image) []image {
	var out []image
	for _, img := range images {
		if img.InBody {
			out = append(out, img)
		}
	}
	return out
}

func fetchImage(client *http.Client, src, dest string) error {
	req, err := http.NewRequest("GET", src, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, io.LimitReader(resp.Body, maxImageBytes)); err != nil {
		return err
	}
	return nil
}

// rewriteImageRef replaces every markdown image src that matches the original
// URL with the local relative path, preserving alt text and optional titles.
func rewriteImageRef(markdown, origSrc, localPath string) string {
	markdown = strings.ReplaceAll(markdown, "]("+origSrc+")", "]("+localPath+")")
	markdown = strings.ReplaceAll(markdown, "]("+origSrc+" ", "]("+localPath+" ")
	return markdown
}

func writeFile(path string, content string) error {
	if d := filepath.Dir(path); d != "" && d != "." {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// runBatch reads URLs from a file, converts each, and prints a BatchSummary.
func runBatch(file string, o convOptions) {
	data, err := os.ReadFile(file)
	if err != nil {
		beckyio.Fatalf("read batch file %s: %v", file, err)
	}
	var urls []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		urls = append(urls, line)
	}
	if len(urls) == 0 {
		beckyio.Fatalf("batch file %s has no URLs", file)
	}

	bs := BatchSummary{Batch: true, Total: len(urls)}
	for i, u := range urls {
		beckyio.Logf(o.verbose, "[%d/%d] %s", i+1, len(urls), u)
		s, cerr := convert(u, o)
		if cerr != nil {
			bs.Failed++
			bs.Results = append(bs.Results, Summary{URL: u, Skipped: true, Reason: cerr.Error(), Tags: o.tags})
			continue
		}
		if s.Skipped {
			bs.Failed++
		} else {
			bs.Succeeded++
		}
		bs.Results = append(bs.Results, s)
	}
	beckyio.PrintJSON(bs)
	if bs.Failed > 0 && bs.Succeeded == 0 {
		os.Exit(2)
	}
}

func ensureTrailingNewline(s string) string {
	if !strings.HasSuffix(s, "\n") {
		return s + "\n"
	}
	return s
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
