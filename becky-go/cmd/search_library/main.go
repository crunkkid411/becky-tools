// search_library — one dumb call that searches Jordan's whole life-context
// library (bookmarks, history, GitHub stars, YouTube, research, AI chats)
// plus his AI chat transcripts, and returns a merged, scored JSON envelope.
//
//	search_library "<plain english query>" [--limit N] [--pretty]
//
// Backed by qmd (https://github.com — installed globally as `qmd` on PATH):
// two qmd collections, "library" (X:\AI-2\hj-mission-control\library, the library-contract.md
// folder tree) and "transcripts" (Jordan's AI chats, pre-existing), searched
// with `qmd search` (BM25, no LLM rerank — qmd's hybrid `query` reranker took
// ~60s per call in testing, too slow for a live voice assistant call).
//
// Default output is the shared JSON envelope from
// hj-mission-control/docs/library-contract.md:
//
//	{"ok":true,"results":[{"title","path","url","date","source","score","snippet"}]}
//
// --pretty prints the same results as high-contrast colored text instead.
// Exit 0 on success; nonzero with {"ok":false,"error":"..."} on failure.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// qmdCollections are searched on every call and merged into one result set.
var qmdCollections = []string{"library", "transcripts"}

// qmdHit is one element of `qmd search --json`'s output array.
type qmdHit struct {
	Docid   string  `json:"docid"`
	Score   float64 `json:"score"`
	File    string  `json:"file"` // "qmd://<collection>/<relative-path>"
	Title   string  `json:"title"`
	Snippet string  `json:"snippet"`
}

// Result is one row of the library-contract.md JSON envelope.
type Result struct {
	Title   string  `json:"title"`
	Path    string  `json:"path"`
	URL     string  `json:"url"`
	Date    string  `json:"date"`
	Source  string  `json:"source"`
	Score   float64 `json:"score"`
	Snippet string  `json:"snippet"`
}

// successEnvelope and errorEnvelope are marshaled separately (rather than one
// struct with omitempty) so a failure prints exactly {"ok":false,"error":"..."}
// per the contract, with no stray "results" key, while success always shows
// "results" even as an empty array.
type successEnvelope struct {
	OK      bool     `json:"ok"`
	Results []Result `json:"results"`
}

type errorEnvelope struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func main() {
	enableANSI()
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	query, limit, pretty, err := parseArgs(args)
	if err != nil {
		return fail(err.Error(), pretty)
	}
	if query == "" {
		return fail("query is required: search_library \"<plain english query>\" [--limit N] [--pretty]", pretty)
	}

	hits, err := qmdSearch(query, limit)
	if err != nil {
		return fail(err.Error(), pretty)
	}

	results := toResults(hits)
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if len(results) > limit {
		results = results[:limit]
	}

	if pretty {
		printPretty(query, results)
		return 0
	}
	return printSuccess(results)
}

// parseArgs does simple manual parsing (not the flag package) because the
// contract puts the positional query BEFORE the flags, which flag.Parse
// stops scanning at.
func parseArgs(args []string) (query string, limit int, pretty bool, err error) {
	limit = 10
	var queryParts []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--pretty":
			pretty = true
		case a == "--json":
			// No-op: search_library's default output (no --pretty) is already
			// the {"ok":...} JSON envelope. Recognized explicitly so the CLI
			// convention every becky tool shares (--json is always a safe,
			// harmless flag to pass) doesn't get swallowed into the query text
			// (becky-AI-Agent-review-1.md acceptance criterion 8).
		case a == "--limit":
			i++
			if i >= len(args) {
				return "", 0, pretty, fmt.Errorf("--limit needs a number")
			}
			n, e := strconv.Atoi(args[i])
			if e != nil || n <= 0 {
				return "", 0, pretty, fmt.Errorf("--limit needs a positive number, got %q", args[i])
			}
			limit = n
		case strings.HasPrefix(a, "--limit="):
			n, e := strconv.Atoi(strings.TrimPrefix(a, "--limit="))
			if e != nil || n <= 0 {
				return "", 0, pretty, fmt.Errorf("--limit needs a positive number, got %q", a)
			}
			limit = n
		case a == "-h", a == "--help":
			return "", 0, pretty, fmt.Errorf("usage: search_library \"<plain english query>\" [--limit N] [--json] [--pretty]")
		default:
			queryParts = append(queryParts, a)
		}
	}
	return strings.TrimSpace(strings.Join(queryParts, " ")), limit, pretty, nil
}

// qmdSearch shells out to `qmd search` (BM25, no LLM rerank) across every
// collection in qmdCollections and returns the raw hits.
func qmdSearch(query string, limit int) ([]qmdHit, error) {
	qmdPath, err := exec.LookPath("qmd")
	if err != nil {
		return nil, fmt.Errorf("qmd not found on PATH (needed for search_library): %w", err)
	}

	cmdArgs := []string{"search", query}
	for _, c := range qmdCollections {
		cmdArgs = append(cmdArgs, "-c", c)
	}
	cmdArgs = append(cmdArgs, "--json", "-n", strconv.Itoa(limit))

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(qmdPath, cmdArgs...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("qmd search failed: %s", msg)
	}

	var hits []qmdHit
	if err := json.Unmarshal(stdout.Bytes(), &hits); err != nil {
		return nil, fmt.Errorf("qmd returned output search_library couldn't parse: %w", err)
	}
	return hits, nil
}

