package seam

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Handler is a function that the FakeSidecar calls for each incoming command
// or query. It must return a data payload (marshallable to JSON, or nil) and
// an error string (empty on success). The FakeSidecar writes the correlated
// response back to the controller.
//
// Handler is called from the pump goroutine; it must not block indefinitely
// unless ctx (passed from SlowHandler) is cancelled.
type Handler func(typ MessageType, name string, args json.RawMessage) (data interface{}, errMsg string)

// FakeSidecar is an in-process implementation of the seam protocol that
// exercises the Sidecar controller without spawning a real subprocess.
//
// Usage in tests:
//
//	fake := NewFakeSidecar(func(typ MessageType, name string, args json.RawMessage) (interface{}, string) {
//	    if name == "ping" { return map[string]string{"reply":"pong"}, "" }
//	    return nil, "unknown verb: " + name
//	})
//	fake.EmitReady("test-sidecar", "0.1.0")
//	sc := fake.Controller()
//	data, err := sc.Call(ctx, TypeCommand, "ping", nil)
type FakeSidecar struct {
	handler Handler
	sc      *Sidecar
	w       *io.PipeWriter // controller reads from the read end; fake writes here
	seq     atomic.Uint64
	mu      sync.Mutex
	closed  bool
}

// NewFakeSidecar creates an in-process pair: a fake sidecar that calls
// handler for each command/query, and a Sidecar controller wired to it
// via io.Pipe. Callers get the controller via Controller().
//
// If handler is nil, every verb returns an error response.
func NewFakeSidecar(handler Handler) *FakeSidecar {
	if handler == nil {
		handler = func(MessageType, string, json.RawMessage) (interface{}, string) {
			return nil, "no handler registered"
		}
	}

	// stdoutPipe: fake writes JSON lines here; Sidecar.pump reads them.
	stdoutR, stdoutW := io.Pipe()
	// stdinPipe: Sidecar writes JSON commands here; fake.serve reads them.
	stdinR, stdinW := io.Pipe()

	sc := &Sidecar{
		stdin:   stdinW,
		waiters: make(map[string]chan ResponseMsg),
		events:  make(chan Event, eventBufSize),
		done:    make(chan struct{}),
	}

	f := &FakeSidecar{
		handler: handler,
		sc:      sc,
		w:       stdoutW,
	}

	// Pump the fake's stdout into the Sidecar controller.
	go sc.pump(stdoutR)

	// Process commands written by the controller into the fake's stdin.
	go f.serve(stdinR)

	return f
}

// Controller returns the Sidecar that talks to this fake.
// Pass it to code under test exactly as you would a Sidecar from Start().
func (f *FakeSidecar) Controller() *Sidecar {
	return f.sc
}

// EmitReady sends the mandatory "ready" event to the controller.
// Call this after NewFakeSidecar if the code under test waits for the event.
func (f *FakeSidecar) EmitReady(name, version string) {
	f.EmitEvent("ready", ReadyData{Sidecar: name, Version: version})
}

// EmitEvent sends an unsolicited event to the controller.
func (f *FakeSidecar) EmitEvent(eventName string, data interface{}) {
	line, err := marshalEvent(eventName, data)
	if err != nil {
		return
	}
	f.writeLine(line)
}

// Close shuts down the fake by closing its write-end pipe; the controller
// pump sees EOF and shuts itself down.
func (f *FakeSidecar) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		f.w.Close()
	}
}

// serve reads command lines from the controller's stdin and dispatches each
// to the handler, writing the correlated response back.
func (f *FakeSidecar) serve(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		f.handleLine(line)
	}
	// stdin closed: shut down the controller side so pending waiters unblock.
	f.sc.shutdown()
}

// handleLine decodes one incoming command/query line, calls the handler, and
// writes a correlated response. Malformed lines get an ok:false response if
// they carry an id; otherwise they are dropped.
func (f *FakeSidecar) handleLine(line []byte) {
	var msg CommandMsg
	if err := json.Unmarshal(line, &msg); err != nil {
		return // truly unparseable — no id to correlate, drop
	}
	if msg.ID == "" {
		return // no id — drop per spec
	}

	var respData interface{}
	var errMsg string

	switch msg.Type {
	case TypeCommand, TypeQuery:
		respData, errMsg = f.handler(msg.Type, msg.Name, msg.Args)
	default:
		errMsg = fmt.Sprintf("unexpected message type: %s", msg.Type)
	}

	ok := errMsg == ""
	respLine, err := marshalResponse(msg.ID, ok, respData, errMsg)
	if err != nil {
		respLine, _ = marshalResponse(msg.ID, false, nil, "fake marshal error: "+err.Error())
	}
	f.writeLine(respLine)
}

// writeLine writes one JSON object line to the fake's output pipe.
func (f *FakeSidecar) writeLine(line []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	_, _ = fmt.Fprintf(f.w, "%s\n", line)
}

// SlowHandler wraps inner so that it blocks until blockCtx is cancelled before
// responding. Use this to test context-timeout behaviour in Call():
//
//	blockCtx, unblock := context.WithCancel(context.Background())
//	fake := NewFakeSidecar(SlowHandler(blockCtx, normalHandler))
//	callCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
//	defer cancel()
//	_, err := fake.Controller().Call(callCtx, TypeCommand, "slow", nil)
//	// err == context.DeadlineExceeded
//	unblock()
func SlowHandler(blockCtx context.Context, inner Handler) Handler {
	return func(typ MessageType, name string, args json.RawMessage) (interface{}, string) {
		<-blockCtx.Done()
		if inner != nil {
			return inner(typ, name, args)
		}
		return nil, ""
	}
}
