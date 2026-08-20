package avlm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
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

// The timestamps no longer live in the prompt as a list — each one is now a
// separate text part sitting immediately before its own frame's image tokens,
// which is the encoding TimeLens (arXiv 2512.14698, Table 2) measured as best.
// So the prompt must ANNOUNCE that and must NOT re-list them.
func TestBuildPromptDoesNotListTimestamps(t *testing.T) {
	opts := Options{Prompt: "QUESTION-MARKER", FPS: 1, WindowStart: 0}
	p := buildPrompt(opts, []string{"f1.jpg", "f2.jpg"}, "a.wav", 30)
	if strings.Contains(p, "[0.0s]") || strings.Contains(p, "frame 1 =") {
		t.Errorf("prompt still lists timestamps up front; they belong interleaved: %q", p)
	}
	if !strings.Contains(p, "preceded by its clip-absolute timestamp") {
		t.Errorf("prompt must tell the model where the timestamps are: %q", p)
	}
	if !strings.Contains(p, "the clip's audio") {
		t.Errorf("prompt should mention audio when present: %q", p)
	}
	if !strings.Contains(p, "2 video frame(s)") {
		t.Errorf("prompt should state the frame count: %q", p)
	}
}

// frameStamp is what actually carries the time now, so its VALUES are asserted.
func TestFrameStampIsTheInterleavedTimestamp(t *testing.T) {
	opts := Options{FPS: 2, WindowStart: 10}
	if got := frameStamp(opts, 0); got != "[10.0s]" {
		t.Errorf("frameStamp(0) = %q, want [10.0s]", got)
	}
	if got := frameStamp(opts, 3); got != "[11.5s]" {
		t.Errorf("frameStamp(3) = %q, want [11.5s] (10 + 3/2)", got)
	}
	// FPS 0 means "unset" everywhere else in this package; it must not divide by zero.
	if got := frameStamp(Options{FPS: 0, WindowStart: 4}, 2); got != "[6.0s]" {
		t.Errorf("frameStamp with FPS 0 = %q, want [6.0s] (fall back to 1 fps)", got)
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

// frameBudget is what stops the shipped defaults from being a guaranteed
// timeout: a 30s window at 1 fps is 30 frames, one frame costs ~14s on this
// card, and the tool's own timeout is 240s. The request could never finish, and
// what came back was zero observations with a note blaming the model.
func TestFrameBudgetFitsTheDeadline(t *testing.T) {
	// No deadline: ask for everything, report no cap.
	if n, capped := frameBudget(context.Background(), MaxFrames); n != MaxFrames || capped {
		t.Errorf("no deadline: got (%d, %v), want (%d, false)", n, capped, MaxFrames)
	}

	// becky-validate's own default: 240s.
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	n, capped := frameBudget(ctx, MaxFrames)
	if !capped {
		t.Fatalf("240s should not afford %d frames at ~%.0fs each", MaxFrames, secPerFrameEstimate)
	}
	// (240s - 25s slack) / 14s = 15
	if n != 15 {
		t.Errorf("240s deadline affords %d frame(s), want 15", n)
	}
	if float64(n)*secPerFrameEstimate+generationSlack.Seconds() > 240 {
		t.Errorf("budget of %d frames does not actually fit 240s", n)
	}

	// A generous deadline must not cap at all.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3600*time.Second)
	defer cancel2()
	if n, capped := frameBudget(ctx2, MaxFrames); n != MaxFrames || capped {
		t.Errorf("an hour: got (%d, %v), want (%d, false)", n, capped, MaxFrames)
	}

	// Already past the deadline: ask for one rather than zero. A frame the
	// caller can still show beats an empty result.
	ctx3, cancel3 := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel3()
	if n, capped := frameBudget(ctx3, MaxFrames); n != 1 || !capped {
		t.Errorf("expired deadline: got (%d, %v), want (1, true)", n, capped)
	}
}
