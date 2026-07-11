// gmail.go — the pure, testable core of becky-gmail: resolving the Google
// OAuth client credentials, building the consent URL, exchanging/refreshing
// tokens, calling the Gmail REST API READ-ONLY, and extracting links from a
// message. main.go is just flag parsing + wiring + the loopback listener;
// everything with a decision in it lives here so it can be unit-tested
// without a real network call or a real manifest file (same split as
// cmd/notify and cmd/websearch).
//
// SCOPE IS READ-ONLY, HARD (Law 19): the only OAuth scope this tool ever
// requests is gmailReadonlyScope. There is no send/compose/modify/delete
// path anywhere in this package. becky-gmail can read + surface, never act.
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// gmailReadonlyScope is the ONE and ONLY OAuth scope becky-gmail requests.
// Hard-coded, asserted in tests and the selftest: if anyone ever widens this
// to send/compose/modify/delete, the readonly-scope check fails loudly.
const gmailReadonlyScope = "https://www.googleapis.com/auth/gmail.readonly"

// Google OAuth + Gmail REST endpoints (installed-app / loopback flow).
const (
	authEndpoint  = "https://accounts.google.com/o/oauth2/v2/auth"
	tokenEndpoint = "https://oauth2.googleapis.com/token"
	gmailAPIBase  = "https://gmail.googleapis.com/gmail/v1/users/me"
)

// defaultManifestPointer is the gitignored file (in the sibling hj-mission-control
// repo) that names the real, messy API-keys manifest on this machine
// (AUTOPILOT.md Law 18d). BECKY_API_MANIFEST_PATH overrides it directly with the
// manifest file itself, skipping the pointer indirection. Identical constant to
// cmd/notify's and cmd/websearch's — all three resolve the same manifest, just
// different sections.
const defaultManifestPointer = `X:\AI-2\hj-mission-control\data\.api-manifest-path`

// oauthClientIDRe / oauthClientSecretRe match the Google OAuth client's own
// distinctive shapes in the manifest — a client id ending
// ".apps.googleusercontent.com" and a secret starting "GOCSPX-". Shape-anchored
// (like cmd/notify's Telegram-token regex, NOT label-anchored) because the
// manifest labels the same pair two different ways ("Client ID:" and
// "Client ID =") and nothing else in the file shares these exact shapes, so one
// regex each is more robust than fragile line-position parsing of a hand-edited
// file.
var (
	oauthClientIDRe     = regexp.MustCompile(`\b\d{6,}-[a-z0-9]+\.apps\.googleusercontent\.com\b`)
	oauthClientSecretRe = regexp.MustCompile(`\bGOCSPX-[A-Za-z0-9_-]{10,}\b`)
)

// oauthCreds is the OAuth client id + secret, read at call time and kept in
// process memory only (Law 18d).
type oauthCreds struct {
	clientID     string
	clientSecret string
}

// resolveManifestPath finds the real manifest file: BECKY_API_MANIFEST_PATH if
// set (as the manifest itself), else the gitignored pointer file's contents.
func resolveManifestPath() (string, error) {
	if p := strings.TrimSpace(os.Getenv("BECKY_API_MANIFEST_PATH")); p != "" {
		return p, nil
	}
	raw, err := os.ReadFile(defaultManifestPointer)
	if err != nil {
		return "", fmt.Errorf("no Google OAuth client configured: set BECKY_GOOGLE_OAUTH_CLIENT_ID+BECKY_GOOGLE_OAUTH_CLIENT_SECRET, or BECKY_API_MANIFEST_PATH, or create %s (%w)", defaultManifestPointer, err)
	}
	path := strings.TrimSpace(string(raw))
	if path == "" {
		return "", fmt.Errorf("%s is empty", defaultManifestPointer)
	}
	return path, nil
}

// resolveCreds finds the Google OAuth client id + secret. Law 18d: read at
// call time, never write either anywhere, keep them in process memory only.
// Env vars win outright per-field; the manifest fills in whichever is missing.
func resolveCreds() (oauthCreds, error) {
	id := strings.TrimSpace(os.Getenv("BECKY_GOOGLE_OAUTH_CLIENT_ID"))
	secret := strings.TrimSpace(os.Getenv("BECKY_GOOGLE_OAUTH_CLIENT_SECRET"))
	if id != "" && secret != "" {
		return oauthCreds{id, secret}, nil
	}
	manifestPath, err := resolveManifestPath()
	if err != nil {
		return oauthCreds{}, err
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return oauthCreds{}, fmt.Errorf("read API manifest: %w", err)
	}
	mID, mSecret := extractOAuthCreds(string(raw))
	if id == "" {
		id = mID
	}
	if secret == "" {
		secret = mSecret
	}
	if id == "" || secret == "" {
		return oauthCreds{}, fmt.Errorf("no Google OAuth client found - need a '...apps.googleusercontent.com' client id and a 'GOCSPX-...' client secret in the manifest, or the BECKY_GOOGLE_OAUTH_CLIENT_ID + BECKY_GOOGLE_OAUTH_CLIENT_SECRET env vars")
	}
	return oauthCreds{id, secret}, nil
}

