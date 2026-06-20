package videopreview

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"becky-go/internal/seam"
)

// recordingFake wraps a seam.FakeSidecar and records every (type,name,args) it
// receives, so tests can assert the client sent the right verbs/args. The handler
// is supplied per-test to shape the responses.
type recordingFake struct {
	fake *seam.FakeSidecar

	mu    sync.Mutex
	calls []recordedCall
}

type recordedCall struct {
	Type seam.MessageType
	Name string
	Args json.RawMessage
}

func newRecordingFake(t *testing.T, handler seam.Handler) (*recordingFake, *Client) {
	t.Helper()
	rf := &recordingFake{}
	wrapped := func(typ seam.MessageType, name string, args json.RawMessage) (interface{}, string) {
		rf.mu.Lock()
		rf.calls = append(rf.calls, recordedCall{Type: typ, Name: name, Args: append(json.RawMessage(nil), args...)})
		rf.mu.Unlock()
		return handler(typ, name, args)
	}
	rf.fake = seam.NewFakeSidecar(wrapped)
	rf.fake.EmitReady("video-preview-fake", "0.1.0")
	t.Cleanup(rf.fake.Close)
	// tmpDir is fixed so framePath is assertable.
	return rf, NewWithController(rf.fake.Controller(), `C:\nle-tmp`)
}

func (rf *recordingFake) last() recordedCall {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	if len(rf.calls) == 0 {
		return recordedCall{}
	}
	return rf.calls[len(rf.calls)-1]
}

func (rf *recordingFake) byName(name string) (recordedCall, bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	for _, c := range rf.calls {
		if c.Name == name {
			return c, true
		}
	}
	return recordedCall{}, false
}

// argField pulls one field out of a recorded call's args JSON.
func argField(t *testing.T, raw json.RawMessage, field string) interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("args not an object: %v (raw=%s)", err, raw)
	}
	return m[field]
}

// --- Open ------------------------------------------------------------------------

func TestOpen_SendsVideoOpenAndParsesInfo(t *testing.T) {
	ctx := context.Background()
	rf, c := newRecordingFake(t, func(_ seam.MessageType, name string, _ json.RawMessage) (interface{}, string) {
		if name == "video.open" {
			return map[string]interface{}{
				"width": 1920, "height": 1080, "fps": 29.97,
				"durationSec": 12.5, "frames": 374,
			}, ""
		}
		return nil, "unknown verb: " + name
	})

	info, err := c.Open(ctx, `E:\TakingBack2007\clip.mp4`)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Verb + type + path arg.
	call := rf.last()
	if call.Type != seam.TypeCommand {
		t.Errorf("Open type = %q, want command", call.Type)
	}
	if call.Name != "video.open" {
		t.Errorf("Open verb = %q, want video.open", call.Name)
	}
	if got := argField(t, call.Args, "path"); got != `E:\TakingBack2007\clip.mp4` {
		t.Errorf("Open path arg = %v, want the source path", got)
	}

	// Parsed Info.
	want := Info{Width: 1920, Height: 1080, FPS: 29.97, DurationSec: 12.5, Frames: 374}
	if info != want {
		t.Errorf("Info = %+v, want %+v", info, want)
	}

	// Cached: Info()/IsOpen()/Path() reflect the open.
	if !c.IsOpen() {
		t.Error("IsOpen() = false after Open")
	}
	if got, _ := c.Info(); got != want {
		t.Errorf("cached Info() = %+v, want %+v", got, want)
	}
	if c.Path() != `E:\TakingBack2007\clip.mp4` {
		t.Errorf("Path() = %q after Open", c.Path())
	}
}

func TestOpen_ErrorClearsState(t *testing.T) {
	ctx := context.Background()
	_, c := newRecordingFake(t, func(_ seam.MessageType, name string, _ json.RawMessage) (interface{}, string) {
		return nil, "no such file"
	})

	if _, err := c.Open(ctx, `X:\missing.mp4`); err == nil {
		t.Fatal("Open of a failing sidecar should error")
	}
	if c.IsOpen() {
		t.Error("IsOpen() should be false after a failed Open")
	}
	if _, err := c.Info(); !errors.Is(err, ErrNotOpen) {
		t.Errorf("Info() after failed Open = %v, want ErrNotOpen", err)
	}
}

// --- Frame / Overlay -------------------------------------------------------------

