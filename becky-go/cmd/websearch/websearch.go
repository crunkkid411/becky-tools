// websearch.go — the pure, testable core of becky-web-search: resolving the
// Google Programmable Search (Custom Search JSON API) credentials and
// talking to the API. main.go is just flag parsing + wiring; everything with
// a decision in it lives here so it can be unit-tested without a real
// network call or a real manifest file (same split as cmd/notify).
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Result is becky-web-search's stdout JSON envelope, always one of:
//
//	{"ok":true,"query":"...","backend":"google_cse","count":N,"results":[...]}
//	{"ok":false,"query":"...","error":"..."}
type Result struct {
	OK      bool         `json:"ok"`
	Query   string       `json:"query,omitempty"`
	Backend string       `json:"backend,omitempty"`
	Count   int          `json:"count,omitempty"`
	Results []SearchItem `json:"results,omitempty"`
	Error   string       `json:"error,omitempty"`
}

// SearchItem is one web result.
type SearchItem struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// defaultManifestPointer is the gitignored file (in the sibling hj-mission-control
// repo) that names the real, messy API-keys manifest on this machine
// (AUTOPILOT.md Law 18d). BECKY_API_MANIFEST_PATH overrides it directly with the
// manifest file itself, skipping the pointer indirection. Identical constant to
// cmd/notify's - both tools resolve the same manifest, just different sections.
const defaultManifestPointer = `X:\AI-2\hj-mission-control\data\.api-manifest-path`

// searchAPIKeyRe / searchEngineIDRe match the manifest's own labeled lines
// under its "SEARCH" section ("Search api: <key>" / "Search engine ID: <id>"),
// verified live against the real manifest on 2026-07-10. Label-anchored
// (unlike cmd/notify's shape-only regex) because both an API key and a CX id
// are plain alphanumeric tokens with no distinctive shape of their own, and
// the manifest also contains unrelated Google API keys (Gemini) that must NOT
// be picked up here.
var (
	searchAPIKeyRe   = regexp.MustCompile(`(?i)search api:\s*(\S+)`)
	searchEngineIDRe = regexp.MustCompile(`(?i)search engine id:\s*(\S+)`)
)

// resolveManifestPath finds the real manifest file: BECKY_API_MANIFEST_PATH if
// set (as the manifest itself), else the gitignored pointer file's contents.
func resolveManifestPath() (string, error) {
	if p := strings.TrimSpace(os.Getenv("BECKY_API_MANIFEST_PATH")); p != "" {
		return p, nil
	}
	raw, err := os.ReadFile(defaultManifestPointer)
	if err != nil {
		return "", fmt.Errorf("no Google Custom Search credentials configured: set BECKY_GOOGLE_CSE_KEY+BECKY_GOOGLE_CSE_CX, or BECKY_API_MANIFEST_PATH, or create %s (%w)", defaultManifestPointer, err)
	}
	path := strings.TrimSpace(string(raw))
	if path == "" {
		return "", fmt.Errorf("%s is empty", defaultManifestPointer)
	}
	return path, nil
}

// resolveCreds finds the Google Custom Search API key and search-engine (cx)
// id. Law 18d: read at call time, never write either anywhere, keep them in
// process memory only. Env vars win outright per-field; the manifest fills in
// whichever one is still missing.
func resolveCreds() (key, cx string, err error) {
	key = strings.TrimSpace(os.Getenv("BECKY_GOOGLE_CSE_KEY"))
	cx = strings.TrimSpace(os.Getenv("BECKY_GOOGLE_CSE_CX"))
	if key != "" && cx != "" {
		return key, cx, nil
	}
	manifestPath, mErr := resolveManifestPath()
	if mErr != nil {
		return "", "", mErr
	}
	raw, rErr := os.ReadFile(manifestPath)
	if rErr != nil {
		return "", "", fmt.Errorf("read API manifest: %w", rErr)
	}
	mKey, mCx := extractSearchCreds(string(raw))
	if key == "" {
		key = mKey
	}
	if cx == "" {
		cx = mCx
	}
	if key == "" || cx == "" {
		return "", "", fmt.Errorf("no Google Custom Search credentials found - need both a 'Search api:' key and a 'Search engine ID:' line in the manifest, or BECKY_GOOGLE_CSE_KEY + BECKY_GOOGLE_CSE_CX env vars")
	}
	return key, cx, nil
}

// extractSearchCreds is the pure parse step of resolveCreds, testable without
// touching the filesystem.
func extractSearchCreds(manifest string) (key, cx string) {
	if m := searchAPIKeyRe.FindStringSubmatch(manifest); m != nil {
		key = m[1]
	}
	if m := searchEngineIDRe.FindStringSubmatch(manifest); m != nil {
		cx = m[1]
	}
	return key, cx
}

// maskKey shows just enough of a key to confirm it's the right one without
// ever revealing the secret (Law 18d).
func maskKey(key string) string {
	if len(key) <= 10 {
		return "***"
	}
	return key[:6] + "..." + key[len(key)-4:]
}

// redact replaces every occurrence of the raw key in s with its masked form.
// Go's http.Client errors embed the full request URL (which contains the
// key as a query param) in their Error() text, so every error string built
// from a failed request MUST pass through this before it can reach stderr,
// JSON output, or a WORKLOG.
func redact(s, key string) string {
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, maskKey(key))
}

// httpGetter is the one method websearch.go needs from *http.Client, so
// tests can fake the network without a real server.
type httpGetter interface {
	Get(url string) (*http.Response, error)
}

// maxResults clamps to Google Custom Search's own per-call cap.
func maxResults(n int) int {
	if n <= 0 {
		return 8
	}
	if n > 10 {
		return 10
	}
	return n
}

// cseResponse is the shape of Google Custom Search JSON API's response,
// trimmed to what becky-web-search uses.
type cseResponse struct {
	Items []struct {
		Title   string `json:"title"`
		Link    string `json:"link"`
		Snippet string `json:"snippet"`
	} `json:"items"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// search calls Google Custom Search JSON API and returns the results.
func search(client httpGetter, key, cx, query string, max int) ([]SearchItem, error) {
	endpoint := "https://www.googleapis.com/customsearch/v1?" + url.Values{
		"key": {key},
		"cx":  {cx},
		"q":   {query},
		"num": {strconv.Itoa(maxResults(max))},
	}.Encode()
	resp, err := client.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("network error reaching Google Custom Search: %s", redact(err.Error(), key))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read Google Custom Search response: %w", err)
	}
	return parseSearchResponse(body, key)
}

// parseSearchResponse is the pure parse step of search, testable without a
// network call.
func parseSearchResponse(body []byte, key string) ([]SearchItem, error) {
	var r cseResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("unexpected Google Custom Search response: %w", err)
	}
	if r.Error != nil {
		msg := redact(r.Error.Message, key)
		if strings.Contains(strings.ToLower(msg), "does not have the access") {
			msg += " (one-time fix: enable it at https://console.cloud.google.com/apis/library/customsearch.googleapis.com for the Google Cloud project that owns this key, then retry)"
		}
		return nil, fmt.Errorf("google Custom Search API error: %s", msg)
	}
	items := make([]SearchItem, 0, len(r.Items))
	for _, it := range r.Items {
		items = append(items, SearchItem{Title: it.Title, URL: it.Link, Snippet: it.Snippet})
	}
	return items, nil
}
