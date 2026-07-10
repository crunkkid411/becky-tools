package main

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

var errFakeNetwork = errors.New("fake network failure")

func TestExtractSearchCreds(t *testing.T) {
	manifest := "\tn8n Google OAuth2:\nClient ID = 795327132746-xxx.apps.googleusercontent.com\n" +
		"Client Secret = GOCSPX-fakefakefakefakefake\n\n" +
		"\t- - -   S E A R C H   - - -\nSearch engine ID: a2a87b7912a7b438b\n" +
		"Search api: AIzaSyFAKE0000000000000000000000000\n" +
		"https://cse.google.com/cse?cx=a2a87b7912a7b438b\n\n" +
		"   - - - ollama - - - \nhuihui_ai/granite3.2-vision-abliterated:latest\n"

	key, cx := extractSearchCreds(manifest)
	if key != "AIzaSyFAKE0000000000000000000000000" {
		t.Errorf("key: got %q", key)
	}
	if cx != "a2a87b7912a7b438b" {
		t.Errorf("cx: got %q", cx)
	}
}

func TestExtractSearchCreds_Missing(t *testing.T) {
	key, cx := extractSearchCreds("nothing relevant here\n")
	if key != "" || cx != "" {
		t.Errorf("expected both empty, got key=%q cx=%q", key, cx)
	}
}

func TestExtractSearchCreds_DoesNotGrabUnrelatedGoogleKey(t *testing.T) {
	// A Gemini key elsewhere in the manifest must never be mistaken for the
	// Search api key - only the labeled "Search api:" line counts.
	manifest := "Gemini key: AIzaSyGEMINI00000000000000000000000\n" +
		"\t- - -   S E A R C H   - - -\nSearch engine ID: a2a87b7912a7b438b\n"
	key, cx := extractSearchCreds(manifest)
	if key != "" {
		t.Errorf("expected no Search api key (only a Gemini key present), got %q", key)
	}
	if cx != "a2a87b7912a7b438b" {
		t.Errorf("cx: got %q", cx)
	}
}

func TestExtractFlags(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantMax   int
		wantJSON  bool
		wantSelf  bool
		wantQuery string
	}{
		{"flag after query", []string{"OpenAI GPT-5 release date", "--json"}, 8, true, false, "OpenAI GPT-5 release date"},
		{"flag before query", []string{"--json", "OpenAI GPT-5 release date"}, 8, true, false, "OpenAI GPT-5 release date"},
		{"no flags", []string{"plain query"}, 8, false, false, "plain query"},
		{"max flag with value", []string{"query text", "--max", "3"}, 3, false, false, "query text"},
		{"max and json combined", []string{"--max", "5", "query", "--json"}, 5, true, false, "query"},
		{"selftest flag", []string{"--selftest"}, 8, false, true, ""},
		{"single-dash flags", []string{"query", "-json", "-max", "2"}, 2, true, false, "query"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			max, asJSON, selftest, query := extractFlags(c.args)
			if max != c.wantMax || asJSON != c.wantJSON || selftest != c.wantSelf || query != c.wantQuery {
				t.Errorf("extractFlags(%v) = (%d,%v,%v,%q), want (%d,%v,%v,%q)",
					c.args, max, asJSON, selftest, query, c.wantMax, c.wantJSON, c.wantSelf, c.wantQuery)
			}
		})
	}
}

func TestMaskKey(t *testing.T) {
	got := maskKey("AIzaSyFAKE0000000000000000000000000")
	if strings.Contains(got, "FAKE00000") {
		t.Errorf("masked key still contains secret material: %q", got)
	}
	if !strings.HasPrefix(got, "AIzaSy") {
		t.Errorf("mask lost its identifying prefix: %q", got)
	}
}

func TestRedact(t *testing.T) {
	key := "AIzaSyFAKE0000000000000000000000000"
	err := `Get "https://www.googleapis.com/customsearch/v1?key=` + key + `&cx=abc": dial tcp: no route to host`
	got := redact(err, key)
	if strings.Contains(got, key) {
		t.Errorf("redact left the raw key in the string: %q", got)
	}
	if !strings.Contains(got, maskKey(key)) {
		t.Errorf("redact should still show the masked form: %q", got)
	}
}