func TestFrame_SendsQueryWithOutPathAndReturnsIt(t *testing.T) {
	ctx := context.Background()
	rf, c := newRecordingFake(t, func(_ seam.MessageType, name string, args json.RawMessage) (interface{}, string) {
		switch name {
		case "video.open":
			return map[string]interface{}{"width": 640, "height": 480, "fps": 30.0, "durationSec": 5.0, "frames": 150}, ""
		case "video.frame":
			// Echo the out path the client supplied, plus geometry.
			var a map[string]interface{}
			_ = json.Unmarshal(args, &a)
			return map[string]interface{}{"out": a["out"], "width": 640, "height": 480, "timeSec": a["timeSec"], "backend": "Vulkan"}, ""
		}
		return nil, "unknown verb: " + name
	})

	if _, err := c.Open(ctx, `clip.mp4`); err != nil {
		t.Fatalf("Open: %v", err)
	}

	png, err := c.Frame(ctx, 1.5)
	if err != nil {
		t.Fatalf("Frame: %v", err)
	}

	call, ok := rf.byName("video.frame")
	if !ok {
		t.Fatal("client did not send video.frame")
	}
	if call.Type != seam.TypeQuery {
		t.Errorf("Frame type = %q, want query", call.Type)
	}
	if got := argField(t, call.Args, "path"); got != "clip.mp4" {
		t.Errorf("Frame path arg = %v, want clip.mp4", got)
	}
	if got := argField(t, call.Args, "timeSec"); got != 1.5 {
		t.Errorf("Frame timeSec arg = %v, want 1.5", got)
	}
	// The out path is chosen by the client under tmpDir; the returned PNG matches it.
	wantOut := `C:\nle-tmp\becky-nle-frame-1500.png`
	if got := argField(t, call.Args, "out"); got != wantOut {
		t.Errorf("Frame out arg = %v, want %q", got, wantOut)
	}
	if png != wantOut {
		t.Errorf("Frame returned %q, want %q", png, wantOut)
	}
}

func TestOverlay_SendsTextAndOverlayVerb(t *testing.T) {
	ctx := context.Background()
	rf, c := newRecordingFake(t, func(_ seam.MessageType, name string, args json.RawMessage) (interface{}, string) {
		switch name {
		case "video.open":
			return map[string]interface{}{"width": 640, "height": 480, "fps": 30.0, "durationSec": 5.0, "frames": 150}, ""
		case "video.overlay":
			var a map[string]interface{}
			_ = json.Unmarshal(args, &a)
			return map[string]interface{}{"out": a["out"], "overlay": true, "timecode": "00:00:02:00", "text": a["text"]}, ""
		}
		return nil, "unknown verb: " + name
	})

	if _, err := c.Open(ctx, `clip.mp4`); err != nil {
		t.Fatalf("Open: %v", err)
	}

	png, err := c.Overlay(ctx, 2.0, "I want Penguin")
	if err != nil {
		t.Fatalf("Overlay: %v", err)
	}
	call, ok := rf.byName("video.overlay")
	if !ok {
		t.Fatal("client did not send video.overlay")
	}
	if call.Type != seam.TypeQuery {
		t.Errorf("Overlay type = %q, want query", call.Type)
	}
	if got := argField(t, call.Args, "text"); got != "I want Penguin" {
		t.Errorf("Overlay text arg = %v, want the caption", got)
	}
	wantOut := `C:\nle-tmp\becky-nle-overlay-2000.png`
	if png != wantOut {
		t.Errorf("Overlay returned %q, want %q", png, wantOut)
	}
}

func TestFrame_BeforeOpen_IsErrNotOpen(t *testing.T) {
	ctx := context.Background()
	_, c := newRecordingFake(t, func(_ seam.MessageType, name string, _ json.RawMessage) (interface{}, string) {
		return nil, "should not be called"
	})
	if _, err := c.Frame(ctx, 0); !errors.Is(err, ErrNotOpen) {
		t.Errorf("Frame before Open = %v, want ErrNotOpen", err)
	}
	if _, err := c.Overlay(ctx, 0, "x"); !errors.Is(err, ErrNotOpen) {
		t.Errorf("Overlay before Open = %v, want ErrNotOpen", err)
	}
}

// --- response shape handling -----------------------------------------------------

