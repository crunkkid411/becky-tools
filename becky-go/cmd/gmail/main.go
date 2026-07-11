// becky-gmail — a READ-ONLY Gmail reader. One dumb call: search Jordan's mail
// or read a message, and get back the links inside it (so a caller can pull
// "the Trackspacer purchase link from today's PluginBoutique email"). Pure
// Gmail REST over OAuth; no browser automation, and — by hard design — no
// send/compose/modify/delete anywhere (Law 19: read + surface, never act).
//
//	becky-gmail auth                         # one-time consent (prints a URL Jordan clicks)
//	becky-gmail auth status                  # is a refresh token on file?
//	becky-gmail search "<query>" [--newer-than 1d] [--max 10] [--json]
//	becky-gmail get <id> [--links-only] [--json]
//	becky-gmail --selftest                   # offline, no-network proof of the pipeline
//
// OAuth client: BECKY_GOOGLE_OAUTH_CLIENT_ID + BECKY_GOOGLE_OAUTH_CLIENT_SECRET
// env vars, or resolved at call time from the gitignored API-keys manifest
// (Law 18d) — same pointer chain as becky-notify/becky-web-search. Read at call
// time, kept in process memory only, masked in every error/log, never written
// or echoed. The one thing that IS persisted is the OAuth *refresh token*, in a
// gitignored file under %USERPROFILE%\.becky\gmail-token.json (0600).
//
// The human step (Jordan clicking "Allow") happens in HIS OWN browser — this
// tool never automates or clicks it. `auth` prints the consent URL and waits on
// a loopback listener for the redirect.
//
// Exit codes: 0 = ran; 1 = couldn't complete (not authorized, network/API
// failure) — always {"ok":false,...} on stdout either way; 2 = usage error.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"becky-go/internal/beckyio"
)

// authTimeout bounds how long `auth` waits on the loopback for Jordan's click
// before giving up (degrade, never hang forever).
const authTimeout = 5 * time.Minute

