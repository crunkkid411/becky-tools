// becky-web-search — Google Programmable Search (Custom Search JSON API),
// a real live-web-results world-knowledge channel: one dumb call, no
// browser, no cookies (AUTOPILOT.md WHORETANA ask #2 / buildplan Phase 3,
// ported from Mark-XXXIX's actions/web_search.py).
//
// Backend choice differs from Mark-XXXIX on purpose: Mark-XXXIX called
// Gemini generative search (google_search grounding) as primary with a
// DuckDuckGo HTML-scrape fallback. DuckDuckGo's html.duckduckgo.com AND
// lite.duckduckgo.com endpoints were both live-tested on 2026-07-10 and now
// return a bot-challenge (anomaly-modal CAPTCHA) to a plain unauthenticated
// GET with no session - that fallback was dropped rather than shipped
// broken. Google's Custom Search JSON API needed no new research: it was
// already curated in the local services-index.md specifically for this
// ("Pure-API web search channel, no browser — could back a becky research
// tool"), has a real free tier, and needs no Gemini/generative-search
// dependency for a plain "search the web" tool - a raw SERP is exactly what
// "one dumb call" wants back.
//
//	becky-web-search "query text" [--max 8] [--json]
//	becky-web-search --selftest      # offline, no-network proof of the pipeline
//
// Credentials: BECKY_GOOGLE_CSE_KEY + BECKY_GOOGLE_CSE_CX env vars, or
// resolved at call time from the gitignored API-keys manifest (Law 18d) -
// same pointer chain as becky-notify (BECKY_API_MANIFEST_PATH or
// X:\AI-2\hj-mission-control\data\.api-manifest-path), reading its "Search
// api:" / "Search engine ID:" lines. Read at call time, kept in process
// memory only, masked in every error and log - never written, never echoed.
//
// Exit codes: 0 = ran, results returned (incl. zero results - a real "no
// hits" answer, not a failure); 1 = couldn't complete the search (missing
// credentials, network failure, API error) - always {"ok":false,...} on
// stdout either way; 2 = usage error.
package main

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"becky-go/internal/beckyio"
)

func main() {
	max, asJSON, selftest, query := extractFlags(os.Args[1:])

	if selftest {
		os.Exit(runSelftest())
	}

	if query == "" {
		fmt.Fprintln(os.Stderr, `usage: becky-web-search "query text" [--max 8] [--json]`)
		os.Exit(2)
	}

	key, cx, err := resolveCreds()
	if err != nil {
		fail(asJSON, query, err)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	items, err := search(client, key, cx, query, max)
	if err != nil {
		fail(asJSON, query, err)
	}

	res := Result{OK: true, Query: query, Backend: "google_cse", Count: len(items), Results: items}
	if !asJSON {
		printPlain(res)
	}
	beckyio.PrintJSON(res)
}

// extractFlags scans args wherever --json/--max/--selftest appear (Go's
// stdlib flag package stops parsing at the first non-flag arg, which would
// wrongly reject `becky-web-search "query" --json` - the exact bug
// cmd/notify already hit and fixed with extractJSONFlag; caught again here
// via a real live-API run during this tick before it shipped). Every
// remaining token is joined back into the query in its original order.
func extractFlags(args []string) (max int, asJSON, selftest bool, query string) {
	max = 8
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json", "-json":
			asJSON = true
		case "--selftest", "-selftest":
			selftest = true
		case "--max", "-max":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					max = n
				}
				i++
			}
		default:
			rest = append(rest, a)
		}
	}
	query = strings.TrimSpace(strings.Join(rest, " "))
	return max, asJSON, selftest, query
}

// fail prints the plain-language line (unless --json) and the JSON envelope,
// then exits 1. Every error string that reaches here has already had any
// secret masked by the caller that formed it (Law 18d).
func fail(asJSON bool, query string, err error) {
	if !asJSON {
		fmt.Fprintln(os.Stderr, "becky-web-search:", err)
	}
	beckyio.PrintJSON(Result{OK: false, Query: query, Error: err.Error()})
	os.Exit(1)
}

// printPlain writes the human-readable report to stderr - stdout always
// carries the JSON envelope (beckyio's "structured JSON to stdout, human
// diagnostics to stderr" contract), so becky-web-search is pipeable either
// way.
func printPlain(res Result) {
	if res.Count == 0 {
		fmt.Fprintf(os.Stderr, "No results for: %s\n", res.Query)
		return
	}
	fmt.Fprintf(os.Stderr, "Search results for: %s\n\n", res.Query)
	for i, it := range res.Results {
		fmt.Fprintf(os.Stderr, "%d. %s\n   %s\n   %s\n\n", i+1, it.Title, it.Snippet, it.URL)
	}
}

// runSelftest is the one-command, OFFLINE, no-network proof of the real code
// path: credential extraction, secret masking/redaction, response parsing,
// and result clamping - no live Google Custom Search call required. This is
// becky's "provable handoff" gate.
func runSelftest() int {
	manifest := "\t- - -   S E A R C H   - - -\nSearch engine ID: a2a87b7912a7b438b\n" +
		"Search api: AIzaSyFAKE0000000000000000000000000\nhttps://cse.google.com/cse?cx=a2a87b7912a7b438b\n"
	key, cx := extractSearchCreds(manifest)

	body := []byte(`{"items":[{"title":"Example","link":"https://example.com","snippet":"an example result"}]}`)
	items, parseErr := parseSearchResponse(body, key)

	errBody := []byte(`{"error":{"message":"API key not valid: ` + key + `"}}`)
	_, apiErr := parseSearchResponse(errBody, key)

	type check struct {
		name string
		ok   bool
	}
	checks := []check{
		{"extracts Search api key from manifest", key == "AIzaSyFAKE0000000000000000000000000"},
		{"extracts Search engine ID from manifest", cx == "a2a87b7912a7b438b"},
		{"maskKey never contains the raw key", key != "" && !strings.Contains(maskKey(key), key)},
		{"redact scrubs the key out of an error string", !strings.Contains(redact("url had "+key+" in it", key), key)},
		{"parseSearchResponse decodes a real CSE JSON shape", parseErr == nil && len(items) == 1 && items[0].URL == "https://example.com"},
		{"parseSearchResponse surfaces API errors, key redacted", apiErr != nil && !strings.Contains(apiErr.Error(), key) && strings.Contains(apiErr.Error(), maskKey(key))},
		{"maxResults clamps to Google's 1-10 range", maxResults(0) == 8 && maxResults(50) == 10 && maxResults(3) == 3},
	}

	failed := 0
	for _, c := range checks {
		status := "PASS"
		if !c.ok {
			status = "FAIL"
			failed++
		}
		fmt.Printf("[%s] %s\n", status, c.name)
	}
	fmt.Println()
	if failed == 0 {
		fmt.Printf("becky-web-search selftest: PASS (%d/%d checks)\n", len(checks), len(checks))
		return 0
	}
	fmt.Printf("becky-web-search selftest: FAIL (%d/%d checks failed)\n", failed, len(checks))
	return 1
}
