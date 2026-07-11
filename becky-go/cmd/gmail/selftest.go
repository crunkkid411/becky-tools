// selftest.go — becky-gmail's one-command, OFFLINE, no-network proof of the
// real code path: READ-ONLY scope is asserted, OAuth creds are extracted +
// masked + redacted, the consent URL is built correctly, token/list/message
// responses parse, links (incl. a tracking-redirect wrapper) extract, the
// loopback listener actually binds, and the not-authorized degrade path fires.
// No live Google call, no real token, no reading of anyone's mail. This is
// becky's "provable handoff" gate (STANDARDS-WORKFLOW.md §7).
package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// rawURLB64 encodes s the way Gmail returns message bodies: web-safe base64
// with the padding stripped. Used only to build selftest fixtures.
func rawURLB64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func runSelftest() int {
	type check struct {
		name string
		ok   bool
	}
	var checks []check
	add := func(name string, ok bool) { checks = append(checks, check{name, ok}) }

	// 1. READ-ONLY SCOPE IS HARD (Law 19): the one scope constant is exactly
	// gmail.readonly and carries no send/compose/modify/full-access capability.
	scopeReadonly := gmailReadonlyScope == "https://www.googleapis.com/auth/gmail.readonly"
	scopeNoWrite := !strings.Contains(gmailReadonlyScope, "send") &&
		!strings.Contains(gmailReadonlyScope, "compose") &&
		!strings.Contains(gmailReadonlyScope, "modify") &&
		gmailReadonlyScope != "https://mail.google.com/"
	add("OAuth scope is exactly gmail.readonly", scopeReadonly)
	add("OAuth scope grants NO send/compose/modify/full access", scopeNoWrite)

	// 2. Creds extraction from a manifest, shape-anchored (both label styles).
	manifest := "Client ID: 795327132746-7lq0oc5pjgq7cmgtp12315erbojlf659.apps.googleusercontent.com\n" +
		"Client secret: GOCSPX-selftestFAKEsecret000000000\n" +
		"n8n Google OAuth2:\nClient ID = 795327132746-7lq0oc5pjgq7cmgtp12315erbojlf659.apps.googleusercontent.com\n"
	id, secret := extractOAuthCreds(manifest)
	add("extracts OAuth client id from manifest", id == "795327132746-7lq0oc5pjgq7cmgtp12315erbojlf659.apps.googleusercontent.com")
	add("extracts OAuth client secret from manifest", secret == "GOCSPX-selftestFAKEsecret000000000")

	// 3. Secret masking + redaction (Law 18d).
	add("maskSecret never contains the raw secret", secret != "" && !strings.Contains(maskSecret(secret), secret))
	add("redact scrubs the secret out of an error string", !strings.Contains(redact("body had "+secret+" in it", secret), secret))

	// 4. Consent URL has the right, read-only shape.
	consent := buildConsentURL(id, "http://127.0.0.1:0", "statenonce")
	add("consent URL requests gmail.readonly", strings.Contains(consent, "gmail.readonly"))
	add("consent URL requests NO write scope", !strings.Contains(consent, "gmail.send") && !strings.Contains(consent, "gmail.compose") && !strings.Contains(consent, "gmail.modify") && !strings.Contains(consent, "mail.google.com"))
	add("consent URL is offline + forces consent (=> refresh token)", strings.Contains(consent, "access_type=offline") && strings.Contains(consent, "prompt=consent"))
	add("consent URL carries the client id + loopback redirect + code flow", strings.Contains(consent, "795327132746-") && strings.Contains(consent, "127.0.0.1") && strings.Contains(consent, "response_type=code"))

	// 5. Token response parsing (success + error-with-secret-redacted).
	tr, terr := parseTokenResponse([]byte(`{"access_token":"ya29.aXXX","refresh_token":"1//refreshXYZ","scope":"`+gmailReadonlyScope+`","expires_in":3599}`), secret)
	add("parseTokenResponse decodes tokens", terr == nil && tr.AccessToken == "ya29.aXXX" && tr.RefreshToken == "1//refreshXYZ")
	_, aerr := parseTokenResponse([]byte(`{"error":"invalid_client","error_description":"secret `+secret+` bad"}`), secret)
	add("parseTokenResponse surfaces OAuth errors with the secret redacted", aerr != nil && !strings.Contains(aerr.Error(), secret))

	// 6. Gmail list + message parsing.
	ids, lerr := parseListResponse([]byte(`{"messages":[{"id":"m1"},{"id":"m2"}],"resultSizeEstimate":2}`))
	add("parseListResponse decodes message ids", lerr == nil && len(ids) == 2 && ids[0] == "m1")

	htmlBody := `<html><body>Buy <a href="https://click.pluginboutique.com/t?url=https%3A%2F%2Fwww.pluginboutique.com%2Fproducts%2FTrackspacer&u=x">Trackspacer</a> now</body></html>`
	msgJSON := `{"id":"m1","snippet":"Your Trackspacer receipt","payload":{"headers":[{"name":"From","value":"Plugin Boutique <no-reply@pluginboutique.com>"},{"name":"Subject","value":"Your order"},{"name":"Date","value":"Fri, 10 Jul 2026 09:00:00 -0700"}],"mimeType":"text/html","body":{"data":"` + rawURLB64(htmlBody) + `"}}}`
	msg, merr := parseMessage([]byte(msgJSON))
	add("parseMessage flattens From/Subject/Date/snippet", merr == nil && msg.From != "" && msg.Subject == "Your order" && msg.Date != "" && msg.Snippet == "Your Trackspacer receipt")
	add("parseMessage extracts the link", len(msg.Links) >= 1)
	add("link unwrapping recovers the real Trackspacer URL", len(msg.Links) >= 1 && msg.Links[0].URL == "https://www.pluginboutique.com/products/Trackspacer")
	add("link keeps its anchor text", len(msg.Links) >= 1 && msg.Links[0].Text == "Trackspacer")

	// 7. Plain-text URL extraction + dedup with the same destination.
	links := extractLinks(``, "See https://example.com/a and https://example.com/a again")
	add("plain-text URL extracted and de-duplicated", len(links) == 1 && links[0].URL == "https://example.com/a")

	// 8. base64url body decoding without padding.
	add("decodeBody handles Gmail web-safe base64 (no padding)", string(decodeBody(rawURLB64("hello world"))) == "hello world")

	// 9. The loopback listener actually binds (offline proof, no click).
	port, ch, closeFn, berr := startLoopbackListener(0)
	if berr == nil {
		defer closeFn()
	}
	add("loopback listener binds on 127.0.0.1 and returns a port", berr == nil && port > 0 && ch != nil)

	// 10. not-authorized degrade path: no token file => loadToken ok=false.
	tmp, _ := os.MkdirTemp("", "becky-gmail-selftest-")
	defer os.RemoveAll(tmp)
	os.Setenv("BECKY_GMAIL_TOKEN_PATH", filepath.Join(tmp, "does-not-exist.json"))
	_, hasToken := loadToken()
	os.Unsetenv("BECKY_GMAIL_TOKEN_PATH")
	add("no token file => not authorized (degrade, not crash)", !hasToken)

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
		fmt.Printf("becky-gmail selftest: PASS (%d/%d checks)\n", len(checks), len(checks))
		return 0
	}
	fmt.Printf("becky-gmail selftest: FAIL (%d/%d checks failed)\n", failed, len(checks))
	return 1
}