func main() {
	args := os.Args[1:]

	// Global --selftest short-circuits everything: offline, no creds, no network.
	for _, a := range args {
		if a == "--selftest" || a == "-selftest" {
			os.Exit(runSelftest())
		}
	}

	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	sub, rest := args[0], args[1:]
	switch sub {
	case "auth":
		runAuth(rest)
	case "search":
		runSearch(rest)
	case "get":
		runGet(rest)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  becky-gmail auth                         one-time consent (prints a URL to click)
  becky-gmail auth status                  is a refresh token on file?
  becky-gmail search "<query>" [--newer-than 1d] [--max 10] [--json]
  becky-gmail get <id> [--links-only] [--json]
  becky-gmail --selftest`)
}

// Result is becky-gmail's stdout JSON envelope. Fields are omitempty so each
// subcommand emits only what it produced (one dumb call → one JSON doc).
type Result struct {
	OK         bool      `json:"ok"`
	Error      string    `json:"error,omitempty"`
	Authorized *bool     `json:"authorized,omitempty"`
	ConsentURL string    `json:"consent_url,omitempty"`
	TokenPath  string    `json:"token_path,omitempty"`
	Scope      string    `json:"scope,omitempty"`
	Query      string    `json:"query,omitempty"`
	Count      int       `json:"count,omitempty"`
	Messages   []Message `json:"messages,omitempty"`
	Message    *Message  `json:"message,omitempty"`
}

// fail prints the plain-language line (unless --json) and the JSON envelope,
// then exits 1. Every error string that reaches here has already had any
// secret masked by the caller that formed it (Law 18d).
func fail(asJSON bool, err error) {
	if !asJSON {
		fmt.Fprintln(os.Stderr, "becky-gmail:", err)
	}
	beckyio.PrintJSON(Result{OK: false, Error: err.Error()})
	os.Exit(1)
}

// failNotAuthorized is the exact degrade path when no refresh token is on file
// yet: a clear, machine-stable message telling the caller to run `auth`.
func failNotAuthorized(asJSON bool) {
	err := fmt.Errorf("not authorized - run becky-gmail auth")
	fail(asJSON, err)
}

// httpClient is the shared client for all network calls.
func httpClient() *http.Client { return &http.Client{Timeout: 30 * time.Second} }

// --- auth ------------------------------------------------------------------

func runAuth(args []string) {
	status, asJSON, port := extractAuthFlags(args)
	if status {
		runAuthStatus(asJSON)
		return
	}

	creds, err := resolveCreds()
	if err != nil {
		fail(asJSON, err)
	}

	actualPort, codeCh, closeListener, err := startLoopbackListener(port)
	if err != nil {
		fail(asJSON, fmt.Errorf("could not start local consent listener on 127.0.0.1: %w", err))
	}
	defer closeListener()

	redirectURI := fmt.Sprintf("http://127.0.0.1:%d", actualPort)
	state := randomState()
	consentURL := buildConsentURL(creds.clientID, redirectURI, state)

	if !asJSON {
		fmt.Fprintln(os.Stderr, "becky-gmail: open this URL in YOUR browser and click Allow (read-only Gmail access). That is the only step:")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "  "+consentURL)
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "Waiting up to %s for the redirect on %s ...\n", authTimeout, redirectURI)
	}

	select {
	case res := <-codeCh:
		if res.state != state {
			fail(asJSON, fmt.Errorf("consent state mismatch - aborted for safety (possible cross-site request); re-run becky-gmail auth"))
		}
		if res.errStr != "" {
			fail(asJSON, fmt.Errorf("consent was denied or failed: %s", res.errStr))
		}
		tr, err := exchangeCode(httpClient(), creds, res.code, redirectURI)
		if err != nil {
			fail(asJSON, err)
		}
		if tr.RefreshToken == "" {
			fail(asJSON, fmt.Errorf("Google returned no refresh token (the account may have a stale prior grant); revoke becky-gmail at https://myaccount.google.com/permissions and run becky-gmail auth again"))
		}
		if err := saveToken(tokenStore{
			RefreshToken: tr.RefreshToken,
			Scope:        firstNonEmpty(tr.Scope, gmailReadonlyScope),
			Obtained:     time.Now().UTC().Format(time.RFC3339),
		}); err != nil {
			fail(asJSON, err)
		}
		yes := true
		if !asJSON {
			fmt.Fprintf(os.Stderr, "Authorized. Refresh token stored at %s (read-only scope). You can close the browser tab.\n", tokenPath())
		}
		beckyio.PrintJSON(Result{OK: true, Authorized: &yes, TokenPath: tokenPath(), Scope: gmailReadonlyScope})
	case <-time.After(authTimeout):
		fail(asJSON, fmt.Errorf("timed out after %s waiting for consent - re-run becky-gmail auth and click Allow", authTimeout))
	}
}

func runAuthStatus(asJSON bool) {
	ts, ok := loadToken()
	if !asJSON {
		if ok {
			fmt.Fprintf(os.Stderr, "Authorized. Refresh token on file at %s (scope: %s).\n", tokenPath(), firstNonEmpty(ts.Scope, gmailReadonlyScope))
		} else {
			fmt.Fprintf(os.Stderr, "Not authorized yet. Run: becky-gmail auth\n")
		}
	}
	beckyio.PrintJSON(Result{OK: true, Authorized: &ok, TokenPath: tokenPath(), Scope: gmailReadonlyScope})
}

// authResult is what the loopback listener hands back from Google's redirect.
type authResult struct {
	code, state, errStr string
}

// startLoopbackListener binds a tiny HTTP server to 127.0.0.1:<port> (port 0 =
// OS-assigned) and returns the actual port, a channel that fires once with the
// OAuth redirect's code/state, and a close function. Loopback-only: it binds
// 127.0.0.1, never 0.0.0.0, so nothing off-box can reach it. This is also the
// function the selftest calls to prove the listener binds with no network.
func startLoopbackListener(port int) (int, <-chan authResult, func(), error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return 0, nil, nil, err
	}
	actualPort := ln.Addr().(*net.TCPAddr).Port
	ch := make(chan authResult, 1)
	srv := &http.Server{}
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		code, errStr := q.Get("code"), q.Get("error")
		if code == "" && errStr == "" {
			// favicon and other stray hits — ignore, don't fire the channel.
			http.Error(w, "waiting for Google redirect", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<!doctype html><meta charset=utf-8><title>becky-gmail</title>"+
			"<body style=\"font:600 22px system-ui;background:#111;color:#eee;padding:3rem\">"+
			"becky-gmail is authorized. You can close this tab.</body>")
		select {
		case ch <- authResult{code: code, state: q.Get("state"), errStr: errStr}:
		default:
		}
	})
	go srv.Serve(ln)
	return actualPort, ch, func() { _ = srv.Close() }, nil
}

// randomState returns a 128-bit anti-CSRF nonce for the OAuth state param.
func randomState() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "becky-gmail-state"
	}
	return hex.EncodeToString(b)
}

func extractAuthFlags(args []string) (status, asJSON bool, port int) {
	if p := strings.TrimSpace(os.Getenv("BECKY_GMAIL_PORT")); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			port = n
		}
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json", "-json":
			asJSON = true
		case "status":
			status = true
		case "--port", "-port":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					port = n
				}
				i++
			}
		}
	}
	return status, asJSON, port
}

// --- search ----------------------------------------------------------------

func runSearch(args []string) {
	query, max, newerThan, asJSON := extractSearchFlags(args)
	if query == "" {
		fmt.Fprintln(os.Stderr, `usage: becky-gmail search "<query>" [--newer-than 1d] [--max 10] [--json]`)
		os.Exit(2)
	}

	ts, ok := loadToken()
	if !ok {
		failNotAuthorized(asJSON)
	}
	creds, err := resolveCreds()
	if err != nil {
		fail(asJSON, err)
	}
	client := httpClient()
	accessToken, err := refreshAccessToken(client, creds, ts.RefreshToken)
	if err != nil {
		fail(asJSON, err)
	}

	q := query
	if newerThan != "" {
		q = strings.TrimSpace(q + " newer_than:" + newerThan)
	}
	ids, err := listMessageIDs(client, accessToken, q, max)
	if err != nil {
		fail(asJSON, err)
	}

	messages := make([]Message, 0, len(ids))
	for _, id := range ids {
		m, err := getMessage(client, accessToken, id)
		if err != nil {
			fail(asJSON, err)
		}
		m.Text = "" // search is a listing view — full text is what `get` is for.
		messages = append(messages, m)
	}

	res := Result{OK: true, Query: q, Count: len(messages), Messages: messages}
	if !asJSON {
		printSearchPlain(res)
	}
	beckyio.PrintJSON(res)
}

func printSearchPlain(res Result) {
	if res.Count == 0 {
		fmt.Fprintf(os.Stderr, "No messages matched: %s\n", res.Query)
		return
	}
	fmt.Fprintf(os.Stderr, "%d message(s) matched: %s\n\n", res.Count, res.Query)
	for i, m := range res.Messages {
		fmt.Fprintf(os.Stderr, "%d. %s\n   from: %s   (%s)\n   id:   %s\n   %s\n", i+1, m.Subject, m.From, m.Date, m.ID, m.Snippet)
		for _, l := range m.Links {
			label := l.Text
			if label != "" {
				label = " (" + label + ")"
			}
			fmt.Fprintf(os.Stderr, "   link: %s%s\n", l.URL, label)
		}
		fmt.Fprintln(os.Stderr)
	}
}

func extractSearchFlags(args []string) (query string, max int, newerThan string, asJSON bool) {
	max = 10
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json", "-json":
			asJSON = true
		case "--max", "-max":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil && n > 0 {
					max = n
				}
				i++
			}
		case "--newer-than", "-newer-than", "--newer_than":
			if i+1 < len(args) {
				newerThan = args[i+1]
				i++
			}
		default:
			rest = append(rest, args[i])
		}
	}
	query = strings.TrimSpace(strings.Join(rest, " "))
	return query, max, newerThan, asJSON
}

// --- get -------------------------------------------------------------------

func runGet(args []string) {
	id, linksOnly, asJSON := extractGetFlags(args)
	if id == "" {
		fmt.Fprintln(os.Stderr, `usage: becky-gmail get <id> [--links-only] [--json]`)
		os.Exit(2)
	}

	ts, ok := loadToken()
	if !ok {
		failNotAuthorized(asJSON)
	}
	creds, err := resolveCreds()
	if err != nil {
		fail(asJSON, err)
	}
	client := httpClient()
	accessToken, err := refreshAccessToken(client, creds, ts.RefreshToken)
	if err != nil {
		fail(asJSON, err)
	}
	m, err := getMessage(client, accessToken, id)
	if err != nil {
		fail(asJSON, err)
	}
	if linksOnly {
		m.Text = ""
	}
	if !asJSON {
		printGetPlain(m, linksOnly)
	}
	beckyio.PrintJSON(Result{OK: true, Message: &m})
}

func printGetPlain(m Message, linksOnly bool) {
	fmt.Fprintf(os.Stderr, "Subject: %s\nFrom:    %s\nDate:    %s\nId:      %s\n\n", m.Subject, m.From, m.Date, m.ID)
	if !linksOnly && m.Text != "" {
		fmt.Fprintln(os.Stderr, m.Text)
		fmt.Fprintln(os.Stderr)
	}
	if len(m.Links) > 0 {
		fmt.Fprintf(os.Stderr, "Links (%d):\n", len(m.Links))
		for _, l := range m.Links {
			label := l.Text
			if label != "" {
				label = " (" + label + ")"
			}
			fmt.Fprintf(os.Stderr, "  %s%s\n", l.URL, label)
		}
	}
}

func extractGetFlags(args []string) (id string, linksOnly, asJSON bool) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json", "-json":
			asJSON = true
		case "--links-only", "-links-only":
			linksOnly = true
		default:
			if id == "" {
				id = args[i]
			}
		}
	}
	return id, linksOnly, asJSON
}

// firstNonEmpty returns the first non-empty string of its args.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
