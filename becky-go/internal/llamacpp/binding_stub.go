//go:build !llamacgo

// This is the default (cgo-free) build of the in-process llama binding: it is a no-op
// stub so the whole becky-go module — CI, cloud, and every other tool — builds with pure
// Go and no llama.dll dependency. The real in-process binding (binding_cgo.go + the C
// shim) is compiled only when becky-edit is built with `-tags llamacgo` + CGO_ENABLED=1.
// With this stub, Available() is false, so becky-edit falls back to the warm llama-server.
package llamacpp

import "errors"

// ErrUnavailable means this binary was not built with the in-process llama binding.
var ErrUnavailable = errors.New("llamacpp: built without -tags llamacgo (in-process model unavailable)")

// DefaultBackendDir mirrors the cgo build's constant so callers can reference it
// unconditionally.
const DefaultBackendDir = `C:/llama.cpp/build/bin`

// Available reports false: no in-process model in a cgo-free build.
func Available() bool { return false }

// Load is a no-op that reports unavailability.
func Load(modelPath, backendDir string, nGpuLayers, nCtx int) error { return ErrUnavailable }

// Complete is a no-op that reports unavailability.
func Complete(prompt string, maxTokens int, temp float32, seed uint32) (string, error) {
	return "", ErrUnavailable
}

// Close is a no-op.
func Close() {}
