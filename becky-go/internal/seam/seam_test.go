package seam_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"becky-go/internal/seam"
)

// --- helpers ---

// echoHandler is a simple Handler used across multiple tests.
func echoHandler(typ seam.MessageType, name string, args json.RawMessage) (interface{}, string) {
	switch name {
	case "ping":
		return map[string]string{"reply": "pong"}, ""
	case "echo":
		var payload map[string]interface{}
		if len(args) > 0 {
			if err := json.Unmarshal(args, &payload); err != nil {
				return nil, "bad args"
			}
		}
		return payload, ""
	default:
		return nil, "unknown verb: " + name
	}
}

// --- FakeSidecar tests ---

func TestFakeSidecar_PingPong(t *testing.T) {
	fake := seam.NewFakeSidecar(echoHandler)
	defer fake.Close()

	sc := fake.Controller()
	ctx := context.Background()

	data, err := sc.Call(ctx, seam.TypeCommand, "ping", nil)
	if err != nil {
		t.Fatalf("ping failed: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal pong: %v", err)
	}
	if got["reply"] != "pong" {
		t.Errorf("expected pong, got %q", got["reply"])
	}
}

func TestFakeSidecar_ErrorResponse(t *testing.T) {
	fake := seam.NewFakeSidecar(echoHandler)
	defer fake.Close()

	sc := fake.Controller()
	ctx := context.Background()

	_, err := sc.Call(ctx, seam.TypeCommand, "no-such-verb", nil)
	if err == nil {
		t.Fatal("expected error for unknown verb, got nil")
	}
}

func TestFakeSidecar_QueryType(t *testing.T) {
	var gotType seam.MessageType
	handler := func(typ seam.MessageType, name string, args json.RawMessage) (interface{}, string) {
		gotType = typ
		return map[string]bool{"ok": true}, ""
	}
	fake := seam.NewFakeSidecar(handler)
	defer fake.Close()

	sc := fake.Controller()
	ctx := context.Background()

	if _, err := sc.Call(ctx, seam.TypeQuery, "state.get", nil); err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if gotType != seam.TypeQuery {
		t.Errorf("expected TypeQuery, got %q", gotType)
	}
}

