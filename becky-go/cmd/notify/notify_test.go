package main

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

var errFakeNetwork = errors.New("fake network failure")

func TestExtractJSONFlag(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantJSON bool
		wantRest []string
	}{
		{"flag after message", []string{"hello world", "--json"}, true, []string{"hello world"}},
		{"flag before message", []string{"--json", "hello world"}, true, []string{"hello world"}},
		{"no flag", []string{"hello world"}, false, []string{"hello world"}},
		{"single dash", []string{"hello", "-json"}, true, []string{"hello"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotJSON, gotRest := extractJSONFlag(c.args)
			if gotJSON != c.wantJSON {
				t.Errorf("json flag: got %v, want %v", gotJSON, c.wantJSON)
			}
			if strings.Join(gotRest, "|") != strings.Join(c.wantRest, "|") {
				t.Errorf("rest args: got %v, want %v", gotRest, c.wantRest)
			}
		})
	}
}

func TestExtractTelegramToken(t *testing.T) {
	manifest := "	Openclaw Gateway Token:\ntkA6f00IoDZ1EV5eb3ETcjNLEWwG5lz0\n\n" +
		"	Telegram\nname: OpenClaw-Orchestrator_bot\nusername: Orchestrator_bot\n" +
		"token to access http api:\n8637869123:AAxxxxxxxxxxxxxxxxxxxxxxxxxxxxxIAYM\n" +
		"https://t.me/OCOrchestrator_bot\n\n\tOpencode\nOPENCODE_ZEN_API_KEY=sk-abc\n"

	got, err := extractTelegramToken(manifest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "8637869123:AAxxxxxxxxxxxxxxxxxxxxxxxxxxxxxIAYM"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractTelegramToken_NoSection(t *testing.T) {
	if _, err := extractTelegramToken("nothing relevant here\n"); err == nil {
		t.Fatal("expected error for missing Telegram section, got nil")
	}
}

func TestExtractTelegramToken_SectionWithoutToken(t *testing.T) {
	manifest := "\tTelegram\nname: some-bot\n(token not yet issued)\n"
	if _, err := extractTelegramToken(manifest); err == nil {
		t.Fatal("expected error for section with no token shape, got nil")
	}
}

func TestMaskToken(t *testing.T) {
	got := maskToken("8637869123:AAxxxxxxxxxxxxxxxxxxxxxxxxxxxxxIAYM")
	if strings.Contains(got, "AAxxxx") {
		t.Errorf("masked token still contains secret material: %q", got)
	}
	if !strings.HasPrefix(got, "863786") || !strings.HasSuffix(got, "IAYM") {
		t.Errorf("mask lost its identifying edges: %q", got)
	}
}

func TestRedactToken(t *testing.T) {
	token := "8637869123:AAxxxxxxxxxxxxxxxxxxxxxxxxxxxxxIAYM"
	err := `Post "https://api.telegram.org/bot` + token + `/sendMessage": dial tcp: no route to host`
	got := redactToken(err, token)
	if strings.Contains(got, token) {
		t.Fatalf("redactToken left the raw token in the string: %q", got)
	}
	if !strings.Contains(got, "no route to host") {
		t.Errorf("redactToken destroyed the useful part of the error: %q", got)
	}
}

func TestParseUpdatesChatID(t *testing.T) {
	body := []byte(`{"ok":true,"result":[
		{"update_id":1,"message":{"chat":{"id":111}}},
		{"update_id":2,"message":{"chat":{"id":222}}}
	]}`)
	got, err := parseUpdatesChatID(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "222" {
		t.Errorf("expected the LAST message's chat id (222), got %q", got)
	}
}

func TestParseUpdatesChatID_Empty(t *testing.T) {
	got, err := parseUpdatesChatID([]byte(`{"ok":true,"result":[]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty chat id for no updates, got %q", got)
	}
}

func TestParseUpdatesChatID_APIError(t *testing.T) {
	if _, err := parseUpdatesChatID([]byte(`{"ok":false}`)); err == nil {
		t.Fatal("expected error for ok:false response")
	}
}

func TestParseSendResponse_Success(t *testing.T) {
	body := []byte(`{"ok":true,"result":{"message_id":42}}`)
	id, err := parseSendResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 42 {
		t.Errorf("got message_id %d, want 42", id)
	}
}

func TestParseSendResponse_APIError(t *testing.T) {
	body := []byte(`{"ok":false,"description":"Unauthorized"}`)
	_, err := parseSendResponse(body)
	if err == nil {
		t.Fatal("expected error for ok:false response")
	}
	if !strings.Contains(err.Error(), "Unauthorized") {
		t.Errorf("expected Telegram's description in the error, got: %v", err)
	}
}

func TestParseSendResponse_MalformedJSON(t *testing.T) {
	if _, err := parseSendResponse([]byte("not json")); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

// fakeGetter is a test double for httpGetter with no real network I/O.
type fakeGetter struct {
	getBody, postBody string
	getErr, postErr   error
}

func (f *fakeGetter) Get(_ string) (*http.Response, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return &http.Response{Body: io.NopCloser(strings.NewReader(f.getBody))}, nil
}

func (f *fakeGetter) PostForm(_ string, _ url.Values) (*http.Response, error) {
	if f.postErr != nil {
		return nil, f.postErr
	}
	return &http.Response{Body: io.NopCloser(strings.NewReader(f.postBody))}, nil
}

func TestResolveChatID_EnvOverride(t *testing.T) {
	t.Setenv("BECKY_TELEGRAM_CHAT_ID", "999")
	t.Setenv("USERPROFILE", t.TempDir()) // isolate from any real cache file
	got, err := resolveChatID("token", &fakeGetter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "999" {
		t.Errorf("got %q, want 999 (env override should win outright)", got)
	}
}

func TestResolveChatID_DiscoversAndCaches(t *testing.T) {
	t.Setenv("USERPROFILE", t.TempDir())
	fg := &fakeGetter{getBody: `{"ok":true,"result":[{"message":{"chat":{"id":555}}}]}`}
	got, err := resolveChatID("token", fg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "555" {
		t.Errorf("got %q, want 555", got)
	}
	// Second call should hit the cache file, not the network, and still work
	// even if getUpdates would now fail.
	fg2 := &fakeGetter{getErr: errFakeNetwork}
	got2, err := resolveChatID("token", fg2)
	if err != nil {
		t.Fatalf("expected cached chat_id to satisfy the second call, got error: %v", err)
	}
	if got2 != "555" {
		t.Errorf("got %q from cache, want 555", got2)
	}
}

func TestResolveChatID_NoneDiscoverable(t *testing.T) {
	t.Setenv("USERPROFILE", t.TempDir())
	fg := &fakeGetter{getBody: `{"ok":true,"result":[]}`}
	_, err := resolveChatID("token", fg)
	if err == nil {
		t.Fatal("expected an honest error when no chat_id is discoverable, got nil")
	}
	if !strings.Contains(err.Error(), "send") {
		t.Errorf("expected instruction to send the bot a message, got: %v", err)
	}
}