func TestFrame_NoOutInResponse_FallsBackToRequested(t *testing.T) {
	ctx := context.Background()
	_, c := newRecordingFake(t, func(_ seam.MessageType, name string, _ json.RawMessage) (interface{}, string) {
		switch name {
		case "video.open":
			return map[string]interface{}{"width": 1, "height": 1, "fps": 1.0, "durationSec": 1.0, "frames": 1}, ""
		case "video.frame":
			// Response omits "out" — client falls back to the path it requested.
			return map[string]interface{}{"width": 1, "height": 1}, ""
		}
		return nil, "unknown verb"
	})
	if _, err := c.Open(ctx, "clip.mp4"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	png, err := c.Frame(ctx, 0.25)
	if err != nil {
		t.Fatalf("Frame: %v", err)
	}
	if png != `C:\nle-tmp\becky-nle-frame-250.png` {
		t.Errorf("fallback out = %q", png)
	}
}

func TestPing_ParsesVersion(t *testing.T) {
	ctx := context.Background()
	_, c := newRecordingFake(t, func(_ seam.MessageType, name string, _ json.RawMessage) (interface{}, string) {
		if name == "ping" {
			return map[string]interface{}{"pong": true, "version": "0.1.0"}, ""
		}
		return nil, "unknown verb"
	})
	v, err := c.Ping(ctx)
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if v != "0.1.0" {
		t.Errorf("Ping version = %q, want 0.1.0", v)
	}
}

// --- error propagation -----------------------------------------------------------

func TestSidecarErrorPropagates(t *testing.T) {
	ctx := context.Background()
	_, c := newRecordingFake(t, func(_ seam.MessageType, name string, _ json.RawMessage) (interface{}, string) {
		switch name {
		case "video.open":
			return map[string]interface{}{"width": 1, "height": 1, "fps": 1.0, "durationSec": 1.0, "frames": 1}, ""
		case "video.frame":
			return nil, "decode failed: corrupt frame"
		}
		return nil, "unknown verb"
	})
	if _, err := c.Open(ctx, "clip.mp4"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, err := c.Frame(ctx, 1.0)
	if err == nil {
		t.Fatal("Frame should surface the sidecar error")
	}
	if !strings.Contains(err.Error(), "corrupt frame") {
		t.Errorf("Frame error = %v, want it to contain the sidecar message", err)
	}
}

// --- nil-controller degrade (no crash) -------------------------------------------

func TestNilController_Degrades(t *testing.T) {
	ctx := context.Background()
	c := NewWithController(nil, "")
	if _, err := c.Open(ctx, "clip.mp4"); !errors.Is(err, ErrSidecarMissing) {
		t.Errorf("Open with nil controller = %v, want ErrSidecarMissing", err)
	}
	// No panic, no crash — exactly the degrade-never-crash contract.
}

// --- framePath determinism -------------------------------------------------------

func TestFramePath_DeterministicPerKindAndTime(t *testing.T) {
	c := NewWithController(nil, `C:\t`)
	cases := []struct {
		kind string
		time float64
		want string
	}{
		{"frame", 0, `C:\t\becky-nle-frame-0.png`},
		{"frame", 1.5, `C:\t\becky-nle-frame-1500.png`},
		{"overlay", 2.0, `C:\t\becky-nle-overlay-2000.png`},
		{"frame", 0.2505, `C:\t\becky-nle-frame-251.png`}, // rounds to nearest ms
	}
	for _, tc := range cases {
		got := c.framePath(tc.kind, tc.time)
		if got != tc.want {
			t.Errorf("framePath(%q,%g) = %q, want %q", tc.kind, tc.time, got, tc.want)
		}
		// Same input -> same output (determinism).
		if again := c.framePath(tc.kind, tc.time); again != got {
			t.Errorf("framePath not deterministic: %q vs %q", again, got)
		}
	}
}

// --- WindowArgs degrade ----------------------------------------------------------

func TestWindowArgs_MissingSidecar_Degrades(t *testing.T) {
	// In the test environment the sidecar binary isn't present (no env override,
	// not next to the test binary), so WindowArgs degrades with ErrSidecarMissing.
	t.Setenv(envSidecar, "") // ensure no override leaks in from the host
	_, _, err := WindowArgs(`clip.mp4`)
	if err != nil && !errors.Is(err, ErrSidecarMissing) {
		t.Errorf("WindowArgs error = %v, want ErrSidecarMissing or success", err)
	}
}