// collectionRoots caches `qmd collection show <name>` lookups (at most one
// subprocess call per distinct collection per run).
var collectionRoots = map[string]string{}

func collectionRoot(name string) string {
	if root, ok := collectionRoots[name]; ok {
		return root
	}
	root := ""
	out, err := exec.Command("qmd", "collection", "show", name).Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if rest, ok := strings.CutPrefix(line, "Path:"); ok {
				root = strings.TrimSpace(rest)
				break
			}
		}
	}
	collectionRoots[name] = root
	return root
}

// toResults converts qmd's raw hits into the contract's envelope rows,
// deriving source/path/url/date that qmd's own JSON doesn't carry.
func toResults(hits []qmdHit) []Result {
	results := make([]Result, 0, len(hits))
	for _, h := range hits {
		collection, relPath, ok := strings.Cut(strings.TrimPrefix(h.File, "qmd://"), "/")
		if !ok {
			collection, relPath = h.File, ""
		}

		source := collection
		if collection == "transcripts" {
			source = "ai-chats"
		} else if seg, _, ok := strings.Cut(relPath, "/"); ok {
			source = seg
		}

		path := h.File
		if root := collectionRoot(collection); root != "" {
			path = filepath.Join(root, filepath.FromSlash(relPath))
		}

		url, date := extractURLAndDate(h.Snippet)

		results = append(results, Result{
			Title:   h.Title,
			Path:    path,
			URL:     url,
			Date:    date,
			Source:  source,
			Score:   h.Score,
			Snippet: cleanSnippet(h.Snippet),
		})
	}
	return results
}

// extractURLAndDate best-effort scans a qmd snippet's diff-style lines for
// "- url: ..." / "- date_added: ..." / "date: ..." markers that the library
// ingestion CLI writes into every markdown item. Not every hit has these
// (e.g. AI-chat transcripts don't), so both may come back empty.
func extractURLAndDate(snippet string) (url, date string) {
	for _, raw := range strings.Split(snippet, "\n") {
		line := strings.TrimSpace(strings.Trim(raw, "\r"))
		line = strings.TrimPrefix(line, "-")
		line = strings.TrimSpace(line)
		switch {
		case url == "" && strings.HasPrefix(line, "url:"):
			url = strings.TrimSpace(strings.TrimPrefix(line, "url:"))
		case date == "" && (strings.HasPrefix(line, "date_added:") || strings.HasPrefix(line, "date:")):
			_, v, _ := strings.Cut(line, ":")
			date = strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return url, date
}

// cleanSnippet strips qmd's "@@ ... @@" diff-hunk header line so the
// envelope's snippet is just readable text.
func cleanSnippet(snippet string) string {
	lines := strings.Split(snippet, "\n")
	kept := lines[:0]
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "@@") {
			continue
		}
		kept = append(kept, l)
	}
	return strings.TrimSpace(strings.Join(kept, " "))
}

func printSuccess(results []Result) int {
	if results == nil {
		results = []Result{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(successEnvelope{OK: true, Results: results}); err != nil {
		fmt.Fprintln(os.Stderr, "search_library: encode:", err)
		return 1
	}
	return 0
}

// fail prints the {"ok":false,"error":"..."} envelope (or a plain-language
// line in --pretty mode) and returns the process exit code.
func fail(msg string, pretty bool) int {
	if pretty {
		fmt.Printf("\x1b[1;91msearch_library error:\x1b[0m %s\n", msg)
		return 1
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(errorEnvelope{OK: false, Error: msg}); err != nil {
		fmt.Fprintln(os.Stderr, "search_library: encode:", err)
	}
	return 1
}

// ANSI, high-contrast (bold + bright) — never dim this for "accessibility";
// bright color on a dark terminal is the accessibility aid, not a decoration.
const (
	clrReset   = "\x1b[0m"
	clrTitle   = "\x1b[1;95m" // bold bright magenta
	clrLabel   = "\x1b[1;93m" // bold bright yellow
	clrURL     = "\x1b[92m"   // bright green
	clrSnippet = "\x1b[97m"   // bright white
	clrDim     = "\x1b[96m"   // bright cyan (date/source)
)

func printPretty(query string, results []Result) {
	fmt.Printf("%s%d result(s) for \"%s\"%s\n\n", clrLabel, len(results), query, clrReset)
	if len(results) == 0 {
		fmt.Printf("%sno matches — try a different phrase, or check the library/ai-chats index is up to date.%s\n", clrDim, clrReset)
		return
	}
	for i, r := range results {
		fmt.Printf("%s%d. %s%s\n", clrTitle, i+1, r.Title, clrReset)
		fmt.Printf("   %ssource:%s %-14s %sscore:%s %.2f\n", clrLabel, clrReset, r.Source, clrLabel, clrReset, r.Score)
		if r.URL != "" {
			fmt.Printf("   %s%s%s\n", clrURL, r.URL, clrReset)
		}
		if r.Date != "" {
			fmt.Printf("   %s%s%s\n", clrDim, r.Date, clrReset)
		}
		if r.Snippet != "" {
			fmt.Printf("   %s%s%s\n", clrSnippet, truncate(r.Snippet, 160), clrReset)
		}
		fmt.Printf("   %s%s%s\n\n", clrDim, r.Path, clrReset)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
