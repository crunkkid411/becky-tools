package main

import (
	"strings"
	"testing"
)

// READ-ONLY is the whole safety story (Law 19): the only scope this tool can
// ever request is gmail.readonly, and it must grant no write capability.
func TestReadonlyScopeIsHard(t *testing.T) {
	if gmailReadonlyScope != "https://www.googleapis.com/auth/gmail.readonly" {
		t.Fatalf("scope changed to %q — becky-gmail must be read-only", gmailReadonlyScope)
	}
	for _, banned := range []string{"send", "compose", "modify", "insert", "https://mail.google.com/"} {
		if strings.Contains(gmailReadonlyScope, banned) {
			t.Errorf("scope %q contains write capability %q", gmailReadonlyScope, banned)
		}
	}
	consent := buildConsentURL("cid", "http://127.0.0.1:0", "s")
	for _, banned := range []string{"gmail.send", "gmail.compose", "gmail.modify", "mail.google.com"} {
		if strings.Contains(consent, banned) {
			t.Errorf("consent URL leaked a write scope: %q", banned)
		}
	}
}

// Position-independent flag parsing — the exact bug cmd/notify hit (Go's stdlib
// flag stops at the first non-flag arg). Flags must work before OR after the
// query, and a multi-word query must survive reassembly in order.
func TestExtractSearchFlags_PositionIndependent(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantQuery string
		wantMax   int
		wantNewer string
		wantJSON  bool
	}{
		{"flags after query", []string{"pluginboutique", "OR", "trackspacer", "--newer-than", "2d", "--json"}, "pluginboutique OR trackspacer", 10, "2d", true},
		{"flags before query", []string{"--json", "--max", "5", "trackspacer"}, "trackspacer", 5, "", true},
		{"flag interleaved", []string{"pluginboutique", "--newer-than", "1d", "trackspacer"}, "pluginboutique trackspacer", 10, "1d", false},
		{"default max, no flags", []string{"hello"}, "hello", 10, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q, max, newer, asJSON := extractSearchFlags(c.args)
			if q != c.wantQuery {
				t.Errorf("query = %q, want %q", q, c.wantQuery)
			}
			if max != c.wantMax {
				t.Errorf("max = %d, want %d", max, c.wantMax)
			}
			if newer != c.wantNewer {
				t.Errorf("newer = %q, want %q", newer, c.wantNewer)
			}
			if asJSON != c.wantJSON {
				t.Errorf("json = %v, want %v", asJSON, c.wantJSON)
			}
		})
	}
}

func TestExtractGetFlags_PositionIndependent(t *testing.T) {
	id, linksOnly, asJSON := extractGetFlags([]string{"--links-only", "abc123", "--json"})
	if id != "abc123" || !linksOnly || !asJSON {
		t.Fatalf("got id=%q linksOnly=%v json=%v; want abc123/true/true", id, linksOnly, asJSON)
	}
	id2, _, _ := extractGetFlags([]string{"xyz"})
	if id2 != "xyz" {
		t.Errorf("bare id = %q, want xyz", id2)
	}
}

func TestExtractAuthFlags(t *testing.T) {
	status, asJSON, _ := extractAuthFlags([]string{"status", "--json"})
	if !status || !asJSON {
		t.Errorf("status=%v json=%v, want true/true", status, asJSON)
	}
	_, _, p := extractAuthFlags([]string{"--port", "8765"})
	if p != 8765 {
		t.Errorf("port = %d, want 8765", p)
	}
}

// Shape-anchored cred extraction: the real manifest labels the pair two ways;
// only one value of each shape exists, so a shape regex is robust.
func TestExtractOAuthCreds(t *testing.T) {
	manifest := "GEMINI_API_KEY=AIzaSyFakeGeminiKeyThatMustNotMatch\n" +
		"Client ID: 795327132746-7lq0oc5pjgq7cmgtp12315erbojlf659.apps.googleusercontent.com\n" +
		"Client secret: GOCSPX-abcDEF123456ghiJKL\n"
	id, secret := extractOAuthCreds(manifest)
	if id != "795327132746-7lq0oc5pjgq7cmgtp12315erbojlf659.apps.googleusercontent.com" {
		t.Errorf("client id = %q", id)
	}
	if secret != "GOCSPX-abcDEF123456ghiJKL" {
		t.Errorf("client secret = %q", secret)
	}
	if strings.Contains(secret, "AIzaSy") {
		t.Error("picked up the unrelated Gemini API key instead of the OAuth secret")
	}
}

func TestExtractOAuthCreds_Missing(t *testing.T) {
	id, secret := extractOAuthCreds("nothing relevant here\n")
	if id != "" || secret != "" {
		t.Errorf("expected empty creds for a manifest with none, got %q / %q", id, secret)
	}
}

func TestMaskAndRedact(t *testing.T) {
	secret := "GOCSPX-abcDEF123456ghiJKL"
	if strings.Contains(maskSecret(secret), "abcDEF") {
		t.Errorf("mask leaked secret material: %q", maskSecret(secret))
	}
	err := `Post "https://oauth2.googleapis.com/token": body client_secret=` + secret
	got := redact(err, secret)
	if strings.Contains(got, secret) {
		t.Fatalf("redact left the raw secret in: %q", got)
	}
	if !strings.Contains(got, "token") {
		t.Errorf("redact destroyed the useful part of the error: %q", got)
	}
}

