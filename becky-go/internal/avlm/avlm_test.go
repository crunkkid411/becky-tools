package avlm

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestReadyMissingArtifacts verifies Ready() returns a *DegradeError naming the
// first missing required artifact, and that a configured ServerURL removes the
// need for a local server binary.
func TestReadyMissingArtifacts(t *testing.T) {
	r := New("", "", "", "", "", "", nil)
	err := r.Ready()
	if err == nil || !IsDegrade(err) {
		t.Fatalf("Ready with nothing set should degrade, got %v", err)
	}
	if !strings.Contains(err.Error(), "gemma model GGUF") {
		t.Errorf("expected model-missing reason, got %q", err.Error())
	}
}

// TestReadyServerURLSkipsBinary confirms that when a ServerURL is set we do not
// require the llama-server binary (only the model/mmproj/ffmpeg files). All of
// those are intentionally absent here, so it still degrades — but on a file
// reason, never on the missing binary.
func TestReadyServerURLSkipsBinary(t *testing.T) {
	r := New("nope-model", "nope-mmproj", "", "http://127.0.0.1:9", "nope-ffmpeg", "", nil)
	err := r.Ready()
	if err == nil {
		t.Fatal("expected degrade for missing model file")
	}
	if strings.Contains(err.Error(), "llama-server") {
		t.Errorf("ServerURL set: should not complain about llama-server, got %q", err.Error())
	}
}

// TestNewDefaults checks the constructor wires NGL=99 and a non-nil logger.
func TestNewDefaults(t *testing.T) {
	r := New("m", "p", "s", "", "ff", "fp", nil)
	if r.NGL != 99 {
		t.Errorf("NGL = %d, want 99", r.NGL)
	}
	if r.Logf == nil {
		t.Error("Logf must be non-nil even when nil is passed")
	}
}

// TestChatRequestDisablesThinking is the load-bearing test: the request body
// MUST carry chat_template_kwargs.enable_thinking=false, or Gemma-4 emits its
// answer into a stripped reasoning channel and content comes back empty.
func TestChatRequestDisablesThinking(t *testing.T) {
	req := chatRequest{
		Model:             "gemma4",
		Temperature:       0.2,
		MaxTokens:         256,
		Messages:          []chatMessage{{Role: "user", Content: []contentPart{{Type: "text", Text: "hi"}}}},
		ChatTemplateKwarg: map[string]bool{"enable_thinking": false},
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"chat_template_kwargs":{"enable_thinking":false}`) {
		t.Errorf("request missing enable_thinking=false: %s", s)
	}
}

// TestBuildPromptFramesFirstWithTimestamps verifies frame timestamps are baked
// into the prompt (the no-native-timestamp workaround) and the question follows.
func TestBuildPromptFramesFirstWithTimestamps(t *testing.T) {
	opts := Options{Prompt: "QUESTION-MARKER", FPS: 1, WindowStart: 0}
	p := buildPrompt(opts, []string{"f1.jpg", "f2.jpg"}, "a.wav", 30)
	if !strings.Contains(p, "[0.0s]") || !strings.Contains(p, "[1.0s]") {
		t.Errorf("prompt missing baked frame timestamps: %q", p)
	}
	if !strings.Contains(p, "the clip's audio") {
		t.Errorf("prompt should mention audio when present: %q", p)
	}
	if i := strings.Index(p, "[0.0s]"); i < 0 || i > strings.Index(p, "QUESTION-MARKER") {
		t.Error("frame captions must come before the question")
	}
}

// TestClampWindow caps requested windows at the model's 60 s video limit.
func TestClampWindow(t *testing.T) {
	if got := clampWindow(0); got != 30 {
		t.Errorf("clampWindow(0) = %v, want 30", got)
	}
	if got := clampWindow(120); got != MaxVideoSeconds {
		t.Errorf("clampWindow(120) = %v, want %v", got, MaxVideoSeconds)
	}
	if got := clampWindow(15); got != 15 {
		t.Errorf("clampWindow(15) = %v, want 15", got)
	}
}

// TestDefaultsFillsUnset checks defaults() fills sane values.
func TestDefaultsFillsUnset(t *testing.T) {
	o := Options{}
	defaults(&o)
	if o.FPS != 1.0 || o.WindowSec != 30 || o.MaxTokens != 512 || o.Seed != 42 {
		t.Errorf("defaults not applied: %+v", o)
	}
}
