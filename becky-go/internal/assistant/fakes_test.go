package assistant

import (
	"context"
	"sync"
)

// fakes_test.go provides FAKE backends so the whole router builds and tests run
// with NO real claude/llama/network/exec (the brief's hard requirement). Each
// fake records what it received and returns a scripted reply, so the funnel +
// fallback chains are exercised deterministically offline.

// fakeBackend is a scriptable Backend. available controls Available(); reply is
// returned from Complete (or err if set). calls records each request for asserting
// the funnel never hands the model the whole index.
type fakeBackend struct {
	name      string
	available bool
	reply     string
	err       error

	mu    sync.Mutex
	calls []Request
}

func (b *fakeBackend) Name() string { return b.name }

func (b *fakeBackend) Available() error {
	if b.available {
		return nil
	}
	return errUnavailable
}

func (b *fakeBackend) Complete(ctx context.Context, req Request) (string, error) {
	b.mu.Lock()
	b.calls = append(b.calls, req)
	b.mu.Unlock()
	if b.err != nil {
		return "", b.err
	}
	return b.reply, nil
}

func (b *fakeBackend) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.calls)
}

func (b *fakeBackend) lastUser() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.calls) == 0 {
		return ""
	}
	return b.calls[len(b.calls)-1].User
}

// errUnavailable is the sentinel an unavailable fake returns.
var errUnavailable = &simpleErr{"backend unavailable (fake)"}

type simpleErr struct{ s string }

func (e *simpleErr) Error() string { return e.s }