// extractOAuthCreds is the pure parse step of resolveCreds, testable without
// touching the filesystem.
func extractOAuthCreds(manifest string) (id, secret string) {
	id = oauthClientIDRe.FindString(manifest)
	secret = oauthClientSecretRe.FindString(manifest)
	return id, secret
}

// maskSecret shows just enough of a secret to confirm it's the right one
// without ever revealing it (Law 18d - masked in every log/output/error).
func maskSecret(s string) string {
	if len(s) <= 10 {
		return "***"
	}
	return s[:6] + "..." + s[len(s)-4:]
}

// redact replaces every occurrence of the raw secret in s with its masked
// form. Any error string that could carry a secret MUST pass through this
// before it can reach stderr, JSON output, or a WORKLOG.
func redact(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, maskSecret(secret))
}

// --- Token store -----------------------------------------------------------

// tokenStore is the on-disk shape of the gitignored refresh-token file. Only
// the refresh token is persisted; access tokens are short-lived and re-minted
// each run.
type tokenStore struct {
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	Obtained     string `json:"obtained"`
}

// tokenPath is the gitignored local file holding the refresh token, under the
// user's home so it never lands in any repo. BECKY_GMAIL_TOKEN_PATH overrides
// it (used by tests to isolate from a real token).
func tokenPath() string {
	if p := strings.TrimSpace(os.Getenv("BECKY_GMAIL_TOKEN_PATH")); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".becky", "gmail-token.json")
}

// loadToken reads the stored refresh token, returning ok=false (not an error)
// when no token file exists yet — the "not authorized" signal.
func loadToken() (tokenStore, bool) {
	raw, err := os.ReadFile(tokenPath())
	if err != nil {
		return tokenStore{}, false
	}
	var ts tokenStore
	if err := json.Unmarshal(raw, &ts); err != nil || strings.TrimSpace(ts.RefreshToken) == "" {
		return tokenStore{}, false
	}
	return ts, true
}

// saveToken writes the refresh token to the gitignored store with 0600-ish
// perms (private to the user).
func saveToken(ts tokenStore) error {
	p := tokenPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("create token dir: %w", err)
	}
	raw, err := json.MarshalIndent(ts, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, raw, 0o600)
}

// --- OAuth consent URL + token exchange ------------------------------------

// buildConsentURL assembles the Google consent URL for the installed-app /
// loopback flow: access_type=offline + prompt=consent (so Google returns a
// refresh token every time), the READ-ONLY scope only, and the loopback
// redirect the local listener is bound to. state is an anti-CSRF nonce the
// listener checks on the way back.
func buildConsentURL(clientID, redirectURI, state string) string {
	q := url.Values{
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"scope":         {gmailReadonlyScope},
		"access_type":   {"offline"},
		"prompt":        {"consent"},
		"state":         {state},
	}
	return authEndpoint + "?" + q.Encode()
}

// tokenResponse is the shape of Google's token endpoint response (both the
// authorization_code exchange and the refresh_token refresh use it).
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// httpPoster / httpGetter are the tiny slices of *http.Client the core needs,
// so tests can fake the network without a real server.
type httpPoster interface {
	PostForm(url string, data url.Values) (*http.Response, error)
}
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// exchangeCode swaps an authorization code for tokens. The client secret lives
// only in the POST body (never a URL), and any error is redacted just in case.
func exchangeCode(client httpPoster, c oauthCreds, code, redirectURI string) (tokenResponse, error) {
	form := url.Values{
		"code":          {code},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"redirect_uri":  {redirectURI},
		"grant_type":    {"authorization_code"},
	}
	return postToken(client, form, c.clientSecret)
}

// refreshAccessToken mints a fresh access token from the stored refresh token.
func refreshAccessToken(client httpPoster, c oauthCreds, refreshToken string) (string, error) {
	form := url.Values{
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"refresh_token": {refreshToken},
		"grant_type":    {"refresh_token"},
	}
	tr, err := postToken(client, form, c.clientSecret)
	if err != nil {
		return "", err
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("token refresh returned no access token")
	}
	return tr.AccessToken, nil
}

