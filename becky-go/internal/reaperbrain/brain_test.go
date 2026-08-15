package reaperbrain

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---- FlattenMessages ----

func TestFlattenMessagesComposesSystemAndTurns(t *testing.T) {
	got := FlattenMessages([]Message{
		{Role: "system", Content: "You control a DAW."},
		{Role: "user", Content: "set tempo to 128"},
		{Role: "assistant", Content: "Done."},
		{Role: "user", Content: "now add a kick"},
	})
	want := "You control a DAW.\n\nConversation so far:\n" +
		"User: set tempo to 128\nAssistant: Done.\nUser: now add a kick" +
		"\n\nReply with the assistant's next message only - no preamble, no commentary about these instructions."
	if got != want {
		t.Fatalf("flatten mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestFlattenMessagesSkipsEmptyAndDefaultsUnknownRoleToUser(t *testing.T) {
	got := FlattenMessages([]Message{
		{Role: "user", Content: "   "},
		{Role: "tool", Content: "weird"},
	})
	if !strings.Contains(got, "User: weird") {
		t.Fatalf("unknown role should read as User, got %q", got)
	}
	if strings.Contains(got, "User:  ") {
		t.Fatalf("empty message should be skipped, got %q", got)
	}
}

// ---- backend selection ----

func TestSelectBackend(t *testing.T) {
	for name, want := range map[string]string{
		"": "claude", "claude": "claude", "zen": "zen", "opencode-zen": "zen",
	} {
		b, err := SelectBackend(name)
		if err != nil {
			t.Fatalf("SelectBackend(%q): %v", name, err)
		}
		if b.Name() != want {
			t.Fatalf("SelectBackend(%q) = %s, want %s", name, b.Name(), want)
		}
	}
	if _, err := SelectBackend("ollama"); err == nil {
		t.Fatal("unknown backend must be rejected (and Ollama is banned anyway)")
	}
}

// ---- the Zen SPEND GUARD (the invariant that must never regress) ----

func TestIsZenFreeGuard(t *testing.T) {
	free := []string{"big-pickle", "deepseek-v4-flash-free", "nemotron-3-ultra-free", " Laguna-S-2.1-FREE "}
	for _, id := range free {
		if !IsZenFree(id) {
			t.Errorf("IsZenFree(%q) = false, want true", id)
		}
	}
	// Zen serves PAID Claude/GPT ids on the same endpoint. Calling them spends
	// Jordan's money on models his Max OAuth already covers. Must be refused.
	paid := []string{"claude-sonnet-5", "claude-fable-5", "gpt-5.5", "grok-4.5", "kimi-k2.7-code", ""}
	for _, id := range paid {
		if IsZenFree(id) {
			t.Errorf("IsZenFree(%q) = true, want false — SPEND GUARD BROKEN", id)
		}
	}
}

func TestIsZenFreeHonoursExtraAllowlist(t *testing.T) {
	t.Setenv(EnvZenFreeExtra, "future-stealth, another-one")
	if !IsZenFree("future-stealth") || !IsZenFree("Another-One") {
		t.Fatal("BECKY_ZEN_FREE_EXTRA entries should be allowed")
	}
	if IsZenFree("gpt-5.5") {
		t.Fatal("non-listed paid id must stay refused")
	}
}

func TestZenBackendRefusesPaidModelBeforeAnyRequest(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()
	b := &ZenBackend{BaseURL: srv.URL, Key: "k", ModelID: "claude-sonnet-5", Client: srv.Client()}
	_, err := b.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err == nil || !strings.Contains(err.Error(), "FREE MODELS ONLY") {
		t.Fatalf("want FREE MODELS ONLY refusal, got %v", err)
	}
	if called {
		t.Fatal("a request LEFT THE MACHINE for a paid model — spend guard failed")
	}
}

func TestZenBackendNeedsKey(t *testing.T) {
	b := &ZenBackend{BaseURL: "http://127.0.0.1:1", ModelID: DefaultZenModel}
	_, err := b.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err == nil || !strings.Contains(err.Error(), "Zen key") {
		t.Fatalf("want missing-key error, got %v", err)
	}
}

// ---- Zen happy path + rotation ----

func TestZenBackendCompletesAndRotatesOnFailure(t *testing.T) {
	var models []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{
				{"id": DefaultZenModel}, {"id": "deepseek-v4-flash-free"}, {"id": "claude-sonnet-5"},
			}})
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		var req zenRequest
		json.NewDecoder(r.Body).Decode(&req)
		models = append(models, req.Model)
		if req.Model == DefaultZenModel { // first choice down → must rotate
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `rate limited`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{
			{"message": Message{Role: "assistant", Content: "Tempo set to 128."}},
		}})
	}))
	defer srv.Close()

	b := &ZenBackend{BaseURL: srv.URL, Key: "test-key", ModelID: DefaultZenModel, Client: srv.Client()}
	got, err := b.Complete(context.Background(), []Message{{Role: "user", Content: "set tempo 128"}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "Tempo set to 128." {
		t.Fatalf("content = %q", got)
	}
	if len(models) != 2 || models[0] != DefaultZenModel || models[1] != "deepseek-v4-flash-free" {
		t.Fatalf("rotation order = %v, want [big-pickle deepseek-v4-flash-free]", models)
	}
}

// ---- Claude backend (injected runner, no real CLI) ----

func TestClaudeBackendRunsCLIAndTrims(t *testing.T) {
	var gotBin string
	var gotArgs []string
	var gotStdin string
	b := &ClaudeBackend{
		Bin: "/fake/claude", ModelID: "sonnet",
		Run: func(ctx context.Context, bin string, args []string, stdin string) (string, error) {
			gotBin, gotArgs, gotStdin = bin, args, stdin
			return "  Kick added on track 3.\n", nil
		},
	}
	got, err := b.Complete(context.Background(), []Message{{Role: "user", Content: "add a kick"}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "Kick added on track 3." {
		t.Fatalf("content = %q", got)
	}
	if gotBin != "/fake/claude" {
		t.Fatalf("bin = %q", gotBin)
	}
	want := []string{"-p", "--model", "sonnet", "--output-format", "text"}
	if strings.Join(gotArgs, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %v, want %v", gotArgs, want)
	}
	if !strings.Contains(gotStdin, "User: add a kick") {
		t.Fatalf("stdin should carry the flattened conversation, got %q", gotStdin)
	}
}

func TestClaudeBackendErrorsAreDescriptive(t *testing.T) {
	b := &ClaudeBackend{resolved: fmt.Errorf("claude CLI not found on PATH")}
	_, err := b.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("want not-found error, got %v", err)
	}
	b2 := &ClaudeBackend{Bin: "x", Run: func(context.Context, string, []string, string) (string, error) {
		return "   ", nil
	}}
	if _, err := b2.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}); err == nil {
		t.Fatal("blank CLI output must be an error, not an empty chat reply")
	}
}