// A newsletter click-tracker that wraps the real URL in a ?url= param must be
// unwrapped so a caller gets the destination, not the tracker.
func TestExtractLinks_UnwrapTracking(t *testing.T) {
	html := `<a href="https://track.example.com/c?url=https%3A%2F%2Fwww.pluginboutique.com%2Fproducts%2FTrackspacer&uid=9">Get Trackspacer</a>`
	links := extractLinks(html, "")
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	if links[0].URL != "https://www.pluginboutique.com/products/Trackspacer" {
		t.Errorf("unwrapped URL = %q", links[0].URL)
	}
	if links[0].Text != "Get Trackspacer" {
		t.Errorf("anchor text = %q, want 'Get Trackspacer'", links[0].Text)
	}
}

// A raw (non-tracking) href passes through untouched, and &amp; in the href is
// decoded so query params survive.
func TestExtractLinks_RawHrefAndEntities(t *testing.T) {
	html := `<a href="https://shop.example.com/item?id=5&amp;ref=news">Item</a>`
	links := extractLinks(html, "")
	if len(links) != 1 || links[0].URL != "https://shop.example.com/item?id=5&ref=news" {
		t.Fatalf("got %+v, want the &amp;-decoded raw URL", links)
	}
}

func TestExtractLinks_Dedup(t *testing.T) {
	html := `<a href="https://x.com/a">one</a><a href="https://x.com/a">two</a>`
	links := extractLinks(html, "also visit https://x.com/a")
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1 (deduped)", len(links))
	}
}

// getMessage flattens headers, snippet, decoded body, and links from a real
// Gmail message JSON shape (format=full), walking nested multipart parts.
func TestParseMessage_Multipart(t *testing.T) {
	plain := rawURLB64("Plain body: https://plugin.example/dl")
	html := rawURLB64(`<a href="https://plugin.example/dl">download</a>`)
	body := `{"id":"m9","snippet":"snip","payload":{` +
		`"headers":[{"name":"From","value":"a@b.com"},{"name":"Subject","value":"Sub"},{"name":"Date","value":"D"}],` +
		`"mimeType":"multipart/alternative","parts":[` +
		`{"mimeType":"text/plain","body":{"data":"` + plain + `"}},` +
		`{"mimeType":"text/html","body":{"data":"` + html + `"}}]}}`
	m, err := parseMessage([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.From != "a@b.com" || m.Subject != "Sub" || m.Date != "D" || m.Snippet != "snip" {
		t.Errorf("headers/snippet wrong: %+v", m)
	}
	if !strings.Contains(m.Text, "Plain body") {
		t.Errorf("text body not decoded: %q", m.Text)
	}
	if len(m.Links) != 1 || m.Links[0].URL != "https://plugin.example/dl" {
		t.Errorf("links wrong: %+v", m.Links)
	}
}

func TestParseListResponse_Error(t *testing.T) {
	if _, err := parseListResponse([]byte(`{"error":{"code":403,"message":"insufficient"}}`)); err == nil {
		t.Fatal("expected an error for an error response")
	}
}

func TestParseTokenResponse_ErrorRedacted(t *testing.T) {
	secret := "GOCSPX-verysecretvalue123"
	_, err := parseTokenResponse([]byte(`{"error":"invalid_client","error_description":"bad `+secret+`"}`), secret)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("token error leaked the secret: %v", err)
	}
}

// The not-authorized signal: with the token path pointed at a missing file,
// loadToken reports ok=false rather than erroring — the degrade path.
func TestLoadToken_NotAuthorized(t *testing.T) {
	t.Setenv("BECKY_GMAIL_TOKEN_PATH", t.TempDir()+"/nope.json")
	if _, ok := loadToken(); ok {
		t.Error("expected not-authorized (ok=false) when no token file exists")
	}
}

// Round-trip: a saved token is loadable.
func TestSaveLoadToken_RoundTrip(t *testing.T) {
	t.Setenv("BECKY_GMAIL_TOKEN_PATH", t.TempDir()+"/tok.json")
	if err := saveToken(tokenStore{RefreshToken: "1//abc", Scope: gmailReadonlyScope}); err != nil {
		t.Fatalf("save: %v", err)
	}
	ts, ok := loadToken()
	if !ok || ts.RefreshToken != "1//abc" {
		t.Errorf("round-trip failed: ok=%v ts=%+v", ok, ts)
	}
}

// The loopback listener binds on 127.0.0.1 with an OS-assigned port.
func TestStartLoopbackListener_Binds(t *testing.T) {
	port, ch, closeFn, err := startLoopbackListener(0)
	if err != nil {
		t.Fatalf("listener did not bind: %v", err)
	}
	defer closeFn()
	if port <= 0 || ch == nil {
		t.Errorf("bad listener result: port=%d ch=%v", port, ch)
	}
}