// postToken POSTs a form to Google's token endpoint and parses the response,
// redacting the secret from any error.
func postToken(client httpPoster, form url.Values, secret string) (tokenResponse, error) {
	resp, err := client.PostForm(tokenEndpoint, form)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("network error reaching Google token endpoint: %s", redact(err.Error(), secret))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("read Google token response: %w", err)
	}
	return parseTokenResponse(body, secret)
}

// parseTokenResponse is the pure parse step, testable without a network call.
func parseTokenResponse(body []byte, secret string) (tokenResponse, error) {
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return tokenResponse{}, fmt.Errorf("unexpected Google token response: %s", redact(string(body), secret))
	}
	if tr.Error != "" {
		msg := tr.Error
		if tr.ErrorDesc != "" {
			msg += ": " + tr.ErrorDesc
		}
		return tokenResponse{}, fmt.Errorf("google OAuth error: %s", redact(msg, secret))
	}
	return tr, nil
}

// --- Gmail REST (READ-ONLY) ------------------------------------------------

// listMessageIDs calls users.messages.list and returns the matching message
// ids (newest first, as Gmail returns them). q is a Gmail search query.
func listMessageIDs(client httpDoer, accessToken, q string, max int) ([]string, error) {
	endpoint := gmailAPIBase + "/messages?" + url.Values{
		"q":          {q},
		"maxResults": {itoa(max)},
	}.Encode()
	body, err := gmailGet(client, endpoint, accessToken)
	if err != nil {
		return nil, err
	}
	return parseListResponse(body)
}