// ---- the HTTP server REAPER Chat talks to ----

type stubBackend struct {
	reply string
	err   error
	last  []Message
}

func (s *stubBackend) Name() string  { return "stub" }
func (s *stubBackend) Model() string { return "stub/echo" }
func (s *stubBackend) Complete(_ context.Context, m []Message) (string, error) {
	s.last = m
	return s.reply, s.err
}

func TestServerChatCompletionRoundTrip(t *testing.T) {
	be := &stubBackend{reply: "Tempo is now 128 BPM."}
	ts := httptest.NewServer(NewServer(be, DefaultHost, DefaultPort).Handler())
	defer ts.Close()

	body := `{"model":"whatever","messages":[{"role":"user","content":"change tempo to 128"}]}`
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var out struct {
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			Message      Message `json:"message"`
			FinishReason string  `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Object != "chat.completion" || out.Model != "stub/echo" {
		t.Fatalf("envelope = %+v", out)
	}
	if len(out.Choices) != 1 || out.Choices[0].Message.Content != "Tempo is now 128 BPM." ||
		out.Choices[0].Message.Role != "assistant" || out.Choices[0].FinishReason != "stop" {
		t.Fatalf("choices = %+v", out.Choices)
	}
	if len(be.last) != 1 || be.last[0].Content != "change tempo to 128" {
		t.Fatalf("backend saw %+v", be.last)
	}
}

func TestServerStreamsSSEWhenAsked(t *testing.T) {
	ts := httptest.NewServer(NewServer(&stubBackend{reply: "ok done"}, DefaultHost, DefaultPort).Handler())
	defer ts.Close()

	body := `{"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
	var dataLines []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(sc.Text(), "data: "))
		}
	}
	if len(dataLines) != 4 || dataLines[3] != "[DONE]" {
		t.Fatalf("want 3 chunks + [DONE], got %d lines: %v", len(dataLines), dataLines)
	}
	var chunk struct {
		Choices []struct {
			Delta map[string]string `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(dataLines[1]), &chunk); err != nil {
		t.Fatalf("chunk decode: %v", err)
	}
	if chunk.Choices[0].Delta["content"] != "ok done" {
		t.Fatalf("content chunk = %v", chunk.Choices[0].Delta)
	}
}

func TestServerHealthModelsAndErrors(t *testing.T) {
	be := &stubBackend{err: fmt.Errorf("no OpenCode Zen key")}
	ts := httptest.NewServer(NewServer(be, DefaultHost, DefaultPort).Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("/health: %v %v", err, resp)
	}
	resp.Body.Close()

	resp, err = http.Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatalf("/v1/models: %v", err)
	}
	var models struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&models)
	resp.Body.Close()
	if len(models.Data) != 1 || models.Data[0].ID != "stub/echo" {
		t.Fatalf("/v1/models = %+v", models)
	}

	// A backend failure must surface as the OpenAI error envelope (so REAPER
	// Chat shows the sentence), never a crash or an empty 200.
	resp, err = http.Post(ts.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&e)
	if !strings.Contains(e.Error.Message, "Zen key") {
		t.Fatalf("error message = %q", e.Error.Message)
	}
}

func TestServerRejectsEmptyMessages(t *testing.T) {
	ts := httptest.NewServer(NewServer(&stubBackend{}, DefaultHost, DefaultPort).Handler())
	defer ts.Close()
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}
