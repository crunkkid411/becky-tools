package seam

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
)

// Sidecar is a controller for one native sidecar subprocess.
//
// It spawns the process, pumps its stdout (dispatching responses to
// per-id waiters and events to the event channel), and writes commands
// to its stdin. All methods are safe for concurrent use.
//
// Usage:
//
//	sc, err := Start(ctx, "becky-audio-host")
//	if err != nil { ... }
//	defer sc.Close()
//
//	data, err := sc.Call(ctx, TypeCommand, "ping", nil)
type Sidecar struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	mu       sync.Mutex // guards stdin writes and waiters map
	waiters  map[string]chan ResponseMsg
	events   chan Event
	done     chan struct{}
	doneOnce sync.Once
	seq      atomic.Uint64
}

const eventBufSize = 64

// Start launches the sidecar binary at exePath with the given args and
// begins pumping its stdout. The returned Sidecar is ready to use
// immediately; the ready event (emitted by well-behaved sidecars on
// startup) is forwarded to Events() like any other event.
//
// The context controls the subprocess lifetime: cancelling it sends
// SIGKILL (or the OS equivalent) to the child process and closes the Sidecar.
func Start(ctx context.Context, exePath string, args ...string) (*Sidecar, error) {
	cmd := exec.CommandContext(ctx, exePath, args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("seam: stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("seam: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("seam: start %s: %w", exePath, err)
	}

	sc := &Sidecar{
		cmd:     cmd,
		stdin:   stdin,
		waiters: make(map[string]chan ResponseMsg),
		events:  make(chan Event, eventBufSize),
		done:    make(chan struct{}),
	}

	go sc.pump(stdout)
	go func() {
		_ = cmd.Wait()
		sc.shutdown()
	}()

	return sc, nil
}

// Call sends a verb of the given type (TypeCommand or TypeQuery) with the
// given args to the sidecar and blocks until the correlated response arrives
// or the context is cancelled.
//
// args may be nil for verbs with no payload. Returns the raw JSON data field
// of the response on success, or a non-nil error if the sidecar replied with
// ok:false or the context was cancelled / the sidecar closed.
func (sc *Sidecar) Call(ctx context.Context, typ MessageType, name string, args interface{}) (json.RawMessage, error) {
	id := sc.nextID()
	line, err := marshalCommand(typ, id, name, args)
	if err != nil {
		return nil, err
	}

	waiter := make(chan ResponseMsg, 1)

	sc.mu.Lock()
	// Check closed under the lock to avoid a race with shutdown.
	select {
	case <-sc.done:
		sc.mu.Unlock()
		return nil, fmt.Errorf("seam: sidecar closed")
	default:
	}
	sc.waiters[id] = waiter
	sc.mu.Unlock()

	// Write to stdin OUTSIDE the lock so that the pump goroutine (which calls
	// dispatch, which acquires the lock) can run concurrently. Holding the
	// lock during a pipe write causes a deadlock when multiple goroutines are
	// in Call simultaneously: the pump blocks on the lock, the pipe's read
	// buffer fills, and the write here blocks too.
	_, werr := fmt.Fprintf(sc.stdin, "%s\n", line)

	if werr != nil {
		sc.mu.Lock()
		delete(sc.waiters, id)
		sc.mu.Unlock()
		return nil, fmt.Errorf("seam: write to sidecar: %w", werr)
	}

	select {
	case resp := <-waiter:
		if !resp.OK {
			return nil, fmt.Errorf("seam: %s", resp.Error)
		}
		return resp.Data, nil
	case <-ctx.Done():
		sc.mu.Lock()
		delete(sc.waiters, id)
		sc.mu.Unlock()
		return nil, ctx.Err()
	case <-sc.done:
		return nil, fmt.Errorf("seam: sidecar closed")
	}
}

// Events returns the channel on which unsolicited events (and the ready event)
// are delivered. The channel is buffered (64 entries); if the caller is slow
// the oldest events are dropped to keep the pump goroutine from blocking.
//
// The channel is closed when the sidecar process exits.
func (sc *Sidecar) Events() <-chan Event {
	return sc.events
}

// Close shuts the sidecar down by closing its stdin (signalling it to exit)
// and draining the done channel. It is idempotent.
func (sc *Sidecar) Close() {
	_ = sc.stdin.Close()
}

// pump reads newline-delimited JSON from the sidecar's stdout and dispatches
// each message to either a per-id waiter (responses) or the event channel
// (events). It runs in its own goroutine.
func (sc *Sidecar) pump(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		sc.dispatch(line)
	}
	sc.shutdown()
}

// dispatch routes one decoded line to the appropriate waiter or event channel.
// Malformed lines are silently dropped (degrade-never-crash invariant).
func (sc *Sidecar) dispatch(line []byte) {
	// Peek at the "type" field only before doing a full decode.
	var env struct {
		Type MessageType `json:"type"`
	}
	if err := json.Unmarshal(line, &env); err != nil {
		return // malformed — skip
	}

	switch env.Type {
	case TypeResponse:
		var resp ResponseMsg
		if err := json.Unmarshal(line, &resp); err != nil {
			return
		}
		sc.mu.Lock()
		ch, ok := sc.waiters[resp.ID]
		if ok {
			delete(sc.waiters, resp.ID)
		}
		sc.mu.Unlock()
		if ok {
			// Non-blocking send: the channel is buffered(1) and the waiter
			// is guaranteed to be listening, so this never blocks.
			ch <- resp
		}
		// No waiter found means the call already timed out — silently drop.

	case TypeEvent:
		var ev EventMsg
		if err := json.Unmarshal(line, &ev); err != nil {
			return
		}
		e := Event{Name: ev.Name, Data: ev.Data}
		select {
		case sc.events <- e:
		default:
			// Channel full: drop the oldest, then push the newest.
			select {
			case <-sc.events:
			default:
			}
			select {
			case sc.events <- e:
			default:
			}
		}
	}
}

// shutdown closes the done channel and unblocks all pending waiters with a
// "sidecar closed" error response. It is idempotent via sync.Once.
func (sc *Sidecar) shutdown() {
	sc.doneOnce.Do(func() {
		close(sc.done)

		sc.mu.Lock()
		waiters := sc.waiters
		sc.waiters = make(map[string]chan ResponseMsg)
		sc.mu.Unlock()

		for id, ch := range waiters {
			ch <- ResponseMsg{
				Type:  TypeResponse,
				ID:    id,
				OK:    false,
				Error: "sidecar closed",
			}
		}

		// Close the events channel after unblocking all waiters.
		close(sc.events)
	})
}

// nextID returns a unique, monotonically increasing string ID for a request.
func (sc *Sidecar) nextID() string {
	n := sc.seq.Add(1)
	return fmt.Sprintf("r%d", n)
}