// parseListResponse is the pure parse step of listMessageIDs.
func parseListResponse(body []byte) ([]string, error) {
	var r struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("unexpected Gmail list response: %w", err)
	}
	if r.Error != nil {
		return nil, fmt.Errorf("gmail API error (list): %s", r.Error.Message)
	}
	ids := make([]string, 0, len(r.Messages))
	for _, m := range r.Messages {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

// gmailPart is one MIME part of a message payload (recursive).
type gmailPart struct {
	MimeType string       `json:"mimeType"`
	Headers  []gmailHdr   `json:"headers"`
	Body     gmailBodyRaw `json:"body"`
	Parts    []gmailPart  `json:"parts"`
}
type gmailHdr struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type gmailBodyRaw struct {
	Data string `json:"data"`
}

// gmailMessage is the trimmed users.messages.get (format=full) shape.
type gmailMessage struct {
	ID      string    `json:"id"`
	Snippet string    `json:"snippet"`
	Payload gmailPart `json:"payload"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Message is becky-gmail's own flattened, caller-friendly view of one email.
type Message struct {
	ID      string `json:"id"`
	From    string `json:"from,omitempty"`
	Subject string `json:"subject,omitempty"`
	Date    string `json:"date,omitempty"`
	Snippet string `json:"snippet,omitempty"`
	Text    string `json:"text,omitempty"`
	Links   []Link `json:"links,omitempty"`
}

// Link is one extracted hyperlink: the (unwrapped) URL and its anchor text.
type Link struct {
	URL  string `json:"url"`
	Text string `json:"text,omitempty"`
}

// getMessage fetches one message (format=full) and flattens it.
func getMessage(client httpDoer, accessToken, id string) (Message, error) {
	endpoint := gmailAPIBase + "/messages/" + url.PathEscape(id) + "?format=full"
	body, err := gmailGet(client, endpoint, accessToken)
	if err != nil {
		return Message{}, err
	}
	return parseMessage(body)
}

// parseMessage is the pure parse+flatten step of getMessage.
func parseMessage(body []byte) (Message, error) {
	var gm gmailMessage
	if err := json.Unmarshal(body, &gm); err != nil {
		return Message{}, fmt.Errorf("unexpected Gmail message response: %w", err)
	}
	if gm.Error != nil {
		return Message{}, fmt.Errorf("gmail API error (get): %s", gm.Error.Message)
	}
	msg := Message{
		ID:      gm.ID,
		Snippet: gm.Snippet,
		From:    headerValue(gm.Payload.Headers, "From"),
		Subject: headerValue(gm.Payload.Headers, "Subject"),
		Date:    headerValue(gm.Payload.Headers, "Date"),
	}
	var htmlParts, textParts []string
	collectBodies(gm.Payload, &htmlParts, &textParts)
	msg.Text = strings.TrimSpace(strings.Join(textParts, "\n"))
	if msg.Text == "" {
		// No text/plain part: fall back to a tag-stripped view of the HTML.
		msg.Text = strings.TrimSpace(stripTags(strings.Join(htmlParts, "\n")))
	}
	msg.Links = extractLinks(strings.Join(htmlParts, "\n"), strings.Join(textParts, "\n"))
	return msg, nil
}

// headerValue returns the first header matching name (case-insensitive).
func headerValue(headers []gmailHdr, name string) string {
	for _, h := range headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

// collectBodies walks the MIME tree, decoding text/plain and text/html bodies.
func collectBodies(p gmailPart, html, text *[]string) {
	if p.Body.Data != "" {
		decoded := string(decodeBody(p.Body.Data))
		switch {
		case strings.HasPrefix(strings.ToLower(p.MimeType), "text/html"):
			*html = append(*html, decoded)
		case strings.HasPrefix(strings.ToLower(p.MimeType), "text/plain"):
			*text = append(*text, decoded)
		}
	}
	for _, child := range p.Parts {
		collectBodies(child, html, text)
	}
}

// decodeBody decodes Gmail's web-safe (URL-safe) base64 body, tolerating the
// missing padding Gmail sometimes omits.
func decodeBody(data string) []byte {
	data = strings.TrimRight(data, "=")
	if b, err := base64.RawURLEncoding.DecodeString(data); err == nil {
		return b
	}
	if b, err := base64.RawStdEncoding.DecodeString(data); err == nil {
		return b
	}
	return nil
}

// gmailGet does an authenticated GET against the Gmail API and returns the raw
// body. The access token is a Bearer header, never a URL query param, so it
// cannot leak into an http.Client error string.
func gmailGet(client httpDoer, endpoint, accessToken string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network error reaching Gmail API: %w", err)
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// --- Link extraction -------------------------------------------------------

var (
	anchorRe   = regexp.MustCompile(`(?is)<a\s+[^>]*?href\s*=\s*["']([^"']+)["'][^>]*>(.*?)</a>`)
	plainURLRe = regexp.MustCompile(`https?://[^\s<>"')\]]+`)
	tagRe      = regexp.MustCompile(`(?s)<[^>]*>`)
)

// trackingParams are the query keys common newsletter click-trackers use to
// wrap the real destination URL. If a link carries one whose value decodes to
// an http(s) URL, that inner URL is the one the caller actually wants.
var trackingParams = []string{"url", "u", "redirect", "target", "dest", "destination", "link", "ns_url"}

// extractLinks pulls links from an HTML body (with anchor text) and any bare
// URLs in the text/plain body, unwraps trivial click-tracking redirects, and
// de-duplicates by final URL (first anchor text wins).
func extractLinks(html, text string) []Link {
	var links []Link
	seen := map[string]bool{}
	addLink := func(rawURL, anchor string) {
		rawURL = strings.TrimSpace(html_unescape(rawURL))
		if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
			return
		}
		final := unwrapTracking(rawURL)
		if seen[final] {
			return
		}
		seen[final] = true
		links = append(links, Link{URL: final, Text: strings.TrimSpace(collapseWS(stripTags(anchor)))})
	}
	for _, m := range anchorRe.FindAllStringSubmatch(html, -1) {
		addLink(m[1], m[2])
	}
	for _, u := range plainURLRe.FindAllString(text, -1) {
		addLink(strings.TrimRight(u, ".,);"), "")
	}
	return links
}

// unwrapTracking returns the inner destination URL if rawURL is a trivial
// click-tracking wrapper (real URL carried in a known query param), else the
// URL unchanged. ponytail: one decode pass — a doubly-wrapped tracker keeps
// its first layer; upgrade to a loop only if a real newsletter needs it.
func unwrapTracking(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := u.Query()
	for _, key := range trackingParams {
		if v := q.Get(key); v != "" {
			if inner, err := url.QueryUnescape(v); err == nil {
				inner = strings.TrimSpace(inner)
				if strings.HasPrefix(inner, "http://") || strings.HasPrefix(inner, "https://") {
					return inner
				}
			}
		}
	}
	return rawURL
}

// stripTags removes HTML tags, leaving the visible text.
func stripTags(s string) string { return tagRe.ReplaceAllString(s, "") }

// collapseWS collapses runs of whitespace to single spaces.
func collapseWS(s string) string { return strings.Join(strings.Fields(s), " ") }

// html_unescape decodes the handful of HTML entities that actually show up in
// hrefs (chiefly &amp; splitting query params). Kept tiny on purpose.
func html_unescape(s string) string {
	r := strings.NewReplacer("&amp;", "&", "&#38;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'")
	return r.Replace(s)
}

// itoa is a tiny strconv.Itoa shim so main.go and gmail.go share one spelling.
func itoa(n int) string { return fmt.Sprintf("%d", n) }