func TestMaxResults(t *testing.T) {
	cases := map[int]int{0: 8, -5: 8, 3: 3, 10: 10, 11: 10, 50: 10}
	for in, want := range cases {
		if got := maxResults(in); got != want {
			t.Errorf("maxResults(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestParseSearchResponse_HappyPath(t *testing.T) {
	body := []byte(`{"items":[
		{"title":"Result One","link":"https://example.com/1","snippet":"first snippet"},
		{"title":"Result Two","link":"https://example.com/2","snippet":"second snippet"}
	]}`)
	items, err := parseSearchResponse(body, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].Title != "Result One" || items[0].URL != "https://example.com/1" || items[0].Snippet != "first snippet" {
		t.Errorf("item 0 mismatched: %+v", items[0])
	}
}

func TestParseSearchResponse_NoResults(t *testing.T) {
	items, err := parseSearchResponse([]byte(`{"items":[]}`), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected zero results, got %d", len(items))
	}
}

func TestParseSearchResponse_APIError_KeyRedacted(t *testing.T) {
	key := "AIzaSyFAKE0000000000000000000000000"
	body := []byte(`{"error":{"message":"API key not valid: ` + key + `"}}`)
	_, err := parseSearchResponse(body, key)
	if err == nil {
		t.Fatal("expected an error for a Google API error response")
	}
	if strings.Contains(err.Error(), key) {
		t.Errorf("error message leaked the raw key: %v", err)
	}
}

func TestParseSearchResponse_DisabledAPIHint(t *testing.T) {
	body := []byte(`{"error":{"message":"This project does not have the access to Custom Search JSON API."}}`)
	_, err := parseSearchResponse(body, "")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "console.cloud.google.com/apis/library/customsearch.googleapis.com") {
		t.Errorf("expected a one-time-fix enablement link in the error, got: %v", err)
	}
}

func TestParseSearchResponse_MalformedJSON(t *testing.T) {
	if _, err := parseSearchResponse([]byte("not json"), ""); err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
}

// fakeGetter is a minimal httpGetter for testing search() without a real
// network call, mirroring cmd/notify's test doubles.
type fakeGetter struct {
	body       string
	statusCode int
	err        error
}

func (f fakeGetter) Get(_ string) (*http.Response, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &http.Response{
		StatusCode: f.statusCode,
		Body:       io.NopCloser(strings.NewReader(f.body)),
	}, nil
}

func TestSearch_NetworkError_KeyRedacted(t *testing.T) {
	key := "AIzaSyFAKE0000000000000000000000000"
	client := fakeGetter{err: errFakeNetwork}
	_, err := search(client, key, "cx123", "test query", 8)
	if err == nil {
		t.Fatal("expected a network error")
	}
	if strings.Contains(err.Error(), key) {
		t.Errorf("network error string leaked the raw key: %v", err)
	}
}

func TestSearch_HappyPath(t *testing.T) {
	client := fakeGetter{statusCode: 200, body: `{"items":[{"title":"T","link":"https://x.test","snippet":"s"}]}`}
	items, err := search(client, "key", "cx", "test query", 8)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 || items[0].URL != "https://x.test" {
		t.Errorf("got %+v", items)
	}
}

func TestResolveCreds_EnvOverride(t *testing.T) {
	t.Setenv("BECKY_GOOGLE_CSE_KEY", "envkey")
	t.Setenv("BECKY_GOOGLE_CSE_CX", "envcx")
	key, cx, err := resolveCreds()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "envkey" || cx != "envcx" {
		t.Errorf("got key=%q cx=%q, want envkey/envcx", key, cx)
	}
}

func TestResolveCreds_NoManifestNoEnv(t *testing.T) {
	t.Setenv("BECKY_GOOGLE_CSE_KEY", "")
	t.Setenv("BECKY_GOOGLE_CSE_CX", "")
	t.Setenv("BECKY_API_MANIFEST_PATH", "X:\\does-not-exist\\nope.txt")
	if _, _, err := resolveCreds(); err == nil {
		t.Fatal("expected an error when neither env vars nor a resolvable manifest exist")
	}
}