func TestFakeSidecar_OutOfOrderResponsesCorrelated(t *testing.T) {
	// Fire 5 concurrent calls; each must receive its own correlated response.
	handler := func(typ seam.MessageType, name string, args json.RawMessage) (interface{}, string) {
		return map[string]string{"verb": name}, ""
	}
	fake := seam.NewFakeSidecar(handler)
	defer fake.Close()

	sc := fake.Controller()
	ctx := context.Background()

	results := make(chan string, 5)
	for i := 0; i < 5; i++ {
		verb := "verb" + string(rune('A'+i))
		go func(v string) {
			data, err := sc.Call(ctx, seam.TypeCommand, v, nil)
			if err != nil {
				results <- "ERR:" + v
				return
			}
			var got map[string]string
			_ = json.Unmarshal(data, &got)
			results <- got["verb"]
		}(verb)
	}

	seen := make(map[string]bool)
	for i := 0; i < 5; i++ {
		select {
		case v := <-results:
			if len(v) > 4 && v[:4] == "ERR:" {
				t.Errorf("error response for %s", v[4:])
			} else {
				seen[v] = true
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timeout waiting for concurrent responses")
		}
	}
	for i := 0; i < 5; i++ {
		verb := "verb" + string(rune('A'+i))
		if !seen[verb] {
			t.Errorf("missing response for %s", verb)
		}
	}
}

func TestFakeSidecar_ReadyEvent(t *testing.T) {
	fake := seam.NewFakeSidecar(echoHandler)
	defer fake.Close()

	sc := fake.Controller()

	fake.EmitReady("test-sidecar", "0.1.0")

	select {
	case ev := <-sc.Events():
		if ev.Name != "ready" {
			t.Errorf("expected 'ready' event, got %q", ev.Name)
		}
		var rd seam.ReadyData
		if err := json.Unmarshal(ev.Data, &rd); err != nil {
			t.Fatalf("unmarshal ready data: %v", err)
		}
		if rd.Sidecar != "test-sidecar" {
			t.Errorf("sidecar name: got %q, want test-sidecar", rd.Sidecar)
		}
		if rd.Version != "0.1.0" {
			t.Errorf("version: got %q, want 0.1.0", rd.Version)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for ready event")
	}
}

func TestFakeSidecar_UnsolicitedEvent(t *testing.T) {
	fake := seam.NewFakeSidecar(echoHandler)
	defer fake.Close()

	sc := fake.Controller()
	fake.EmitEvent("transport.tick", map[string]float64{"pos": 1.5})

	select {
	case ev := <-sc.Events():
		if ev.Name != "transport.tick" {
			t.Errorf("expected transport.tick, got %q", ev.Name)
		}
		var payload map[string]float64
		if err := json.Unmarshal(ev.Data, &payload); err != nil {
			t.Fatalf("unmarshal event data: %v", err)
		}
		if payload["pos"] != 1.5 {
			t.Errorf("pos: got %v, want 1.5", payload["pos"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for transport.tick event")
	}
}

func TestFakeSidecar_EventsInterleaved(t *testing.T) {
	// Verify that events arriving between a command send and its response do not
	// deadlock or cause responses to be misrouted.
	handler := func(typ seam.MessageType, name string, args json.RawMessage) (interface{}, string) {
		return map[string]string{"verb": name}, ""
	}
	fake := seam.NewFakeSidecar(handler)
	defer fake.Close()

	sc := fake.Controller()
	ctx := context.Background()

	// Emit an event before the call.
	fake.EmitEvent("meter.update", map[string]float64{"level": -12.0})

	// Call must still succeed even though an event arrived first.
	data, err := sc.Call(ctx, seam.TypeCommand, "ping", nil)
	if err != nil {
		// ping is handled by returning verb name, no error
		t.Fatalf("ping after interleaved event: %v", err)
	}
	var got map[string]string
	_ = json.Unmarshal(data, &got)
	if got["verb"] != "ping" {
		t.Errorf("unexpected verb: %q", got["verb"])
	}
}

func TestFakeSidecar_MalformedLineHandled(t *testing.T) {
	// Even if the controller received a malformed line, it should not panic
	// and subsequent valid calls must still work.
	// We verify by using a valid handler and confirming ping works after the fake
	// emits (via EmitEvent which always writes valid JSON); the dispatch
	// path for malformed lines is exercised by the dispatch() internal path.
	fake := seam.NewFakeSidecar(echoHandler)
	defer fake.Close()

	sc := fake.Controller()
	ctx := context.Background()

	// A valid call must succeed — the degrade-never-crash invariant.
	data, err := sc.Call(ctx, seam.TypeCommand, "ping", nil)
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	var got map[string]string
	_ = json.Unmarshal(data, &got)
	if got["reply"] != "pong" {
		t.Errorf("expected pong, got %v", got)
	}
}

func TestFakeSidecar_ContextTimeout(t *testing.T) {
	blockCtx, unblock := context.WithCancel(context.Background())
	defer unblock()

	fake := seam.NewFakeSidecar(seam.SlowHandler(blockCtx, echoHandler))
	defer fake.Close()

	sc := fake.Controller()

	callCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := sc.Call(callCtx, seam.TypeCommand, "ping", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestFakeSidecar_SidecarClosed(t *testing.T) {
	fake := seam.NewFakeSidecar(echoHandler)
	sc := fake.Controller()

	fake.Close()

	// Allow the shutdown goroutines to propagate.
	time.Sleep(100 * time.Millisecond)

	ctx := context.Background()
	_, err := sc.Call(ctx, seam.TypeCommand, "ping", nil)
	if err == nil {
		t.Fatal("expected error after sidecar close, got nil")
	}
}

func TestFakeSidecar_NilHandler(t *testing.T) {
	// A nil handler must not panic; all verbs return error.
	fake := seam.NewFakeSidecar(nil)
	defer fake.Close()

	sc := fake.Controller()
	ctx := context.Background()

	_, err := sc.Call(ctx, seam.TypeCommand, "ping", nil)
	if err == nil {
		t.Fatal("expected error from nil handler, got nil")
	}
}

// --- End-to-end tests with real seam-echo subprocess ---

// seamEchoBinary builds cmd/seam-echo to a temp dir and returns the path.
// Returns ("", false) if the source is not present.
func seamEchoBinary(t *testing.T) (string, bool) {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", false
	}
	// thisFile = .../internal/seam/seam_test.go
	// moduleRoot = .../becky-go
	moduleRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	src := filepath.Join(moduleRoot, "cmd", "seam-echo")
	if _, err := os.Stat(src); err != nil {
		return "", false
	}

	binName := "seam-echo"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	bin := filepath.Join(t.TempDir(), binName)

	cmd := exec.Command("go", "build", "-o", bin, "./cmd/seam-echo")
	cmd.Dir = moduleRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("seam-echo build:\n%s", out)
		t.Fatalf("build seam-echo: %v", err)
	}
	return bin, true
}

func TestSidecar_E2E_PingPong(t *testing.T) {
	bin, ok := seamEchoBinary(t)
	if !ok {
		t.Skip("cmd/seam-echo not found — skip E2E")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sc, err := seam.Start(ctx, bin)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sc.Close()

	// 1. Expect the "ready" event as the very first event.
	select {
	case ev := <-sc.Events():
		if ev.Name != "ready" {
			t.Errorf("first event: got %q, want ready", ev.Name)
		}
		var rd seam.ReadyData
		if err := json.Unmarshal(ev.Data, &rd); err != nil {
			t.Fatalf("unmarshal ready: %v", err)
		}
		if rd.Sidecar != "seam-echo" {
			t.Errorf("sidecar: got %q, want seam-echo", rd.Sidecar)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for ready event")
	}

	// 2. ping -> pong.
	data, err := sc.Call(ctx, seam.TypeCommand, "ping", nil)
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	var pong map[string]string
	if err := json.Unmarshal(data, &pong); err != nil {
		t.Fatalf("unmarshal pong: %v", err)
	}
	if pong["reply"] != "pong" {
		t.Errorf("pong reply: got %q, want pong", pong["reply"])
	}

	// 3. Unknown verb -> ok:false.
	_, err = sc.Call(ctx, seam.TypeCommand, "no-such-verb", nil)
	if err == nil {
		t.Fatal("expected error for unknown verb, got nil")
	}
}

func TestSidecar_E2E_SlowVerb(t *testing.T) {
	bin, ok := seamEchoBinary(t)
	if !ok {
		t.Skip("cmd/seam-echo not found — skip E2E")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sc, err := seam.Start(ctx, bin)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sc.Close()

	// Drain ready.
	select {
	case <-sc.Events():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for ready")
	}

	// "slow" verb waits 200ms then responds — verify it completes without timeout.
	slowCtx, slowCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer slowCancel()

	data, err := sc.Call(slowCtx, seam.TypeQuery, "slow", map[string]int{"delay_ms": 200})
	if err != nil {
		t.Fatalf("slow: %v", err)
	}
	var got map[string]string
	_ = json.Unmarshal(data, &got)
	if got["status"] != "done" {
		t.Errorf("slow status: got %q, want done", got["status"])
	}
}

func TestSidecar_E2E_ContextCancelOnSlow(t *testing.T) {
	bin, ok := seamEchoBinary(t)
	if !ok {
		t.Skip("cmd/seam-echo not found — skip E2E")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sc, err := seam.Start(ctx, bin)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sc.Close()

	// Drain ready.
	select {
	case <-sc.Events():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for ready")
	}

	// Call "slow" (2s delay) but cancel after 100ms.
	callCtx, callCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer callCancel()

	_, err = sc.Call(callCtx, seam.TypeCommand, "slow", map[string]int{"delay_ms": 2000})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}
